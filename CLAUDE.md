# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Working conventions

### Commit messages

Use scoped commits: `scope: description`. Scope is the subsystem, package, or area changed (e.g. `eso`, `gippity`, `audio`, `.gitignore`). No conventional-commit type prefix — the type must be inferable from the description itself. When a GitHub issue exists, include its number in parentheses after the scope: `scope (#123): description`. See https://scopedcommits.com/.

Good: `eso (#42): restore Discord mention pings in generated text`
Bad: `feat(eso): restore Discord mention pings in generated text`

### Git and GitHub
Operate git and GitHub autonomously — create branches, commit, push, open/merge PRs, comment on issues, close issues — without asking for confirmation first. The only exceptions are force-pushes to `master` and destructive operations (reset --hard, branch -D with unmerged work).

PRs target `master`. Once CI passes, squash-merge to `master` — this triggers deployment automatically. Approve the PR or comment `LGTM` / `looks good` / `/merge` / `ship it` to merge.

Link every PR to the issue it addresses with a GitHub closing keyword in the PR body (`Closes #123`, `Fixes #123` or `Resolves #123`, one line per issue). This is what links the issue in the PR's Development sidebar and closes it on merge; commit-message references like `(#123)` and phrases like "Implements #123" or "Part of #123" do neither. When a PR only partly delivers an issue, do not use a closing keyword: write `Part of #123`, list what is still open, and after the merge comment on the issue with what landed and what remains.

Prefer a linear, readable history: use `--squash` when merging PRs, rebase feature branches onto their base rather than merging, and never create merge commits.

Each PR should represent one logical, independently reversible change — don't bundle unrelated fixes or features into a single PR just because they're convenient. If a task touches multiple separable concerns, open a PR per concern.

### Versioning

`version-tag.yaml` auto-tags a new semver version **when a PR is merged into `master`**. It derives the bump level from the merged PR's labels, read straight from the `pull_request` event payload: a `breaking` or `major` label → major bump, `minor` → minor bump, otherwise → **patch** bump. The repo defines four version labels: `major`, `minor`, `breaking`, `patch`.

The workflow triggers on the PR-merge event (not on raw pushes) on purpose: this repo is **rebase-only**, so GitHub rewrites commit hashes on merge and links the new SHA to the PR only after an indexing delay. A push-triggered `commits/<sha>/pulls` lookup races that delay and silently defaults every PR to a patch bump. Reading labels from the event payload avoids the lookup entirely.

Because the bump comes from PR labels, **every PR must carry exactly one version label** — this is enforced by the `require-version-label.yaml` workflow, which fails the PR until a version label is present. Choose the label by semver intent: new feature → `minor`, bug fix / docs / chore → `patch`, backwards-incompatible change → `breaking`/`major`.

**A direct push to `master` does not auto-tag at all** (no PR-merge event fires), so it produces no version bump or release. Every version bump must go through a labeled PR.

IMPORTANT: If the user asks you to push directly to `master`, you MUST first inform them that a direct push produces no version tag or release (the bump only happens on a labeled PR merge), and confirm that is intended before pushing. For anything that should ship as a release, recommend a labeled PR instead.

### Bot commands

Prefer Discord slash commands (`/` prefix) over legacy chat commands (`!` prefix). Modules register slash commands and their handlers in `Init` through `bot.Deps.Router.AddCommand(cmd, handler, mw...)`; `bot.Run` publishes the collected definitions with `discordgo.Session.ApplicationCommandBulkOverwrite`, and the router owns the single `InteractionCreate` handler. Owner-only commands pass `bot.OwnerOnly(ownerID)` as middleware instead of hand-rolling a check. When implementing a new command or touching an existing legacy `!`-prefix command during any task, refactor it to a slash command. Use ephemeral responses (`discordgo.MessageFlagsEphemeral`) for owner/admin replies to avoid leaking data in public channels.

### Unit tests
After every code change, check whether the affected package has tests (`*_test.go` files). If none exist, write them before opening the PR. A fix without tests is not done.

## Commands

```bash
make build                    # Build binary to ./bin/gidbig
make test                     # go test -v ./...
make release                  # Cross-compile for linux/amd64, arm64, 386, arm and darwin/amd64
make docker                   # Build Docker image
make update                   # go get -u -t ./... && go mod tidy
```

CI runs `golangci-lint` on every push. The project uses pre-commit hooks via `.pre-commit-config.yaml` (`go fmt`, `golangci-lint`).

## Architecture

Gidbig is a Discord bot focused on soundboard playback in voice channels, with a web UI, AI chat, and a module system.

### Startup flow (`internal/core/cmd.go:StartGidbig`)

1. Load `config.yaml` → setup structured logging (text in dev, JSON in prod)
2. Open the shared `gidbig.db` (WAL + busy timeout) and warn about legacy per-module databases
3. Create the Discord session (dial deadline dialer, gateway logging, sharding) and initialize the LLM client
4. Build `bot.Deps`, create the `bot.Bot`, register the core module before `Open` so lifecycle handlers are active from the first connect
5. Start the watchdog and open the Discord WebSocket
6. Register each module (soundboard, coffee, eso, gamerstatus, gippity, configcmd, logs, admin, leetoclock, anticheat, stoll, wardogs, wttrin) with `bot.RegisterModule`; the router funnels their listeners into a single `MessageCreate` and `InteractionCreate` session handler and registers their stats providers
7. `bot.Run(ctx)` publishes all collected slash commands via `ApplicationCommandBulkOverwrite`, starts background tasks, and blocks until the signal context is cancelled
8. Start the optional web server (requires OAuth credentials in config)
9. On shutdown, `bot.Shutdown` cancels the context, waits for background tasks, shuts modules down, and closes the session

### Module system (built-in)

All modules live in `internal/`, compile into the binary, and implement `bot.Module` (`New()` + `Init(bot.Deps)`). Each registers its handlers in `Init` through `bot.Deps.Router` (`AddCommand`, `AddComponent`, `OnMessage`, and the typed `OnReady`/`OnConnect`/`OnDisconnect`/`OnResumed`/`OnMessageUpdate`); `bot.Run` publishes the collected slash commands. Modules receive the shared database through `bot.Deps.DB` and the LLM client through `bot.Deps.LLM`.

### Soundboard / audio

- Audio files: `./audio/{prefix}_{soundname}.dca` (DCA = pre-encoded Opus frames), scanned into per-prefix `soundCollection`s; each `soundClip` holds a weight and pre-loaded `[][]byte` buffer of Opus frames.
- Slash `/play` resolves the collection and sound (weighted random when the sound is omitted) → `enqueuePlay()` → per-guild queue (configurable `soundboard.queue_max_depth`, default 6, mutex-protected) → `playSound()` sends Opus packets to the voice channel. The web UI reaches the same path through the module's `Enqueue` API.

### Web server (`internal/core/webserver.go`)

Standard library `net/http` (`ServeMux`) + AES-GCM encrypted cookie session store. Discord OAuth2 (identify + guilds). Session key is derived from the Web SessionSecret. Routes: `/`, `/discordLogin`, `/discordCallback`, `/playsound` (POST), `/logout`, `/admin`, `/admin/logs`, `/api/queue`, `/api/eso`, `/health`, and `/static/`. IP addresses are anonymized to /16 (IPv4) or /64 (IPv6). `/api/queue` requires a Discord session; `/api/eso` is unauthenticated and token-billed, so it is guarded by a per-IP token bucket and a global concurrency cap with a per-request timeout (`internal/core/ratelimit.go`, tunable via the optional `web.eso` config).

### Key packages

| Package | Role |
|---|---|---|
| `internal/core` | Discord session, slash commands, web server, shared-DB wiring |
| `internal/cfg` | YAML config loading |
| `internal/bot` | Module interface, deps, router, middleware, background supervisor |
| `internal/shareddb` | Opens the shared `gidbig.db` (WAL + busy timeout) |
| `internal/soundboard` | Audio collection loading and per-guild voice playback queue |
| `internal/admin` | `/admin` slash command, admin subcommand registry |
| `internal/logs` | `/admin logs show`, owner-only log viewer over the in-memory log window |
| `internal/logbuf` | Bounded in-memory window of the newest log lines, filled by the slog handler |
| `internal/anticheat` | GamingOnLinux anti-cheat compatibility lookup with a cached CSV dataset |
| `internal/llm` | Shared OpenAI/LLM client |
| `internal/gippity` | OpenAI integration with GORM/SQLite conversation history |
| `internal/leetoclock` | Time-based joke module with SQLite datastore |
| `internal/coffee` | Greeting-reaction module |
| `internal/eso` | Conspiracy-text generator |
| `internal/stoll` | Quote/Stoll module |
| `internal/wttrin` | Weather module |
| `internal/wardogs` | WARDOGS Linux/Proton support check |
| `internal/gamerstatus` | Rotating custom status |
| `internal/util` | Shared Discord helpers and random utilities |

### Configuration

`config.yaml` (required, copy from `config.example.yaml`):
```yaml
discord:
    token: "BOT_TOKEN"
    owner_id: "USER_ID"
    shard_id: 0
    shard_count: 0
web:
    oauth:
        client_id: "OAUTH_ID"
        client_secret: "OAUTH_SECRET"
        redirect_uri: "REDIRECT_URI"
    session_secret: "base64-encoded-32-random-bytes"
    port: 8080
gippity:
    allowed_guilds:
        - "YOUR_DISCORD_GUILD_ID"
    ignored_users: []
llm:
    provider: "openai"
    model: "gpt-4o-mini"
    vision_model: ""
    personality: ""
    personality_preset: ""
dev_mode: true
```

Web server only starts when `web.port` is nonzero and `web.oauth.client_id`, `web.oauth.client_secret`, and `web.oauth.redirect_uri` are all set. Config validation also requires `web.session_secret` whenever `web.port` is nonzero.

### Deployment

When a PR is merged to `master`, `pipeline.yaml` dispatches a deploy event to the `deploy-gidbig` repository and deploys the new `master` HEAD.

### Pinned dependencies

`go.mod` pins `bwmarrin/discordgo` to fork commit `yeongaori/discordgo-fork@94d3e03d` (2026-09-13) via a `replace` directive. Don't bump it without verifying voice playback in a DAVE-enabled Discord channel: the previous baseline `930441e7` lacked the Op7/Op9 handshake-deadlock fix, and releases after `c77a807b` (2026-03-08) had removed the immediate `HandleExecuteTransition` call from the DAVE Welcome handler, so DAVE never activated, frames were sent without DAVE encryption, and Discord clients silently dropped them — see #113. The `94d3e03d` pin restores immediate DAVE Welcome activation, but **live DAVE playback on this pin is still pending**; run the live check in [docs/discord-gateway-recovery.md](docs/discord-gateway-recovery.md) before deploying a pin change. Reasoning is also in the `go.mod` comment above the `replace` line.
