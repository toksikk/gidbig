package gippity

import (
	"testing"
)

func TestAdminGetUserPrivacy_Default(t *testing.T) {
	m, _ := setupGippityTest(t)

	if !m.adminGetUserPrivacy("unknown-user") {
		t.Error("expected default privacy=true for unknown user")
	}
}

func TestAdminGetUserPrivacy_ExplicitOff(t *testing.T) {
	m, _ := setupGippityTest(t)

	if err := m.setUserPrivacy("user1", false); err != nil {
		t.Fatalf("setUserPrivacy: %v", err)
	}

	if m.adminGetUserPrivacy("user1") {
		t.Error("expected privacy=false after explicit opt-out")
	}
}

func TestAdminGetAllUserPrivacy_Empty(t *testing.T) {
	m, _ := setupGippityTest(t)

	settings, err := m.adminGetAllUserPrivacy()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(settings) != 0 {
		t.Errorf("expected 0 settings, got %d", len(settings))
	}
}

func TestAdminGetAllUserPrivacy_Multiple(t *testing.T) {
	m, _ := setupGippityTest(t)

	if err := m.setUserPrivacy("user-a", true); err != nil {
		t.Fatalf("setUserPrivacy user-a: %v", err)
	}
	if err := m.setUserPrivacy("user-b", false); err != nil {
		t.Fatalf("setUserPrivacy user-b: %v", err)
	}

	settings, err := m.adminGetAllUserPrivacy()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(settings) != 2 {
		t.Fatalf("expected 2 settings, got %d", len(settings))
	}
	if !settings["user-a"] {
		t.Error("expected user-a privacy=true")
	}
	if settings["user-b"] {
		t.Error("expected user-b privacy=false")
	}
}

func TestAdminHasConversationHistory_NoHistory(t *testing.T) {
	m, _ := setupGippityTest(t)

	if m.adminHasConversationHistory("unknown-user") {
		t.Error("expected false for user with no history")
	}
}

func TestAdminHasConversationHistory_WithHistory(t *testing.T) {
	m, _ := setupGippityTest(t)

	if _, err := m.db.Exec(
		`INSERT INTO chat_history (user_id, channel_id, timestamp, message, message_id, guild_id, is_bot_mention) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"user1", "chan1", 1000, "hello", "msg1", "guild1", 0,
	); err != nil {
		t.Fatalf("insert: %v", err)
	}

	if !m.adminHasConversationHistory("user1") {
		t.Error("expected true for user with history")
	}
}

func TestAdminGetUsersWithHistory_Empty(t *testing.T) {
	m, _ := setupGippityTest(t)

	users, err := m.adminGetUsersWithHistory()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(users) != 0 {
		t.Errorf("expected 0 users, got %d", len(users))
	}
}

func TestAdminGetUsersWithHistory_Multiple(t *testing.T) {
	m, _ := setupGippityTest(t)

	for i, uid := range []string{"user-a", "user-b", "user-a"} {
		if _, err := m.db.Exec(
			`INSERT INTO chat_history (user_id, channel_id, timestamp, message, message_id, guild_id, is_bot_mention) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			uid, "chan1", int64(1000+i), "msg", "msg"+string(rune('0'+i)), "guild1", 0,
		); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	users, err := m.adminGetUsersWithHistory()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(users) != 2 {
		t.Fatalf("expected 2 distinct users, got %d: %v", len(users), users)
	}
}
