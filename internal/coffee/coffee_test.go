package coffee

import (
	"context"
	"fmt"
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

func captureReactions(m *Module, t *testing.T) func() []capturedReaction {
	t.Helper()
	previous := m.reactOnMessage
	reactions := []capturedReaction{}
	m.reactOnMessage = func(_ *discordgo.Session, channelID, messageID, emoji, reactionType string) {
		reactions = append(reactions, capturedReaction{
			channelID:    channelID,
			messageID:    messageID,
			emoji:        emoji,
			reactionType: reactionType,
		})
	}
	t.Cleanup(func() {
		m.reactOnMessage = previous
	})
	return func() []capturedReaction {
		return reactions
	}
}

func useSpecialDay(m *Module, t *testing.T, special bool) {
	t.Helper()
	previous := m.isSpecialDay
	m.isSpecialDay = func() bool {
		return special
	}
	t.Cleanup(func() {
		m.isSpecialDay = previous
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

func countGreetings(m *Module, t *testing.T, userID string) int64 {
	t.Helper()
	d := m.getDB()
	var count int64
	if err := d.Model(&UserGreeting{}).Where("user_id = ?", userID).Count(&count).Error; err != nil {
		t.Fatalf("failed to count greetings: %v", err)
	}
	return count
}

type capturedDM struct {
	userID string
	emoji  string
}

func captureIntroDMs(m *Module, t *testing.T) func() []capturedDM {
	t.Helper()
	previous := m.sendIntroDM
	dms := []capturedDM{}
	m.sendIntroDM = func(_ *discordgo.Session, userID string, emoji string) {
		dms = append(dms, capturedDM{
			userID: userID,
			emoji:  emoji,
		})
	}
	t.Cleanup(func() {
		m.sendIntroDM = previous
	})
	return func() []capturedDM {
		return dms
	}
}

func TestOnMessageCreate_TriggersIntroDMOnFirstGreeting(t *testing.T) {
	m := newTestModule(t)
	useNow(m, t, time.Date(2026, 5, 3, 9, 0, 0, 0, time.Local))
	useSpecialDay(m, t, false)
	_ = captureReactions(m, t)
	getDMs := captureIntroDMs(m, t)

	m.onMessageCreate(nil, greetingMessage("user_new", "moin"))

	dms := getDMs()
	if len(dms) != 1 {
		t.Fatalf("expected 1 intro DM, got %d", len(dms))
	}
	if dms[0].userID != "user_new" {
		t.Errorf("DM userID = %q, want user_new", dms[0].userID)
	}
	if dms[0].emoji != fallbackBeverage {
		t.Errorf("DM emoji = %q, want %q", dms[0].emoji, fallbackBeverage)
	}

	if !m.isUserIntroduced("user_new") {
		t.Error("expected user_new to be marked as introduced")
	}
}

func TestOnMessageCreate_DoesNotTriggerIntroDMIfAlreadyIntroduced(t *testing.T) {
	m := newTestModule(t)
	useNow(m, t, time.Date(2026, 5, 3, 9, 0, 0, 0, time.Local))
	useSpecialDay(m, t, false)
	_ = captureReactions(m, t)
	getDMs := captureIntroDMs(m, t)

	_ = m.setBeverageEmoji("user_old", "🧃")

	m.onMessageCreate(nil, greetingMessage("user_old", "moin"))

	dms := getDMs()
	if len(dms) != 0 {
		t.Fatalf("expected 0 intro DMs for introduced user, got %d", len(dms))
	}
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
		{"🫖", true},
		{"☕", true},
		{"🍺", true},
		{"🧃", true},
		{"<:customemoji:123456789>", true},
		{"<a:animatedemoji:987654321>", true},
		{"hello", false},
		{"hello world", false},
		{"", false},
		{"42", false},
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
	m := newTestModule(t)
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
		got := m.beverageEmojiFor(tt.userID)
		if got != tt.expected {
			t.Errorf("beverageEmojiFor(%q) = %q; want %q", tt.userID, got, tt.expected)
		}
	}
}

func TestOnMessageCreate_FirstGreetingReactsAndRecordsGreeting(t *testing.T) {
	m := newTestModule(t)
	useNow(m, t, time.Date(2026, 5, 3, 9, 0, 0, 0, time.Local))
	useSpecialDay(m, t, false)
	getReactions := captureReactions(m, t)
	_ = captureIntroDMs(m, t)

	m.onMessageCreate(nil, greetingMessage("user1", "moin"))

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
	if count := countGreetings(m, t, "user1"); count != 1 {
		t.Errorf("expected 1 greeting row, got %d", count)
	}
}

func TestOnMessageCreate_DuplicateSameDayGreetingIsSuppressed(t *testing.T) {
	m := newTestModule(t)
	useNow(m, t, time.Date(2026, 5, 3, 9, 0, 0, 0, time.Local))
	useSpecialDay(m, t, false)
	getReactions := captureReactions(m, t)
	_ = captureIntroDMs(m, t)

	m.onMessageCreate(nil, greetingMessage("user1", "moin"))
	m.onMessageCreate(nil, greetingMessage("user1", "hi"))

	reactions := getReactions()
	if len(reactions) != 1 {
		t.Fatalf("expected only the first greeting to react, got %d reactions", len(reactions))
	}
	if count := countGreetings(m, t, "user1"); count != 1 {
		t.Errorf("expected duplicate greeting to keep 1 row, got %d", count)
	}
}

func TestOnMessageCreate_PriorDayGreetingAllowsNextDayReaction(t *testing.T) {
	m := newTestModule(t)
	useNow(m, t, time.Date(2026, 5, 3, 9, 0, 0, 0, time.Local))
	useSpecialDay(m, t, false)
	getReactions := captureReactions(m, t)
	_ = captureIntroDMs(m, t)

	d := m.getDB()
	if err := d.Create(&UserGreeting{
		UserID:    "user1",
		GreetedAt: time.Date(2026, 5, 2, 23, 0, 0, 0, time.Local),
	}).Error; err != nil {
		t.Fatalf("failed to create prior greeting: %v", err)
	}

	m.onMessageCreate(nil, greetingMessage("user1", "moin"))

	reactions := getReactions()
	if len(reactions) != 1 {
		t.Fatalf("expected next-day greeting to react once, got %d reactions", len(reactions))
	}
	if count := countGreetings(m, t, "user1"); count != 2 {
		t.Errorf("expected prior and new greeting rows, got %d", count)
	}
}

func TestOnMessageCreate_SpecialDayFirstGreetingReactsAndRecordsGreeting(t *testing.T) {
	m := newTestModule(t)
	useNow(m, t, time.Date(2026, 5, 3, 9, 0, 0, 0, time.Local))
	useSpecialDay(m, t, true)
	getReactions := captureReactions(m, t)
	_ = captureIntroDMs(m, t)

	m.onMessageCreate(nil, greetingMessage("user1", "moin"))

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
	if count := countGreetings(m, t, "user1"); count != 1 {
		t.Errorf("expected 1 greeting row, got %d", count)
	}
}

func stubLLM(m *Module, t *testing.T, reply string, callErr error) func() []string {
	t.Helper()
	prevDetect := m.detectLanguage
	prevGenerate := m.generateLLMMessage
	t.Cleanup(func() {
		m.detectLanguage = prevDetect
		m.generateLLMMessage = prevGenerate
	})
	m.detectLanguage = func(_ *discordgo.Session, _ string) (string, error) {
		return "English", nil
	}
	calls := []string{}
	m.generateLLMMessage = func(_ context.Context, _, userPrompt string) (string, error) {
		calls = append(calls, userPrompt)
		return reply, callErr
	}
	return func() []string { return calls }
}

func TestGenerateInteractionMessage_ReturnsLLMText(t *testing.T) {
	m := newTestModule(t)
	getCalls := stubLLM(m, t, "Der Kaffee ist fertig.", nil)
	got := m.generateInteractionMessage(nil, "ch1", "Coffee is ready.", "fallback")
	if got != "Der Kaffee ist fertig." {
		t.Errorf("got %q, want LLM reply", got)
	}
	if len(getCalls()) != 1 {
		t.Errorf("expected 1 LLM call, got %d", len(getCalls()))
	}
}

func TestGenerateInteractionMessage_FallsBackOnError(t *testing.T) {
	m := newTestModule(t)
	stubLLM(m, t, "", fmt.Errorf("api failure"))
	got := m.generateInteractionMessage(nil, "ch1", "scenario", "my fallback")
	if got != "my fallback" {
		t.Errorf("got %q, want fallback", got)
	}
}

func TestGenerateInteractionMessage_FallsBackOnEmptyReply(t *testing.T) {
	m := newTestModule(t)
	stubLLM(m, t, "   ", nil)
	got := m.generateInteractionMessage(nil, "ch1", "scenario", "my fallback")
	if got != "my fallback" {
		t.Errorf("got %q, want fallback on empty LLM reply", got)
	}
}

func TestGenerateInteractionMessage_UsedBySetbeverage(t *testing.T) {
	m := newTestModule(t)
	stubLLM(m, t, "Set!", nil)
	got := m.generateInteractionMessage(nil, "ch1", "Confirm beverage.", "fallback")
	if got != "Set!" {
		t.Errorf("got %q, want LLM reply", got)
	}
}
