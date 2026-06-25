package coffee

import (
	"fmt"
	"net/url"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// newTestModule creates a Module with an isolated in-memory SQLite store.
func newTestModule(t *testing.T) *Module {
	t.Helper()
	m := New()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", url.PathEscape(t.Name()))
	gormDB, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open in-memory store: %v", err)
	}
	if err := gormDB.AutoMigrate(&UserBeveragePreference{}, &UserGreeting{},
		&MachineInventory{}, &RefillEvent{}, &DrinkEvent{},
		&PendingService{}, &SlackerEvent{}); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}
	m.dbMu.Lock()
	m.db = gormDB
	m.dbMu.Unlock()
	t.Cleanup(func() {
		m.dbMu.Lock()
		defer m.dbMu.Unlock()
		if m.db == nil {
			return
		}
		sqlDB, _ := m.db.DB()
		if sqlDB != nil {
			if err := sqlDB.Close(); err != nil {
				t.Logf("warning: failed to close test DB: %v", err)
			}
		}
		m.db = nil
	})
	return m
}

func useNow(m *Module, t *testing.T, now time.Time) {
	t.Helper()
	previous := m.nowFunc
	m.nowFunc = func() time.Time { return now }
	t.Cleanup(func() { m.nowFunc = previous })
}

func TestSetAndGetBeverageEmoji(t *testing.T) {
	m := newTestModule(t)

	if err := m.setBeverageEmoji("user1", "🍺"); err != nil {
		t.Fatalf("setBeverageEmoji: %v", err)
	}
	emoji, ok := m.getBeverageEmoji("user1")
	if !ok {
		t.Fatal("expected ok=true, got false")
	}
	if emoji != "🍺" {
		t.Errorf("got %q, want %q", emoji, "🍺")
	}

	if !m.isUserIntroduced("user1") {
		t.Error("expected user1 to be introduced after setting beverage")
	}
}

func TestIsUserIntroduced_UnknownUser(t *testing.T) {
	m := newTestModule(t)
	if m.isUserIntroduced("unknown") {
		t.Error("expected false for unknown user")
	}
}

func TestMarkUserIntroduced(t *testing.T) {
	m := newTestModule(t)

	if err := m.markUserIntroduced("user_intro"); err != nil {
		t.Fatalf("markUserIntroduced: %v", err)
	}

	if !m.isUserIntroduced("user_intro") {
		t.Error("expected user_intro to be introduced")
	}

	emoji, ok := m.getBeverageEmoji("user_intro")
	if !ok {
		t.Fatal("expected beverage to be set (fallback)")
	}
	if emoji != fallbackBeverage {
		t.Errorf("got %q, want %q", emoji, fallbackBeverage)
	}
}

func TestGetBeverageEmoji_UnknownUser(t *testing.T) {
	m := newTestModule(t)

	_, ok := m.getBeverageEmoji("unknown")
	if ok {
		t.Fatal("expected ok=false for unknown user, got true")
	}
}

func TestSetBeverageEmoji_Upsert(t *testing.T) {
	m := newTestModule(t)

	if err := m.setBeverageEmoji("user2", "🍵"); err != nil {
		t.Fatalf("first setBeverageEmoji: %v", err)
	}
	if err := m.setBeverageEmoji("user2", "🧃"); err != nil {
		t.Fatalf("second setBeverageEmoji: %v", err)
	}

	emoji, ok := m.getBeverageEmoji("user2")
	if !ok {
		t.Fatal("expected ok=true after upsert")
	}
	if emoji != "🧃" {
		t.Errorf("got %q, want %q after upsert", emoji, "🧃")
	}

	d := m.getDB()
	var count int64
	d.Model(&UserBeveragePreference{}).Where("user_id = ?", "user2").Count(&count)
	if count != 1 {
		t.Errorf("expected 1 row, got %d (upsert created duplicate)", count)
	}
}

func TestHasGreetedToday_UnknownUser(t *testing.T) {
	m := newTestModule(t)
	useNow(m, t, time.Date(2026, 5, 3, 10, 0, 0, 0, time.Local))

	if m.hasGreetedToday("unknown") {
		t.Fatal("expected false for unknown user")
	}
}

func TestHasGreetedToday_SameLocalDay(t *testing.T) {
	m := newTestModule(t)
	now := time.Date(2026, 5, 3, 10, 0, 0, 0, time.Local)
	useNow(m, t, now)

	d := m.getDB()
	if err := d.Create(&UserGreeting{
		UserID:    "user1",
		GreetedAt: time.Date(2026, 5, 3, 7, 30, 0, 0, time.Local),
	}).Error; err != nil {
		t.Fatalf("failed to create greeting: %v", err)
	}

	if !m.hasGreetedToday("user1") {
		t.Fatal("expected true for greeting earlier on the same local day")
	}
}

func TestHasGreetedToday_PreviousLocalDay(t *testing.T) {
	m := newTestModule(t)
	now := time.Date(2026, 5, 3, 10, 0, 0, 0, time.Local)
	useNow(m, t, now)

	d := m.getDB()
	if err := d.Create(&UserGreeting{
		UserID:    "user1",
		GreetedAt: time.Date(2026, 5, 2, 23, 59, 0, 0, time.Local),
	}).Error; err != nil {
		t.Fatalf("failed to create greeting: %v", err)
	}

	if m.hasGreetedToday("user1") {
		t.Fatal("expected false for greeting on the previous local day")
	}
}
