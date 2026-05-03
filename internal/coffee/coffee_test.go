package coffee

import (
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/toksikk/gidbig/internal/util"
)

type capturedReaction struct {
	channelID    string
	messageID    string
	emoji        string
	reactionType string
}

func captureReactions(t *testing.T) func() []capturedReaction {
	t.Helper()
	previous := reactOnMessage
	reactions := []capturedReaction{}
	reactOnMessage = func(_ *discordgo.Session, channelID, messageID, emoji, reactionType string) {
		reactions = append(reactions, capturedReaction{
			channelID:    channelID,
			messageID:    messageID,
			emoji:        emoji,
			reactionType: reactionType,
		})
	}
	t.Cleanup(func() {
		reactOnMessage = previous
	})
	return func() []capturedReaction {
		return reactions
	}
}

func useSpecialDay(t *testing.T, special bool) {
	t.Helper()
	previous := isSpecialDay
	isSpecialDay = func() bool {
		return special
	}
	t.Cleanup(func() {
		isSpecialDay = previous
	})
}

func greetingMessage(userID, content string) *discordgo.MessageCreate {
	return &discordgo.MessageCreate{
		Message: &discordgo.Message{
			ID:        "message1",
			ChannelID: "channel1",
			Content:   content,
			Author: &discordgo.User{
				ID: userID,
			},
		},
	}
}

func countGreetings(t *testing.T, userID string) int64 {
	t.Helper()
	d := getDB()
	var count int64
	if err := d.Model(&UserGreeting{}).Where("user_id = ?", userID).Count(&count).Error; err != nil {
		t.Fatalf("failed to count greetings: %v", err)
	}
	return count
}

func isSpecialGreetingEmoji(emoji string) bool {
	for _, ae := range util.Ae {
		if emoji == string(ae) {
			return true
		}
	}
	return false
}

func TestIsValidBeverageEmoji(t *testing.T) {
	tests := []struct {
		input string
		valid bool
	}{
		// valid Unicode emoji
		{"🫖", true},
		{"☕", true},
		{"🍺", true},
		{"🧃", true},
		// valid Discord custom emoji
		{"<:customemoji:123456789>", true},
		{"<a:animatedemoji:987654321>", true},
		// invalid: plain text
		{"hello", false},
		{"hello world", false},
		// invalid: empty
		{"", false},
		// invalid: number
		{"42", false},
		// invalid: mixed text
		{"coffee", false},
	}
	for _, tt := range tests {
		got := isValidBeverageEmoji(tt.input)
		if got != tt.valid {
			t.Errorf("isValidBeverageEmoji(%q) = %v; want %v", tt.input, got, tt.valid)
		}
	}
}

func TestBeverageEmojiFor(t *testing.T) {
	tests := []struct {
		userID   string
		expected string
	}{
		{"263959699764805642", "☕"},
		{"217697101818232832", "☕"},
		{"000000000000000000", "☕"},
		{"", "☕"},
	}
	for _, tt := range tests {
		got := beverageEmojiFor(tt.userID)
		if got != tt.expected {
			t.Errorf("beverageEmojiFor(%q) = %q; want %q", tt.userID, got, tt.expected)
		}
	}
}

func TestOnMessageCreate_FirstGreetingReactsAndRecordsGreeting(t *testing.T) {
	openInMemoryStore(t)
	useNow(t, time.Date(2026, 5, 3, 9, 0, 0, 0, time.Local))
	useSpecialDay(t, false)
	getReactions := captureReactions(t)

	onMessageCreate(nil, greetingMessage("user1", "moin"))

	reactions := getReactions()
	if len(reactions) != 1 {
		t.Fatalf("expected 1 reaction, got %d", len(reactions))
	}
	if reactions[0].emoji != fallbackBeverage {
		t.Errorf("reaction emoji = %q, want %q", reactions[0].emoji, fallbackBeverage)
	}
	if reactions[0].reactionType != "add" {
		t.Errorf("reaction type = %q, want add", reactions[0].reactionType)
	}
	if count := countGreetings(t, "user1"); count != 1 {
		t.Errorf("expected 1 greeting row, got %d", count)
	}
}

func TestOnMessageCreate_DuplicateSameDayGreetingIsSuppressed(t *testing.T) {
	openInMemoryStore(t)
	useNow(t, time.Date(2026, 5, 3, 9, 0, 0, 0, time.Local))
	useSpecialDay(t, false)
	getReactions := captureReactions(t)

	onMessageCreate(nil, greetingMessage("user1", "moin"))
	onMessageCreate(nil, greetingMessage("user1", "hi"))

	reactions := getReactions()
	if len(reactions) != 1 {
		t.Fatalf("expected only the first greeting to react, got %d reactions", len(reactions))
	}
	if count := countGreetings(t, "user1"); count != 1 {
		t.Errorf("expected duplicate greeting to keep 1 row, got %d", count)
	}
}

func TestOnMessageCreate_PriorDayGreetingAllowsNextDayReaction(t *testing.T) {
	openInMemoryStore(t)
	useNow(t, time.Date(2026, 5, 3, 9, 0, 0, 0, time.Local))
	useSpecialDay(t, false)
	getReactions := captureReactions(t)

	d := getDB()
	if err := d.Create(&UserGreeting{
		UserID:    "user1",
		GreetedAt: time.Date(2026, 5, 2, 23, 0, 0, 0, time.Local),
	}).Error; err != nil {
		t.Fatalf("failed to create prior greeting: %v", err)
	}

	onMessageCreate(nil, greetingMessage("user1", "moin"))

	reactions := getReactions()
	if len(reactions) != 1 {
		t.Fatalf("expected next-day greeting to react once, got %d reactions", len(reactions))
	}
	if count := countGreetings(t, "user1"); count != 2 {
		t.Errorf("expected prior and new greeting rows, got %d", count)
	}
}

func TestOnMessageCreate_SpecialDayFirstGreetingReactsAndRecordsGreeting(t *testing.T) {
	openInMemoryStore(t)
	useNow(t, time.Date(2026, 5, 3, 9, 0, 0, 0, time.Local))
	useSpecialDay(t, true)
	getReactions := captureReactions(t)

	onMessageCreate(nil, greetingMessage("user1", "moin"))

	reactions := getReactions()
	if len(reactions) != 2 {
		t.Fatalf("expected 2 special-day reactions, got %d", len(reactions))
	}
	if !isSpecialGreetingEmoji(reactions[0].emoji) {
		t.Errorf("first special-day reaction = %q, want one of util.Ae", reactions[0].emoji)
	}
	if reactions[1].emoji != string(util.Cl) {
		t.Errorf("second special-day reaction = %q, want %q", reactions[1].emoji, string(util.Cl))
	}
	if count := countGreetings(t, "user1"); count != 1 {
		t.Errorf("expected 1 greeting row, got %d", count)
	}
}
