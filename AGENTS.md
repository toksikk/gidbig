# Agent Instructions

## Verify Changes

- Use Go 1.26 (from `go.mod`). CI runs `golangci-lint` before `go test ./...`; match it with `make lint && make test` for Go changes.
- Run one package with `go test ./internal/<package>` and one test with `go test ./internal/<package> -run '^TestName$'` before the full suite.
- Tests use in-memory or temporary SQLite databases and HTTP recorders; they do not require Discord, OAuth, OpenAI, or other services.
- Add or update tests in the affected package for behavior changes. Most packages already have local `*_test.go` coverage.

## Architecture And Runtime

- `cmd/gidbig/main.go` only calls `internal/core.StartGidbig`; `internal/core/cmd.go` is the composition root for Discord, modules, slash-command registration, sound loading, and the optional web server.
- Module migration is incomplete. New-style modules implement `internal/bot.Module`, but `gippity` still exposes `Start`; wire changes according to the package's current pattern rather than assuming the interface is universal.
- Slash commands are assembled centrally and replaced globally with `ApplicationCommandBulkOverwrite`. Adding a command to a module is insufficient unless that module's `Commands()` is included in `internal/core/cmd.go`.
- The process reads `config.yaml`, `audio/`, web assets, and SQLite paths relative to the working directory. Copy `config.example.yaml`; config validation requires `discord.token`, a non-empty `gippity.allowed_guilds`, and `web.session_secret` whenever `web.port` is nonzero.
- Sound files are preloaded from `audio/{prefix}_{soundname}.dca`. Coffee and `leetoclock` default to `gidbig.db` unless `database.path` is configured.
- Prefer Discord slash commands for new or touched bot commands. Owner/admin responses should be ephemeral when they may expose private data.

## Dependency Constraint

- Keep `github.com/bwmarrin/discordgo` replaced with an explicit `yeongaori/discordgo-fork` commit. The pin `94d3e03d` includes the Op7/Op9 handshake deadlock fix and restored initial DAVE Welcome activation. `930441e7` was the previous live-verified baseline; the new pin's live DAVE playback check is still pending. Any pin change requires playback testing in a DAVE-enabled voice channel before deployment (silent-audio regression #113); see `docs/discord-gateway-recovery.md`.

## Git And Releases

- Commit messages use `scope: description`, or `scope (#123): description` when tied to an issue; do not use `feat(scope): ...` conventional-commit prefixes.
- PRs target `master` and should carry a version label: `major`/`breaking` for major, `minor` for minor, or `patch` for patch. The current gate checks for at least one such label, despite wording that says exactly one.
- Version tagging and GitHub release creation happen only when a PR merges into `master`. A direct push may deploy through `pipeline.yaml`, but it creates no version tag or release; confirm that tradeoff before a requested direct push.
