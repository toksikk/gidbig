# core

Package `gidbig`, directory `internal/core`. Composition root and core module.

- `cmd.go` – `StartGidbig`: opens the shared database, builds the runtime settings store, builds the session and the `*llm.Client`, wires modules through `bot.Bot`, waits for signal, shuts down.
- `module.go` – the core `bot.Module`: lifecycle logging (`onReady`/`onConnect`/`onDisconnect`/`onResumed`), ping/pong, `/status`, `/uptime`.
- `tools.go` – registers the `bot_status` LLM tool, which reports the process runtime line (heap, sys, stack, goroutines, GC cycles, uptime) plus aggregated `StatsProvider` counters for the answer path.
- `slashcmd.go` – `/uptime` rendering helpers.
- `status.go` / `status_users.go` – `/status` snapshot collection and rendering. The snapshot carries an `LLM` section with the effective, non-secret LLM configuration (provider, models, base URL, credential source, persona source, classifier, reasoning effort, tools, rate limits, timeouts), sourced from the shared `*llm.Client` (`Info`) and the gippity config via `Module.llmStatus`.
- `webserver.go` – optional OAuth session web UI, sound + eso APIs. Served by an `http.Server` with read/write/idle timeouts and a `securityHeaders` middleware (CSP with `frame-ancestors 'none'`, `X-Frame-Options`, `nosniff`, `Referrer-Policy`); the OAuth callback answers 502 when the Discord user lookup fails. See "Web sessions and endpoints" below.
- `webadmin.go` – the owner-only `/admin` panel: it renders the same runtime
  settings the `/admin config` command edits, through the `configcmd` module, and
  applies a set or an unset with the same validation. Access needs the session to
  belong to `discord.owner_id` and to have been issued within
  `adminMaxSessionAge` (12h); every refusal is a plain 404 so the panel cannot be
  discovered. A submission needs the per-session form token and a matching
  `Origin`, and is a POST that redirects back to the table.
- `ratelimit.go` – per-IP token bucket and global concurrency cap guarding the unauthenticated, token-billed `/api/eso`, tuned through the optional `web.eso` config (`resolveEsoLimits`).
- `wsdeadline.go` – read/write deadlines on discordgo websockets.
- `version.go`, `discordlog.go`, `structs.go`.

The soundboard lives in `internal/soundboard`; the web server reaches it through
the module's exported `Collections`, `Enqueue` and `QueueStatus` API.

## Startup sequence

`StartGidbig` builds the session and `Deps`, registers every module with a
`bot.Bot`, then calls `bot.Run(ctx)`. The Bot owns the single `MessageCreate` and
`InteractionCreate` session handlers, the background supervisor, and the
slash-command bulk overwrite; no other code calls `AddHandler`.

```mermaid
sequenceDiagram
    participant main
    participant core as core.StartGidbig
    participant cfg
    participant dg as discordgo
    participant llm
    participant bot as bot.Bot
    participant mod as Modules
    participant web as WebServer

    main->>core: StartGidbig()
    core->>cfg: GetConfig()
    core->>core: shareddb.Open → settings.New(db, conf)
    core->>dg: New("Bot token")
    core->>llm: New(Options + ModelOverride) → *llm.Client
    core->>bot: New(Deps) — installs single session handlers
    core->>bot: RegisterModule(core) before Open
    core->>dg: Open()
    dg-->>core: READY
    core->>bot: RegisterModule(soundboard/coffee/eso/gamerstatus/gippity/admin/leetoclock/anticheat/stoll/wardogs/wttrin)
    bot-->>core: module handlers registered, stats registered
    core->>web: go startWebServer(config, session, soundboard, eso, configcmd) (if configured)
    core->>bot: Run(ctx) — bulk overwrite commands, start background, block
    core->>core: wait SIGINT/SIGTERM, cancel ctx
    bot-->>core: ctx done
    core->>bot: Shutdown() cancels, waits, closes session
```

## Web sessions and endpoints

- **Session cookie** – AES-GCM encrypted, `HttpOnly`, `SameSite=Lax`, `MaxAge`
  7 days, and `Secure` when `web.oauth.redirect_uri` is `https`. It stores the
  user ID, name, avatar and login time (`IssuedAt`); the OAuth access token is
  used once to resolve the user and never stored. Every session route
  (`/`, `/playsound`, `/api/queue`) checks `validSession`: user set and
  `IssuedAt` at most `sessionMaxAge` (7 days) old; a cookie without `IssuedAt`
  counts as expired. Logout only clears the cookie; expiry is the bound.
- **OAuth state** – `/discordLogin` stores a random state in the caller's own
  session cookie; `/discordCallback` compares it in constant time, rejects an
  empty one with 403, never logs either value, and clears it on login.
- **`/playsound`** – POST only (405 with `Allow: POST` otherwise), `Origin` must
  match the redirect URI's origin (`validOrigin`, shared with `/admin`; a
  missing `Origin` is accepted), then a valid session (401).
- **`/api/queue`** – only the queues of guilds where the caller currently has a
  voice state in the bot's cache (`voiceGuilds`); empty when not in voice.
- **Logs** – web request logs carry the path (never the query, which holds the
  OAuth code) and the client IP anonymized to /16 or /64 (`anonymizedIP`).
- **UI** – `internal.html` builds toasts and queue rows with `textContent`; no
  `innerHTML`.

```mermaid
sequenceDiagram
    participant B as Browser
    participant W as webServer
    participant D as Discord OAuth
    participant SB as soundboard

    B->>W: GET /discordLogin
    W-->>B: Set-Cookie session{State}, 307 to Discord with state
    B->>D: authorize
    D-->>B: redirect /discordCallback?code&state
    B->>W: GET /discordCallback (cookie)
    W->>W: constant-time state compare, else 403
    W->>D: exchange code, fetch @me
    W-->>B: Set-Cookie session{user, IssuedAt} (Secure on https, 7d)
    B->>W: POST /playsound (Origin, cookie)
    W->>W: POST? Origin? validSession?
    W->>SB: Enqueue(user, voice guild, command, sound)
    B->>W: GET /api/queue
    W->>SB: QueueStatus()
    W-->>B: queues filtered to caller's voice guilds
```

## Web admin panel

`/admin` is a second front end for the runtime settings the `/admin config`
command edits. Both call the same `configcmd` methods, so a value is validated
and applied identically whichever surface the owner uses; the panel adds no
writer of its own.

The session cookie carries the login time (`IssuedAt`, stamped in the OAuth
callback), the panel's form token and one flash message.

Each key is rendered with a control that fits its kind: a checkbox for a boolean
(Go's `PostFormValue` returns the first value of a repeated name, so the checkbox
comes before a hidden `false` fallback that covers the unticked case, which
submits nothing), a number field for an integer, a text field otherwise. The
checkbox is ticked from the effective value through the `isTrue` template helper,
so a row stored as `1` or `TRUE` shows as ticked too.

```mermaid
sequenceDiagram
    participant O as Owner browser
    participant W as webServer.handleAdmin
    participant S as sessionStore
    participant C as configcmd
    participant T as settings.Store

    O->>W: GET /admin (session cookie)
    W->>S: Get(cookie)
    W->>W: isOwner: owner_id set, user matches, IssuedAt within 12h
    alt not the owner
        W-->>O: 404 — same response as a missing page
    else owner
        W->>C: Entries()
        C->>T: Effective(key) for every registry key
        T-->>C: value + source (or the invalid-combination error)
        C-->>W: entries + error
        W-->>O: admin.html — table, forms, fresh form token
        O->>W: POST /admin action=set|unset key value csrf
        W->>W: Origin check, constant-time token compare
        W->>C: ApplySet / ApplyUnset
        C->>T: Set / Unset (validated against config.yaml, merged)
        W-->>O: 303 to /admin, outcome flashed once
    end
```
