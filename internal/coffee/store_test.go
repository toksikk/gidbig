package coffee

import (
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func openInMemoryStore(t *testing.T) {
	t.Helper()
	var err error
	db, err = gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open in-memory store: %v", err)
	}
	if err := db.AutoMigrate(&UserBeveragePreference{}); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		if err := sqlDB.Close(); err != nil {
			t.Logf("warning: failed to close test DB: %v", err)
		}
		db = nil
	})
}

func TestSetAndGetBeverageEmoji(t *testing.T) {
	openInMemoryStore(t)

	if err := setBeverageEmoji("user1", "🍺"); err != nil {
		t.Fatalf("setBeverageEmoji: %v", err)
	}
	emoji, ok := getBeverageEmoji("user1")
	if !ok {
		t.Fatal("expected ok=true, got false")
	}
	if emoji != "🍺" {
		t.Errorf("got %q, want %q", emoji, "🍺")
	}
}

func TestGetBeverageEmoji_UnknownUser(t *testing.T) {
	openInMemoryStore(t)

	_, ok := getBeverageEmoji("unknown")
	if ok {
		t.Fatal("expected ok=false for unknown user, got true")
	}
}

func TestSetBeverageEmoji_Upsert(t *testing.T) {
	openInMemoryStore(t)

	if err := setBeverageEmoji("user2", "🍵"); err != nil {
		t.Fatalf("first setBeverageEmoji: %v", err)
	}
	if err := setBeverageEmoji("user2", "🧃"); err != nil {
		t.Fatalf("second setBeverageEmoji: %v", err)
	}

	emoji, ok := getBeverageEmoji("user2")
	if !ok {
		t.Fatal("expected ok=true after upsert")
	}
	if emoji != "🧃" {
		t.Errorf("got %q, want %q after upsert", emoji, "🧃")
	}

	var count int64
	db.Model(&UserBeveragePreference{}).Where("user_id = ?", "user2").Count(&count)
	if count != 1 {
		t.Errorf("expected 1 row, got %d (upsert created duplicate)", count)
	}
}
