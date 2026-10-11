// Package shareddb opens the single SQLite database shared by every module.
//
// SQLite allows one writer at a time. Since all modules now write the same
// file, Open enables WAL (readers never block the writer) and a five-second
// busy timeout (writers wait instead of failing with SQLITE_BUSY on short
// contention). A long write transaction in one module can still exhaust the
// timeout, so keep write transactions short.
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
	return gorm.Open(sqlite.Open(dsn(path)), &gorm.Config{})
}

// dsn adds a busy timeout and WAL journal mode so concurrent module access to
// the shared file does not fail with "database is locked".
func dsn(path string) string {
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	return path + sep + "_busy_timeout=5000&_journal_mode=WAL"
}
