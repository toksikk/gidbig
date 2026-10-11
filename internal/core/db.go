package gidbig

import (
	"log/slog"
	"os"
	"path/filepath"

	"github.com/toksikk/gidbig/internal/shareddb"
	"gorm.io/gorm"
)

// legacyDatabases maps an old per-module database file to a table that the
// shared database contains after a successful migration of that module.
var legacyDatabases = []struct {
	file  string
	table string
}{
	{"gippity.db", "gippity_chat_history"},
	{"coffee.db", "coffee_user_beverage_preferences"},
	{filepath.Join("plugins", "leetoclock.sqlite"), "leetoclock_players"},
}

// warnLegacyDatabases logs a warning when a legacy per-module database still
// exists but its data has not been migrated into the shared database. Without
// this, an upgrade silently creates an empty shared database and the data looks
// lost until the operator finds docs/database-migration.md. The warning never
// moves data.
func warnLegacyDatabases(shared *gorm.DB) {
	if shared == nil {
		return
	}
	var found []string
	for _, legacy := range legacyDatabases {
		if shared.Migrator().HasTable(legacy.table) {
			continue // already migrated
		}
		if _, err := os.Stat(legacy.file); err == nil {
			found = append(found, legacy.file)
		}
	}
	if len(found) == 0 {
		return
	}
	slog.Warn("legacy per-module databases found but the shared database has no migrated tables for them; migrate the data before trusting this install",
		"legacy_files", found,
		"shared_database", shareddb.Filename,
		"guide", "docs/database-migration.md",
	)
}
