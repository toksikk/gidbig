package coffee

import (
	"testing"
)

func TestAdminGetBeveragePreference_NotFound(t *testing.T) {
	openInMemoryStore(t)

	pref, found := AdminGetBeveragePreference("unknown-user")
	if found {
		t.Errorf("expected found=false for unknown user, got pref=%v", pref)
	}
}

func TestAdminGetBeveragePreference_Found(t *testing.T) {
	openInMemoryStore(t)

	if err := setBeverageEmoji("user1", "🍵"); err != nil {
		t.Fatalf("setBeverageEmoji: %v", err)
	}

	pref, found := AdminGetBeveragePreference("user1")
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
	openInMemoryStore(t)

	prefs, err := AdminGetAllBeveragePreferences()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(prefs) != 0 {
		t.Errorf("expected 0 prefs, got %d", len(prefs))
	}
}

func TestAdminGetAllBeveragePreferences_Multiple(t *testing.T) {
	openInMemoryStore(t)

	users := []struct {
		id    string
		emoji string
	}{
		{"user-a", "☕"},
		{"user-b", "🍺"},
		{"user-c", "🧃"},
	}
	for _, u := range users {
		if err := setBeverageEmoji(u.id, u.emoji); err != nil {
			t.Fatalf("setBeverageEmoji(%q): %v", u.id, err)
		}
	}

	prefs, err := AdminGetAllBeveragePreferences()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(prefs) != 3 {
		t.Fatalf("expected 3 prefs, got %d", len(prefs))
	}
}

func TestAdminGetBeveragePreference_NilDB(t *testing.T) {
	dbMu.Lock()
	prev := db
	db = nil
	dbMu.Unlock()
	t.Cleanup(func() {
		dbMu.Lock()
		db = prev
		dbMu.Unlock()
	})

	_, found := AdminGetBeveragePreference("user1")
	if found {
		t.Error("expected found=false when DB is nil")
	}
}

func TestAdminGetAllBeveragePreferences_NilDB(t *testing.T) {
	dbMu.Lock()
	prev := db
	db = nil
	dbMu.Unlock()
	t.Cleanup(func() {
		dbMu.Lock()
		db = prev
		dbMu.Unlock()
	})

	_, err := AdminGetAllBeveragePreferences()
	if err == nil {
		t.Error("expected error when DB is nil")
	}
}
