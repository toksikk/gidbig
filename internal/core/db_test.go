package gidbig

import (
	"path/filepath"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/toksikk/gidbig/internal/bot"
	"github.com/toksikk/gidbig/internal/cfg"
	"github.com/toksikk/gidbig/internal/coffee"
	"github.com/toksikk/gidbig/internal/gippity"
	"github.com/toksikk/gidbig/internal/leetoclock"
)

// TestSharedDatabaseTableIsolation migrates every persisted module into one
// gidbig.db and asserts that their tables coexist under module prefixes without
// colliding on generic names.
func TestSharedDatabaseTableIsolation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gidbig.db")
	db, err := openSharedDatabase(path)
	if err != nil {
		t.Fatalf("openSharedDatabase: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, dbErr := db.DB(); dbErr == nil {
			_ = sqlDB.Close()
		}
	})

	session, err := discordgo.New("")
	if err != nil {
		t.Fatalf("discordgo.New: %v", err)
	}
	deps := bot.Deps{Session: session, Config: &cfg.Config{}, DB: db}

	if err := coffee.New().Init(deps); err != nil {
		t.Fatalf("coffee Init: %v", err)
	}
	if err := gippity.New().Init(deps); err != nil {
		t.Fatalf("gippity Init: %v", err)
	}
	if err := leetoclock.New().Init(deps); err != nil {
		t.Fatalf("leetoclock Init: %v", err)
	}

	tables := map[string]bool{}
	tableList, err := db.Migrator().GetTables()
	if err != nil {
		t.Fatalf("GetTables: %v", err)
	}
	for _, name := range tableList {
		tables[name] = true
	}

	want := []string{
		"coffee_user_beverage_preferences",
		"gippity_chat_history",
		"gippity_user_privacy",
		"leetoclock_players",
		"leetoclock_games",
	}
	for _, name := range want {
		if !tables[name] {
			t.Errorf("expected table %q in shared database", name)
		}
	}

	// The legacy, unprefixed table names must not appear in the shared database.
	for _, name := range []string{"chat_history", "user_privacy", "players", "scores", "games"} {
		if tables[name] {
			t.Errorf("unprefixed legacy table %q must not exist in the shared database", name)
		}
	}
}
