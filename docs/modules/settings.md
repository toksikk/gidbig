# settings

Directory `internal/settings`. Runtime overrides for operational settings,
persisted in the shared database and changed by the owner through `/admin
config`. Not user-facing on its own.

## What it does

- `New(db, base)` – creates the `settings` table if missing, loads every stored
  row into an in-memory cache and returns a `*Store`. `base` is the config
  loaded at boot; it supplies the values an override layers over. The shared
  `*gorm.DB` is owned by the composition root and is never closed here.
- `Store.Set/Unset` – validate, write, reload the cache.
- `Store.Add/Remove` – edit one item of a `stringlist` key. Under the write lock
  they read the effective list, add or drop the item, and store the result
  through the same validation as `Set`. A duplicate add, a missing remove, an
  empty item or one containing a comma, and a non-list key (`ErrNotList`) are
  rejected and nothing is written.
- `Store.Bool/Int/String/StringList` – typed effective value for a key.
- `Store.Override` – the stored value only, without the config.yaml or built-in
  fallback. The llm client uses it to resolve `llm.model` per request.
- `Store.Effective` – `(value, source)` where source is `default`, `yaml` or
  `db`.
- `Keys()`/`Lookup()` – package-level accessors for the registry of runtime-editable keys.

## Precedence

```
built-in default  <  config.yaml  <  database override
```

An explicit `/admin config set` is operator intent, so the database row wins
over the file; the file stays the deploy-time seed. Removing the row with
`/admin config unset` falls back to the file. The bot never writes
`config.yaml`.

The source is derived without duplicating the default constants: `New` also
decodes an empty document, which applies only the built-in defaults, and a value
equal to that baseline is reported as `default`. One consequence is pinned by
test: a config.yaml value that happens to equal the built-in default is reported
as `default`, because the effective value is identical either way.

## Hot, cold and secret keys

A key is either **hot** (applies to the next read) or **cold** (`Cold: true`,
read only at boot). Cold keys — every non-secret `cfg.Config` field that is not
hot, for example `discord.shard_id`, `web.port`, `llm.provider` or `dev_mode` —
are registered with `Extract` only and carry the `Note` "restart required: edit
config.yaml". They are listed and readable with their value and source, but
`Set`/`Unset`/`Add`/`Remove` return `ErrRestartRequired`, which is distinct from
`ErrUnknownKey`. A stored row for a cold key (hand-edited) is ignored with a
warning on reload, so it can never look in force.

`Note` is an optional per-key warning that every surface renders the same way.

Secrets (`discord.token`, `web.oauth.client_secret`, `web.session_secret`,
`llm.api_key`) are not registered, so they are indistinguishable from a typo.
Every entry point (`Lookup`, `Set`, `Unset`, `Add`, `Remove`, `Effective`,
`Override`) also rejects a key flagged `Secret`, so a future addition cannot leak
a credential even by mistake.

## Registry, validation and keys

`registry.go` holds the key table: name, kind, `Extract` (read the deploy-time
value from a `*cfg.Config`), `Apply` (write a raw string into a config copy) and
an optional `Validate` for a rule `cfg.Validate` does not cover.

`Set` reuses the boot validation instead of growing a second set of rules: it
builds a copy of the base config, applies **every** stored override, then the
new value, and runs `cfg.Validate` on the result. Applying all overrides first
matters — two values that are individually valid can be invalid together (tools
enabled plus a zero iteration cap), and the file alone would not catch that.

Eight keys carry a registry-local rule. Six of them restate a `cfg.Validate` rule — the four positive-value caps, the non-empty `allowed_guilds` and the `reasoning_effort` enum — so the store can judge a single stored row on its own without building a merged config on every read:

- `llm.model` must not be empty; an empty model would mean the built-in default
  (or be invalid for openrouter), so `unset` is the way back.
- `gippity.rate_limit_messages_per_hour`, `gippity.classifier_rate_limit_per_hour`
  and `soundboard.queue_max_depth` must be greater than zero, mirroring the limit
  each consumer floors.
- `llm.max_tokens` must be greater than zero. `cfg.Decode` applies the 300
  default, matching `llm.DefaultMaxTokens`, so the cap cannot be driven to zero
  (which would disable completions) from Discord.
- `gippity.allowed_guilds` must hold at least one item and no empty item,
  mirroring `cfg.Validate`, so the last allowed guild cannot be removed.
- `gippity.ignored_users` may be empty (nobody ignored) but must not contain an
  empty item.
- `gippity.tools.reasoning_effort` must be one of the accepted enum values;
  the registry reuses `cfg.ValidateReasoningEffort` rather than copying the list.

`gippity.tools.max_iterations` deliberately has no rule: a non-positive cap is
legal while tools are off, and the loop then makes no rounds and forces a final
answer, so such a value is honoured and reported as in force.

## Cache

The cache is derived, never authoritative. `Set` order is validate → database
commit → reload cache, so a crash between the write and the swap only costs
staleness until the next reload or restart. The cache is published with an
`atomic.Pointer` to an immutable map: reads are lock-free (hot answer paths pay
almost nothing) and a writer swaps the whole snapshot rather than mutating it.
The mutex is held across the validation, the SQLite write and the cache reload, so two concurrent writes cannot each validate the pre-change state. It is never held across a Discord or other network call.

A stored value that does not parse (for example a row edited by hand with
`sqlite3`) is logged and falls back to the `config.yaml` value, so a bad row
cannot break an answer path. `Effective` and the typed getters share one rule: a stored
row is used only when it is *applicable* — it parses for the key's kind and passes
the key's own rule — so a row advertised with source `db` is always the value a
consumer receives. One unusable row also cannot block writes to other keys: `Set`
logs and skips it while validating, and `unset` clears it.

The combination is checked as well as each row. A row is validated against the
`config.yaml` of the day it was written, so a later deployment edit can leave the
two jointly invalid — tools enabled in the file, a zero iteration cap in a row —
without any single write being able to produce it. `New` therefore reports the
combination on every load, `/admin config list` prints it, and every `set` is
rejected until the combination is valid again. The values are still published in
that state, so the owner can see and fix them from Discord rather than needing to
stop the bot, and `unset` stays allowed because removing a row is how it gets
fixed. `Unset` applies the same check and refuses a
removal that would *introduce* an invalid combination, so an already-invalid
state can always be cleared.

## Key flow

```mermaid
sequenceDiagram
    participant Owner
    participant CC as configcmd
    participant S as settings.Store
    participant DB as gidbig.db
    participant M as gippity / soundboard

    Owner->>CC: /admin config set gippity.tools.enabled true
    CC->>S: Set(key, value, ownerID)
    S->>S: registry lookup + Validate
    S->>S: merged := base + every stored override + new value
    S->>S: cfg.Validate(merged)
    alt invalid
        S-->>CC: error (nothing written)
        CC-->>Owner: rejected + reason + unchanged value
    else valid
        S->>DB: upsert settings row
        S->>S: reload cache (atomic swap)
        S-->>CC: nil
        CC-->>Owner: effective value + source
    end
    M->>S: Bool/Int/String on the next message
    S-->>M: runtime value
```
