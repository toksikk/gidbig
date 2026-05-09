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

type capturedDM struct {
	userID string
	emoji  string
}

func captureIntroDMs(t *testing.T) func() []capturedDM {
	t.Helper()
	previous := sendIntroDM
	dms := []capturedDM{}
	sendIntroDM = func(_ *discordgo.Session, userID string, emoji string) {
		dms = append(dms, capturedDM{
			userID: userID,
			emoji:  emoji,
		})
	}
	t.Cleanup(func() {
		sendIntroDM = previous
	})
	return func() []capturedDM {
		return dms
	}
}

func TestOnMessageCreate_TriggersIntroDMOnFirstGreeting(t *testing.T) {
	openInMemoryStore(t)
	useNow(t, time.Date(2026, 5, 3, 9, 0, 0, 0, time.Local))
	useSpecialDay(t, false)
	_ = captureReactions(t)
	getDMs := captureIntroDMs(t)

	onMessageCreate(nil, greetingMessage("user_new", "moin"))

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

	if !isUserIntroduced("user_new") {
		t.Error("expected user_new to be marked as introduced")
	}
}

func TestOnMessageCreate_DoesNotTriggerIntroDMIfAlreadyIntroduced(t *testing.T) {
	openInMemoryStore(t)
	useNow(t, time.Date(2026, 5, 3, 9, 0, 0, 0, time.Local))
	useSpecialDay(t, false)
	_ = captureReactions(t)
	getDMs := captureIntroDMs(t)

	_ = setBeverageEmoji("user_old", "🧃") // sets introduced=true

	onMessageCreate(nil, greetingMessage("user_old", "moin"))

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
	_ = captureIntroDMs(t)

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
	_ = captureIntroDMs(t)

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
	_ = captureIntroDMs(t)

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
	_ = captureIntroDMs(t)

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

func stubLLM(t *testing.T, reply string, callErr error) func() []string {
	t.Helper()
	prevDetect := detectLanguage
	prevGenerate := generateLLMMessage
	t.Cleanup(func() {
		detectLanguage = prevDetect
		generateLLMMessage = prevGenerate
	})
	detectLanguage = func(_ *discordgo.Session, _ string) (string, error) {
		return "English", nil
	}
	calls := []string{}
	generateLLMMessage = func(_ context.Context, _, userPrompt string) (string, error) {
		calls = append(calls, userPrompt)
		return reply, callErr
	}
	return func() []string { return calls }
}

func TestGenerateInteractionMessage_ReturnsLLMText(t *testing.T) {
	getCalls := stubLLM(t, "Der Kaffee ist fertig.", nil)
	got := generateInteractionMessage(nil, "ch1", "Coffee is ready.", "fallback")
	// nil session → detectLanguage returns "English" (stubbed), generateLLMMessage still called
	if got != "Der Kaffee ist fertig." {
		t.Errorf("got %q, want LLM reply", got)
	}
	if len(getCalls()) != 1 {
		t.Errorf("expected 1 LLM call, got %d", len(getCalls()))
	}
}

func TestGenerateInteractionMessage_FallsBackOnError(t *testing.T) {
	stubLLM(t, "", fmt.Errorf("api failure"))
	got := generateInteractionMessage(nil, "ch1", "scenario", "my fallback")
	if got != "my fallback" {
		t.Errorf("got %q, want fallback", got)
	}
}

func TestGenerateInteractionMessage_FallsBackOnEmptyReply(t *testing.T) {
	stubLLM(t, "   ", nil)
	got := generateInteractionMessage(nil, "ch1", "scenario", "my fallback")
	if got != "my fallback" {
		t.Errorf("got %q, want fallback on empty LLM reply", got)
	}
}

type deferCall struct {
	ephemeral bool
}

type editCall struct {
	content string
}

func stubInteractionHelpers(t *testing.T, deferErr error) (func() []deferCall, func() []editCall) {
	t.Helper()

	prevDefer := deferInteraction
	prevEdit := editDeferredResponse
	defers := []deferCall{}
	edits := []editCall{}

	deferInteraction = func(_ *discordgo.Session, _ *discordgo.InteractionCreate, ephemeral bool) error {
		defers = append(defers, deferCall{ephemeral: ephemeral})
		return deferErr
	}
	editDeferredResponse = func(_ *discordgo.Session, _ *discordgo.InteractionCreate, content string) {
		edits = append(edits, editCall{content: content})
	}

	t.Cleanup(func() {
		deferInteraction = prevDefer
		editDeferredResponse = prevEdit
	})

	return func() []deferCall { return defers }, func() []editCall { return edits }
}

func makeBrewInteraction(guildID, channelID string) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			GuildID:   guildID,
			ChannelID: channelID,
			Type:      discordgo.InteractionApplicationCommand,
			Data: discordgo.ApplicationCommandInteractionData{
				Name: "brew",
			},
		},
	}
}

func makeComponentInteraction(guildID, channelID, customID, userID string) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			GuildID:   guildID,
			ChannelID: channelID,
			Type:      discordgo.InteractionMessageComponent,
			Member:    &discordgo.Member{User: &discordgo.User{ID: userID}},
			Data:      discordgo.MessageComponentInteractionData{CustomID: customID},
		},
	}
}

func TestHandleBrewInteraction_DefersEphemeralWhenAlreadyBrewing(t *testing.T) {
	openInMemoryStore(t)
	resetBrewStates(t)
	getDefers, getEdits := stubInteractionHelpers(t, nil)
	stubLLM(t, "Already brewing!", nil)
	captureBrewReadyMessages(t)

	i := makeBrewInteraction("g1", "ch1")
	handleBrewInteraction(nil, i)
	handleBrewInteraction(nil, i)

	defers := getDefers()
	edits := getEdits()
	if len(defers) < 2 {
		t.Fatalf("expected at least 2 defer calls, got %d", len(defers))
	}
	if !defers[1].ephemeral {
		t.Error("second /brew (already brewing) should defer as ephemeral")
	}
	if len(edits) < 2 {
		t.Fatalf("expected at least 2 edit calls, got %d", len(edits))
	}
}

func TestHandleBrewInteraction_DefersPublicOnNewBrew(t *testing.T) {
	openInMemoryStore(t)
	resetBrewStates(t)
	getDefers, getEdits := stubInteractionHelpers(t, nil)
	stubLLM(t, "Coffee brewing!", nil)
	captureBrewReadyMessages(t)

	handleBrewInteraction(nil, makeBrewInteraction("g2", "ch2"))

	defers := getDefers()
	if len(defers) != 1 {
		t.Fatalf("expected 1 defer call, got %d", len(defers))
	}
	if defers[0].ephemeral {
		t.Error("new brew should defer as public (not ephemeral)")
	}
	if len(getEdits()) != 1 {
		t.Errorf("expected 1 edit call, got %d", len(getEdits()))
	}
}

func TestHandleBrewInteraction_AbortsOnDeferError(t *testing.T) {
	openInMemoryStore(t)
	resetBrewStates(t)
	_, getEdits := stubInteractionHelpers(t, fmt.Errorf("discord unavailable"))
	stubLLM(t, "Coffee!", nil)
	captureBrewReadyMessages(t)

	handleBrewInteraction(nil, makeBrewInteraction("g3", "ch3"))

	if len(getEdits()) != 0 {
		t.Error("edit should not be called when defer fails")
	}
}

func TestHandleGrabCoffeeButton_DefersEphemeralWhenNotReady(t *testing.T) {
	openInMemoryStore(t)
	resetBrewStates(t)
	getDefers, getEdits := stubInteractionHelpers(t, nil)
	stubLLM(t, "Pot is empty!", nil)

	handleGrabCoffeeButton(nil, makeComponentInteraction("g4", "ch4", "grab_coffee", "user1"))

	defers := getDefers()
	if len(defers) != 1 {
		t.Fatalf("expected 1 defer call, got %d", len(defers))
	}
	if !defers[0].ephemeral {
		t.Error("grab when not ready should defer as ephemeral")
	}
	if len(getEdits()) != 1 {
		t.Errorf("expected 1 edit call, got %d", len(getEdits()))
	}
}
