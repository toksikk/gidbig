// Package shareddb opens the single SQLite database shared by every module.
//
// SQLite allows one writer at a time. Since all modules now write the same
// file, Open enables WAL (readers never block the writer), a five-second busy
// timeout (writers wait instead of failing with SQLITE_BUSY on short
// contention) and immediate transactions (a write transaction takes the write
// lock up front instead of failing to upgrade a deferred read lock). The
// underlying connection pool is capped at a single connection so SQLite never
// sees two of this process's transactions competing for the write lock at all.
package shareddb

import (
	"strings"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// Filename is the shared database file, resolved relative to the process
// working directory. Deployments control its location with the working
// directory or a container volume mount.
const Filename = "gidbig.db"

// Open opens the shared database with the DSN every caller must use. An empty
// path falls back to Filename.
func Open(path string) (*gorm.DB, error) {
	if path == "" {
		path = Filename
	}
	db, err := gorm.Open(sqlite.Open(dsn(path)), &gorm.Config{})
	if err != nil {
		return nil, err
	}

	// SQLite has a single writer. Serialising every module's access on one
	// connection means no two transactions of this process can contend for the
	// write lock, so the deferred read-then-write upgrade that returns
	// SQLITE_BUSY immediately (ignoring busy_timeout) cannot happen. Combined
	// with _txlock=immediate this is belt and braces: the pool is the strong
	// guarantee, the DSN option helps other connections (for example sqlite3
	// on the same file) behave too.
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)

	return db, nil
}

// dsn adds WAL, a busy timeout and immediate transactions so concurrent access
// to the shared file does not fail with "database is locked".
func dsn(path string) string {
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	return path + sep + "_busy_timeout=5000&_journal_mode=WAL&_txlock=immediate"
}
