package coffee

import (
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func openInMemoryStore(t *testing.T) {
	t.Helper()
	gormDB, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open in-memory store: %v", err)
	}
	if err := gormDB.AutoMigrate(&UserBeveragePreference{}, &UserGreeting{}); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}
	dbMu.Lock()
	db = gormDB
	dbMu.Unlock()
	t.Cleanup(func() {
		dbMu.Lock()
		defer dbMu.Unlock()
		sqlDB, _ := db.DB()
		if err := sqlDB.Close(); err != nil {
			t.Logf("warning: failed to close test DB: %v", err)
		}
		db = nil
	})
}

func useNow(t *testing.T, now time.Time) {
	t.Helper()
	previous := nowFunc
	nowFunc = func() time.Time {
		return now
	}
	t.Cleanup(func() {
		nowFunc = previous
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

	if !isUserIntroduced("user1") {
		t.Error("expected user1 to be introduced after setting beverage")
	}
}

func TestIsUserIntroduced_UnknownUser(t *testing.T) {
	openInMemoryStore(t)
	if isUserIntroduced("unknown") {
		t.Error("expected false for unknown user")
	}
}

func TestMarkUserIntroduced(t *testing.T) {
	openInMemoryStore(t)

	if err := markUserIntroduced("user_intro"); err != nil {
		t.Fatalf("markUserIntroduced: %v", err)
	}

	if !isUserIntroduced("user_intro") {
		t.Error("expected user_intro to be introduced")
	}

	// Verify it sets fallback beverage if it didn't exist
	emoji, ok := getBeverageEmoji("user_intro")
	if !ok {
		t.Fatal("expected beverage to be set (fallback)")
	}
	if emoji != fallbackBeverage {
		t.Errorf("got %q, want %q", emoji, fallbackBeverage)
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

	d := getDB()
	var count int64
	d.Model(&UserBeveragePreference{}).Where("user_id = ?", "user2").Count(&count)
	if count != 1 {
		t.Errorf("expected 1 row, got %d (upsert created duplicate)", count)
	}
}

func TestHasGreetedToday_UnknownUser(t *testing.T) {
	openInMemoryStore(t)
	useNow(t, time.Date(2026, 5, 3, 10, 0, 0, 0, time.Local))

	if hasGreetedToday("unknown") {
		t.Fatal("expected false for unknown user")
	}
}

func TestHasGreetedToday_SameLocalDay(t *testing.T) {
	openInMemoryStore(t)
	now := time.Date(2026, 5, 3, 10, 0, 0, 0, time.Local)
	useNow(t, now)

	d := getDB()
	if err := d.Create(&UserGreeting{
		UserID:    "user1",
		GreetedAt: time.Date(2026, 5, 3, 7, 30, 0, 0, time.Local),
	}).Error; err != nil {
		t.Fatalf("failed to create greeting: %v", err)
	}

	if !hasGreetedToday("user1") {
		t.Fatal("expected true for greeting earlier on the same local day")
	}
}

func TestHasGreetedToday_PreviousLocalDay(t *testing.T) {
	openInMemoryStore(t)
	now := time.Date(2026, 5, 3, 10, 0, 0, 0, time.Local)
	useNow(t, now)

	d := getDB()
	if err := d.Create(&UserGreeting{
		UserID:    "user1",
		GreetedAt: time.Date(2026, 5, 2, 23, 59, 0, 0, time.Local),
	}).Error; err != nil {
		t.Fatalf("failed to create greeting: %v", err)
	}

	if hasGreetedToday("user1") {
		t.Fatal("expected false for greeting on the previous local day")
	}
}
