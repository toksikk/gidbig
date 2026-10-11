package gidbig

import (
	"strings"

	"github.com/toksikk/gidbig/internal/cfg"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// openSharedDatabase opens the single SQLite database shared by every module.
// Modules receive the handle through bot.Deps.DB and must not close it.
func openSharedDatabase(path string) (*gorm.DB, error) {
	if path == "" {
		path = cfg.DefaultDatabaseFilename
	}
	return gorm.Open(sqlite.Open(withSQLiteParams(path)), &gorm.Config{})
}

// withSQLiteParams adds a busy timeout and WAL journal mode so concurrent
// module access to the shared file does not fail with "database is locked".
func withSQLiteParams(path string) string {
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	return path + sep + "_busy_timeout=5000&_journal_mode=WAL"
}
