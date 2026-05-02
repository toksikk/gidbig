# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Working conventions

### Git and GitHub
Operate git and GitHub autonomously — create branches, commit, push, open/merge PRs, comment on issues, close issues — without asking for confirmation first. The only exceptions are force-pushes to `master` and destructive operations (reset --hard, branch -D with unmerged work).

PRs target `master`. Once CI passes, squash-merge to `master` — this triggers deployment automatically. Approve the PR or comment `LGTM` / `looks good` / `/merge` / `ship it` to merge.

Prefer a linear, readable history: use `--squash` when merging PRs, rebase feature branches onto their base rather than merging, and never create merge commits.

Each PR should represent one logical, independently reversible change — don't bundle unrelated fixes or features into a single PR just because they're convenient. If a task touches multiple separable concerns, open a PR per concern.

### Unit tests
After every code change, check whether the affected package has tests (`*_test.go` files). If none exist, write them before opening the PR. A fix without tests is not done.

## Commands

```bash
make build                    # Build binary to ./bin/gidbig
make test                     # go test -v ./...
make release                  # Cross-compile for linux/amd64, arm64, 386, arm and darwin/amd64
make docker                   # Build Docker image
make update                   # go get -u -t ./... && go mod tidy
make build_with_local_plugins # Build with local plugin path replacements
```

CI runs `golangci-lint` on every push. The project uses pre-commit hooks for `go fmt`, `go lint`, and `golangci-lint-full`.

## Architecture

Gidbig is a Discord bot focused on soundboard playback in voice channels, with a web UI, AI chat, and a plugin system.

### Startup flow (`internal/core/cmd.go:StartGidbig`)

1. Load `config.yaml` → setup structured logging (text in dev, JSON in prod)
2. Scan `./audio/` for `{prefix}_{soundname}.dca` files → build `COLLECTIONS`
3. Start optional web server (requires OAuth credentials in config)
4. Pre-load all `.dca` audio into memory as Opus frame buffers
5. Open Discord WebSocket, register `onMessageCreate` handler
6. Call `Start()` on every built-in plugin (coffee, eso, gamerstatus, gippity, leetoclock, stoll, wttrin)
7. Load dynamic plugins from `./plugins/*.so` via `gbploader.LoadPlugins`

### Plugin system

**Built-in plugins** (`internal/`) — compiled into the binary, each has a `Start(*discordgo.Session)` called from `StartGidbig`. They register their own `discordgo` event handlers independently.

**Dynamic plugins** (`plugins/*.so`) — loaded at runtime via Go's `plugin` package. Must export `Start(*discordgo.Session)`, `PluginName string`, and `PluginVersion string`.

### Soundboard / audio

- Audio files: `./audio/{prefix}_{soundname}.dca` (DCA = pre-encoded Opus frames)
- `soundCollection` groups sounds under a command prefix; `soundClip` holds a weight and pre-loaded `[][]byte` buffer of Opus frames
- `onMessageCreate` matches `!{prefix} [soundname]` → `enqueuePlay()` → per-guild queue (max 6 items, mutex-protected) → `playSound()` sends Opus packets to voice channel
- Weighted random selection when no specific sound is named

### Web server (`internal/core/webserver.go`)

Gorilla mux + sessions. Discord OAuth2 (identify + guilds). Session key is the OAuth ClientSecret. Routes: `/`, `/discordLogin`, `/discordCallback`, `/playsound` (POST), `/logout`. IP addresses are anonymized to /16 (IPv4) or /64 (IPv6).

### Key packages

| Package | Role |
|---|---|
| `internal/core` | Discord session, soundboard, web server |
| `internal/cfg` | YAML config loading |
| `internal/gbploader` | Dynamic `.so` plugin loader |
| `internal/gippity` | OpenAI integration with GORM/SQLite conversation history |
| `internal/leetoclock` | Time-based joke plugin with SQLite datastore |
| `internal/coffee` | Greeting-reaction plugin |
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
  port: 8080
dev_mode: true
```

Web server only starts when `web.port`, `web.oauth.client_id`, and `web.oauth.client_secret` are all set.

### Deployment

When a PR is merged to `master`, `pipeline.yaml` dispatches a deploy event to the `deploy-gidbig` repository and deploys the new `master` HEAD.

### Pinned dependencies

`go.mod` pins `bwmarrin/discordgo` to fork commit `yeongaori/discordgo-fork@930441e7` (2026-03-07) via a `replace` directive. Don't bump it without verifying voice playback in a DAVE-enabled Discord channel: every fork release after `c77a807b` (2026-03-08) removed the immediate `HandleExecuteTransition` call from the DAVE Welcome handler, so DAVE never activates, frames are sent without DAVE encryption, and Discord clients silently drop them — see #113. Reasoning is also in the `go.mod` comment above the `replace` line.
