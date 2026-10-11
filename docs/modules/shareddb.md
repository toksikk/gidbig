# shareddb

Directory `internal/shareddb`. Shared SQLite access. Not user-facing.

- `shareddb.go` – `Filename` (`gidbig.db`, resolved against the process working directory) and `Open(path)`.

Every module reads and writes the same file through `bot.Deps.DB`; `Open` is called once by `internal/core/cmd.go`. The DSN enables WAL (readers never block the writer) and a five-second busy timeout (writers wait instead of failing with `SQLITE_BUSY`). Keep write transactions short so they do not exhaust the timeout.

Modules must not close the shared handle; the composition root owns its lifecycle and closes it last during shutdown.
