# Agent Instructions

## Sources Of Truth

- `CLAUDE.md` holds the detailed working conventions (commit format, git/PR/release flow, versioning, bot-command and testing rules). `GEMINI.md` and `.github/copilot-instructions.md` only point to it. Read it before changing git history or workflow behavior; do not duplicate it here.
- `docs/modules/` has one file per internal package with what it does and a sequence diagram of its key flow. Useful before touching a module's wiring.

## Verify Changes

- Use Go 1.26 (from `go.mod`; CI reads `go-version-file: go.mod`). CI runs `golangci-lint` before `go test ./...`; match it with `make lint && make test` for Go changes. There is no `.golangci` config file — lint uses defaults.
- Run one package with `go test ./internal/<package>` and one test with `go test ./internal/<package> -run '^TestName$'` before the full suite.
- Build and tests need CGO and a C compiler (`mattn/go-sqlite3`); tests use in-memory or temporary SQLite databases and HTTP recorders and do not require Discord, OAuth, OpenAI, or other services.
- Add or update tests in the affected package for behavior changes. Most packages already have local `*_test.go` coverage.

## Architecture And Runtime

- `cmd/gidbig/main.go` only calls `internal/core.StartGidbig`; `internal/core/cmd.go` is the composition root for Discord, modules, slash-command registration, sound loading, and the optional web server.
- All modules implement `internal/bot.Module` (`New()` + `Init(bot.Deps)`); the old legacy `Start` boot path is gone. Dependencies (session, config, LLM client, logger, owner ID, shared DB) are injected through `bot.Deps`.
- All modules share one SQLite file, `gidbig.db` in the working directory, via `bot.Deps.DB` (opened once in `cmd.go` with WAL + busy timeout; modules must not close it). The old `database.path` config and per-module DBs are removed; see `docs/database-migration.md`.
- Slash commands are assembled centrally in `cmd.go` and replaced globally with `ApplicationCommandBulkOverwrite`. Adding a command inside a module is insufficient unless that module's `Commands()` is appended to the `cmds` slice in `internal/core/cmd.go`.
- `/admin` aggregates modules implementing `bot.AdminProvider`; a new provider must be passed to `admin.New(...)` in `cmd.go` to appear under `/admin`.
- The process reads `config.yaml`, `audio/`, and web assets relative to the working directory. Copy `config.example.yaml`; config validation requires `discord.token`, a non-empty `gippity.allowed_guilds`, and `web.session_secret` whenever `web.port` is nonzero (`internal/cfg/cfg.go`).
- Sound files are preloaded from `audio/{prefix}_{soundname}.dca` into memory as Opus frames.
- Prefer Discord slash commands for new or touched bot commands. Owner/admin responses should be ephemeral when they may expose private data.

## Dependency Constraint

- Keep `github.com/bwmarrin/discordgo` replaced with an explicit `yeongaori/discordgo-fork` commit. The pin `94d3e03d` includes the Op7/Op9 handshake deadlock fix and restored initial DAVE Welcome activation. `930441e7` was the previous live-verified baseline; the new pin's live DAVE playback check is still pending. Any pin change requires playback testing in a DAVE-enabled voice channel before deployment (silent-audio regression #113); see `docs/discord-gateway-recovery.md`.

## Git And Releases

- Commit messages use `scope: description`, or `scope (#123): description` when tied to an issue; do not use `feat(scope): ...` conventional-commit prefixes.
- PRs target `master` and should carry a version label: `major`/`breaking` for major, `minor` for minor, or `patch` for patch. The current gate checks for at least one such label, despite wording that says exactly one.
- Version tagging and GitHub release creation happen only when a PR merges into `master`. A direct push may deploy through `pipeline.yaml`, but it creates no version tag or release; confirm that tradeoff before a requested direct push.
