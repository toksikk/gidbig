package gidbig

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/toksikk/gidbig/internal/bot"
	"github.com/toksikk/gidbig/internal/cfg"
	"github.com/toksikk/gidbig/internal/coffee"
	"github.com/toksikk/gidbig/internal/gippity"
	"github.com/toksikk/gidbig/internal/leetoclock"
	"github.com/toksikk/gidbig/internal/shareddb"
)

// captureLogs redirects the default slog logger into a buffer for the duration
// of the test.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return buf
}

func TestWarnLegacyDatabases_WarnsWhenLegacyFileUnmigrated(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.WriteFile("gippity.db", []byte{}, 0o600); err != nil {
		t.Fatal(err)
	}
	shared, err := shareddb.Open(filepath.Join(t.TempDir(), "gidbig.db"))
	if err != nil {
		t.Fatalf("shareddb.Open: %v", err)
	}
	logs := captureLogs(t)

	warnLegacyDatabases(shared)

	if !strings.Contains(logs.String(), "legacy per-module databases found") {
		t.Errorf("expected a legacy database warning, got %q", logs.String())
	}
}

func TestWarnLegacyDatabases_SilentWhenMigrated(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.WriteFile("gippity.db", []byte{}, 0o600); err != nil {
		t.Fatal(err)
	}
	shared, err := shareddb.Open(filepath.Join(t.TempDir(), "gidbig.db"))
	if err != nil {
		t.Fatalf("shareddb.Open: %v", err)
	}
	if err := shared.Exec("CREATE TABLE gippity_chat_history (id integer primary key)").Error; err != nil {
		t.Fatal(err)
	}
	logs := captureLogs(t)

	warnLegacyDatabases(shared)

	if strings.Contains(logs.String(), "legacy per-module databases found") {
		t.Errorf("expected no legacy database warning once migrated, got %q", logs.String())
	}
}

// TestSharedDatabaseTableIsolation migrates every persisted module into one
// gidbig.db and asserts that their tables coexist under module prefixes without
// colliding on generic names.
func TestSharedDatabaseTableIsolation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gidbig.db")
	db, err := shareddb.Open(path)
	if err != nil {
		t.Fatalf("shareddb.Open: %v", err)
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
