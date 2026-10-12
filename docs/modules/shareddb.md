# shareddb

Directory `internal/shareddb`. Shared SQLite access. Not user-facing.

- `shareddb.go` – `Filename` (`gidbig.db`, resolved against the process working directory) and `Open(path)`.

Every module reads and writes the same file through `bot.Deps.DB`; `Open` is called once by `internal/core/cmd.go`. `Open` enables WAL (readers never block the writer), a five-second busy timeout (writers wait instead of failing with `SQLITE_BUSY`), immediate transactions (`_txlock=immediate`, so a write transaction takes the write lock up front instead of failing to upgrade a deferred read lock) and caps the connection pool at one connection, so SQLite never sees two of this process's transactions competing for the write lock. Keep write transactions short so they do not exhaust the timeout.

Modules must not close the shared handle; the composition root owns its lifecycle and closes it last during shutdown.

Because `Open` caps the pool at one connection, any query issued on the shared `*gorm.DB` handle from inside a `db.Transaction` closure would block forever waiting for that connection. Inside a transaction, always thread the `tx` handle the closure receives (`tx.Model(...)`, `tx.Create(...)`); never call `m.db`/`Deps.DB` from within it. `internal/shareddb/txinvariant_test.go` scans every `db.Transaction` closure in `internal/` and fails if a GORM method is called on anything but the closure's `tx`.
