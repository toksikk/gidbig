# Agent Instructions

## Verify Changes

- Use Go 1.25 (from `go.mod`). CI runs `golangci-lint` before `go test ./...`; match it with `make lint && make test` for Go changes.
- Run one package with `go test ./internal/<package>` and one test with `go test ./internal/<package> -run '^TestName$'` before the full suite.
- Tests use in-memory or temporary SQLite databases and HTTP recorders; they do not require Discord, OAuth, OpenAI, or other services.
- Add or update tests in the affected package for behavior changes. Most packages already have local `*_test.go` coverage.

## Architecture And Runtime

- `cmd/gidbig/main.go` only calls `internal/core.StartGidbig`; `internal/core/cmd.go` is the composition root for Discord, modules, slash-command registration, sound loading, and the optional web server.
- Module migration is incomplete. New-style modules implement `internal/bot.Module`, but `gippity` and `leetoclock` still expose `Start`; wire changes according to the package's current pattern rather than assuming the interface is universal.
- Slash commands are assembled centrally and replaced globally with `ApplicationCommandBulkOverwrite`. Adding a command to a module is insufficient unless that module's `Commands()` is included in `internal/core/cmd.go`.
- The process reads `config.yaml`, `audio/`, web assets, and SQLite paths relative to the working directory. Copy `config.example.yaml`; config validation requires `discord.token`, a non-empty `gippity.allowed_guilds`, and `web.session_secret` whenever `web.port` is nonzero.
- Sound files are preloaded from `audio/{prefix}_{soundname}.dca`. `leetoclock` writes `plugins/leetoclock.sqlite`; coffee defaults to `gidbig.db` unless `database.path` is configured.
- Prefer Discord slash commands for new or touched bot commands. Owner/admin responses should be ephemeral when they may expose private data.

## Dependency Constraint

- Do not casually update `github.com/bwmarrin/discordgo`: `go.mod` replaces it with `yeongaori/discordgo-fork@930441e7`, the last verified commit where initial DAVE voice encryption activates. Any bump requires testing playback in a DAVE-enabled voice channel; later fork commits caused silently dropped audio (#113).

## Git And Releases

- Commit messages use `scope: description`, or `scope (#123): description` when tied to an issue; do not use `feat(scope): ...` conventional-commit prefixes.
- PRs target `master` and should carry a version label: `major`/`breaking` for major, `minor` for minor, or `patch` for patch. The current gate checks for at least one such label, despite wording that says exactly one.
- Version tagging and GitHub release creation happen only when a PR merges into `master`. A direct push may deploy through `pipeline.yaml`, but it creates no version tag or release; confirm that tradeoff before a requested direct push.
