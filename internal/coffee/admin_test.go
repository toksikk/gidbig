package coffee

import (
	"testing"
)

func TestAdminGetBeveragePreference_NotFound(t *testing.T) {
	m := newTestModule(t)

	pref, found := m.adminGetBeveragePreference("unknown-user")
	if found {
		t.Errorf("expected found=false for unknown user, got pref=%v", pref)
	}
}

func TestAdminGetBeveragePreference_Found(t *testing.T) {
	m := newTestModule(t)

	if err := m.setBeverageEmoji("user1", "🍵"); err != nil {
		t.Fatalf("setBeverageEmoji: %v", err)
	}

	pref, found := m.adminGetBeveragePreference("user1")
	if !found {
		t.Fatal("expected found=true")
	}
	if pref.BeverageEmoji != "🍵" {
		t.Errorf("BeverageEmoji = %q, want %q", pref.BeverageEmoji, "🍵")
	}
	if pref.UserID != "user1" {
		t.Errorf("UserID = %q, want %q", pref.UserID, "user1")
	}
}

func TestAdminGetAllBeveragePreferences_Empty(t *testing.T) {
	m := newTestModule(t)

	prefs, err := m.adminGetAllBeveragePreferences()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(prefs) != 0 {
		t.Errorf("expected 0 prefs, got %d", len(prefs))
	}
}

func TestAdminGetAllBeveragePreferences_Multiple(t *testing.T) {
	m := newTestModule(t)

	users := []struct {
		id    string
		emoji string
	}{
		{"user-a", "☕"},
		{"user-b", "🍺"},
		{"user-c", "🧃"},
	}
	for _, u := range users {
		if err := m.setBeverageEmoji(u.id, u.emoji); err != nil {
			t.Fatalf("setBeverageEmoji(%q): %v", u.id, err)
		}
	}

	prefs, err := m.adminGetAllBeveragePreferences()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(prefs) != 3 {
		t.Fatalf("expected 3 prefs, got %d", len(prefs))
	}
}

func TestAdminGetBeveragePreference_NilDB(t *testing.T) {
	m := newTestModule(t)
	m.dbMu.Lock()
	m.db = nil
	m.dbMu.Unlock()

	_, found := m.adminGetBeveragePreference("user1")
	if found {
		t.Error("expected found=false when DB is nil")
	}
}

func TestAdminGetAllBeveragePreferences_NilDB(t *testing.T) {
	m := newTestModule(t)
	m.dbMu.Lock()
	m.db = nil
	m.dbMu.Unlock()

	_, err := m.adminGetAllBeveragePreferences()
	if err == nil {
		t.Error("expected error when DB is nil")
	}
}
