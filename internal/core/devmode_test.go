package gidbig

import (
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/toksikk/gidbig/internal/cfg"
	"github.com/toksikk/gidbig/internal/settings"
	"github.com/toksikk/gidbig/internal/shareddb"
	"gorm.io/gorm"
)

func openDevModeDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := shareddb.Open(filepath.Join(t.TempDir(), shareddb.Filename))
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db.DB: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

func newDevModeStore(t *testing.T, db *gorm.DB, devMode bool) *settings.Store {
	t.Helper()
	yaml := "discord:\n  token: t\ngippity:\n  allowed_guilds: [guild]\n"
	if devMode {
		yaml += "dev_mode: true\n"
	}
	base, err := cfg.Decode(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("decode base config: %v", err)
	}
	store, err := settings.New(db, base)
	if err != nil {
		t.Fatalf("settings.New: %v", err)
	}
	return store
}

func TestWireDevModeLevelFollowsRuntimeChanges(t *testing.T) {
	store := newDevModeStore(t, openDevModeDB(t), false)
	var lv slog.LevelVar
	wireDevModeLevel(store, &lv)
	if lv.Level() != slog.LevelInfo {
		t.Fatalf("boot level = %v; want Info", lv.Level())
	}
	if err := store.Set(settings.KeyDevMode, "true", "owner"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if lv.Level() != slog.LevelDebug {
		t.Fatalf("after set true = %v; want Debug", lv.Level())
	}
	if err := store.Set(settings.KeyDevMode, "maybe", "owner"); err == nil {
		t.Fatal("Set with a non-boolean succeeded")
	}
	if lv.Level() != slog.LevelDebug {
		t.Fatalf("after failed set = %v; want Debug", lv.Level())
	}
	if err := store.Unset(settings.KeyDevMode, "owner"); err != nil {
		t.Fatalf("Unset: %v", err)
	}
	if lv.Level() != slog.LevelInfo {
		t.Fatalf("after unset = %v; want Info", lv.Level())
	}
}

func TestWireDevModeLevelAppliesStoredValueAtBoot(t *testing.T) {
	db := openDevModeDB(t)
	first := newDevModeStore(t, db, true)
	if err := first.Set(settings.KeyDevMode, "false", "owner"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	// A fresh store over the same database is what a restart sees.
	store := newDevModeStore(t, db, true)
	var lv slog.LevelVar
	lv.Set(slog.LevelDebug) // what setupLogging chose from config.yaml
	wireDevModeLevel(store, &lv)
	if lv.Level() != slog.LevelInfo {
		t.Fatalf("boot level = %v; want Info from the stored override", lv.Level())
	}
}
