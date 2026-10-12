# shareddb

Directory `internal/shareddb`. Shared SQLite access. Not user-facing.

- `shareddb.go` – `Filename` (`gidbig.db`, resolved against the process working directory) and `Open(path)`.

Every module reads and writes the same file through `bot.Deps.DB`; `Open` is called once by `internal/core/cmd.go`. `Open` enables WAL (readers never block the writer), a five-second busy timeout (writers wait instead of failing with `SQLITE_BUSY`), immediate transactions (`_txlock=immediate`, so a write transaction takes the write lock up front instead of failing to upgrade a deferred read lock) and caps the connection pool at one connection, so SQLite never sees two of this process's transactions competing for the write lock. Keep write transactions short so they do not exhaust the timeout.

Modules must not close the shared handle; the composition root owns its lifecycle and closes it last during shutdown.
