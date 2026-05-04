package coffee

import (
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
)

type capturedBrewMessage struct {
	channelID string
	content   string
}

func captureBrewMessages(t *testing.T) func() []capturedBrewMessage {
	t.Helper()
	previous := sendBrewMessage
	msgs := []capturedBrewMessage{}
	sendBrewMessage = func(_ *discordgo.Session, channelID, content string) {
		msgs = append(msgs, capturedBrewMessage{channelID: channelID, content: content})
	}
	t.Cleanup(func() { sendBrewMessage = previous })
	return func() []capturedBrewMessage { return msgs }
}

func resetBrewStates(t *testing.T) {
	t.Helper()
	brewMu.Lock()
	brewStates = map[string]*brewState{}
	brewMu.Unlock()
	t.Cleanup(func() {
		brewMu.Lock()
		brewStates = map[string]*brewState{}
		brewMu.Unlock()
	})
}

func setBrewReady(guildID, channelID string) {
	brewMu.Lock()
	defer brewMu.Unlock()
	key := brewStateKey(guildID, channelID)
	if st, ok := brewStates[key]; ok {
		st.isReady = true
	}
}

func TestStartBrew_NewBrew(t *testing.T) {
	resetBrewStates(t)
	useNow(t, time.Date(2026, 5, 4, 10, 0, 0, 0, time.UTC))

	alreadyBrewing, readyAt := startBrew(nil, "guild1", "channel1")

	if alreadyBrewing {
		t.Fatal("expected new brew, got alreadyBrewing=true")
	}
	expected := time.Date(2026, 5, 4, 10, 3, 0, 0, time.UTC)
	if !readyAt.Equal(expected) {
		t.Errorf("readyAt = %v, want %v", readyAt, expected)
	}

	brewMu.Lock()
	st := brewStates[brewStateKey("guild1", "channel1")]
	brewMu.Unlock()

	if st == nil {
		t.Fatal("expected brew state to be created")
	}
	if st.isReady {
		t.Error("expected brew to not be ready yet")
	}
	if st.coffeeLiters != potCapacity {
		t.Errorf("coffeeLiters = %v, want %v", st.coffeeLiters, potCapacity)
	}
}

func TestStartBrew_AlreadyBrewing(t *testing.T) {
	resetBrewStates(t)
	useNow(t, time.Date(2026, 5, 4, 10, 0, 0, 0, time.UTC))

	startBrew(nil, "guild1", "channel1")
	alreadyBrewing, readyAt := startBrew(nil, "guild1", "channel1")

	if !alreadyBrewing {
		t.Fatal("expected alreadyBrewing=true for second /brew")
	}
	expected := time.Date(2026, 5, 4, 10, 3, 0, 0, time.UTC)
	if !readyAt.Equal(expected) {
		t.Errorf("readyAt = %v, want %v", readyAt, expected)
	}
}

func TestStartBrew_AllowsNewBrewAfterReady(t *testing.T) {
	resetBrewStates(t)
	useNow(t, time.Date(2026, 5, 4, 10, 0, 0, 0, time.UTC))

	startBrew(nil, "guild1", "channel1")
	setBrewReady("guild1", "channel1")

	alreadyBrewing, _ := startBrew(nil, "guild1", "channel1")
	if alreadyBrewing {
		t.Error("expected to allow new brew after previous brew is ready")
	}
}

func TestHandleBrewMessage_BeforeReady(t *testing.T) {
	resetBrewStates(t)
	getReactions := captureReactions(t)
	getMsgs := captureBrewMessages(t)

	brewMu.Lock()
	brewStates["guild1:channel1"] = &brewState{
		readyAt:      time.Now().Add(brewDuration),
		isReady:      false,
		coffeeLiters: potCapacity,
	}
	brewMu.Unlock()

	handleBrewMessage(nil, "guild1", "channel1", "msg1", "user1")

	if len(getReactions()) != 0 {
		t.Error("expected no reactions before brew is ready")
	}
	if len(getMsgs()) != 0 {
		t.Error("expected no messages before brew is ready")
	}
}

func TestHandleBrewMessage_AfterReady(t *testing.T) {
	resetBrewStates(t)
	getReactions := captureReactions(t)
	getMsgs := captureBrewMessages(t)

	brewMu.Lock()
	brewStates["guild1:channel1"] = &brewState{
		readyAt:      time.Now().Add(-time.Second),
		isReady:      true,
		coffeeLiters: potCapacity,
	}
	brewMu.Unlock()

	handleBrewMessage(nil, "guild1", "channel1", "msg1", "user1")

	reactions := getReactions()
	if len(reactions) != 1 {
		t.Fatalf("expected 1 reaction, got %d", len(reactions))
	}
	if reactions[0].emoji != "☕" {
		t.Errorf("emoji = %q, want ☕", reactions[0].emoji)
	}
	if reactions[0].channelID != "channel1" {
		t.Errorf("channelID = %q, want channel1", reactions[0].channelID)
	}

	msgs := getMsgs()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 summary message, got %d", len(msgs))
	}
	want := "<@user1> took coffee. 0.75L remaining"
	if msgs[0].content != want {
		t.Errorf("summary = %q, want %q", msgs[0].content, want)
	}
}

func TestHandleBrewMessage_MultipleUsers(t *testing.T) {
	resetBrewStates(t)
	_ = captureReactions(t)
	getMsgs := captureBrewMessages(t)

	brewMu.Lock()
	brewStates["guild1:channel1"] = &brewState{
		readyAt:      time.Now().Add(-time.Second),
		isReady:      true,
		coffeeLiters: potCapacity,
	}
	brewMu.Unlock()

	handleBrewMessage(nil, "guild1", "channel1", "msg1", "alice")
	handleBrewMessage(nil, "guild1", "channel1", "msg2", "bob")

	msgs := getMsgs()
	if len(msgs) != 2 {
		t.Fatalf("expected 2 summary messages, got %d", len(msgs))
	}
	if !strings.Contains(msgs[1].content, "<@alice>") || !strings.Contains(msgs[1].content, "<@bob>") {
		t.Errorf("second summary should mention both users: %q", msgs[1].content)
	}
	if !strings.Contains(msgs[1].content, "0.50L remaining") {
		t.Errorf("second summary should show 0.50L remaining: %q", msgs[1].content)
	}
}

func TestHandleBrewMessage_DuplicateUser(t *testing.T) {
	resetBrewStates(t)
	getReactions := captureReactions(t)
	getMsgs := captureBrewMessages(t)

	brewMu.Lock()
	brewStates["guild1:channel1"] = &brewState{
		readyAt:      time.Now().Add(-time.Second),
		isReady:      true,
		coffeeLiters: potCapacity,
		takenBy:      []string{"user1"},
	}
	brewMu.Unlock()

	handleBrewMessage(nil, "guild1", "channel1", "msg2", "user1")

	if len(getReactions()) != 0 {
		t.Error("expected no reaction for duplicate user")
	}
	if len(getMsgs()) != 0 {
		t.Error("expected no summary message for duplicate user")
	}
}

func TestHandleBrewMessage_PotEmpty(t *testing.T) {
	resetBrewStates(t)
	getReactions := captureReactions(t)
	getMsgs := captureBrewMessages(t)

	brewMu.Lock()
	brewStates["guild1:channel1"] = &brewState{
		readyAt:      time.Now().Add(-time.Second),
		isReady:      true,
		coffeeLiters: 0.25,
		takenBy:      []string{"user1", "user2", "user3"},
	}
	brewMu.Unlock()

	handleBrewMessage(nil, "guild1", "channel1", "msg4", "user4")

	if len(getReactions()) != 1 {
		t.Fatalf("expected 1 reaction for last cup, got %d", len(getReactions()))
	}

	msgs := getMsgs()
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages (summary + empty), got %d", len(msgs))
	}
	if msgs[1].content != "The coffee pot is empty!" {
		t.Errorf("last message = %q, want 'The coffee pot is empty!'", msgs[1].content)
	}

	brewMu.Lock()
	_, exists := brewStates["guild1:channel1"]
	brewMu.Unlock()
	if exists {
		t.Error("expected brew state to be deleted after pot is empty")
	}
}

func TestHandleBrewMessage_EmptyPotNoReaction(t *testing.T) {
	resetBrewStates(t)
	getReactions := captureReactions(t)
	getMsgs := captureBrewMessages(t)

	// Pot already below threshold
	brewMu.Lock()
	brewStates["guild1:channel1"] = &brewState{
		readyAt:      time.Now().Add(-time.Second),
		isReady:      true,
		coffeeLiters: 0.0,
		takenBy:      []string{"user1", "user2", "user3", "user4"},
	}
	brewMu.Unlock()

	handleBrewMessage(nil, "guild1", "channel1", "msg5", "user5")

	if len(getReactions()) != 0 {
		t.Error("expected no reaction when pot is empty")
	}
	if len(getMsgs()) != 0 {
		t.Error("expected no message when pot is empty")
	}
}

func TestMarkBrewReady_SendsMessage(t *testing.T) {
	resetBrewStates(t)
	getMsgs := captureBrewMessages(t)

	brewMu.Lock()
	brewStates["guild1:channel1"] = &brewState{
		readyAt:      time.Now(),
		isReady:      false,
		coffeeLiters: potCapacity,
	}
	brewMu.Unlock()

	markBrewReady(nil, "guild1", "channel1")

	brewMu.Lock()
	st := brewStates["guild1:channel1"]
	brewMu.Unlock()

	if st == nil || !st.isReady {
		t.Error("expected brew to be marked ready")
	}

	msgs := getMsgs()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].content != "☕ Coffee is ready! Everyone grab a cup with `/coffee`!" {
		t.Errorf("message = %q", msgs[0].content)
	}
}

func TestMarkBrewReady_NoStateDoesNothing(t *testing.T) {
	resetBrewStates(t)
	getMsgs := captureBrewMessages(t)

	markBrewReady(nil, "guild1", "channel1")

	if len(getMsgs()) != 0 {
		t.Error("expected no message when no brew state exists")
	}
}

func TestHandleBrewMessage_NoBrew(t *testing.T) {
	resetBrewStates(t)
	getReactions := captureReactions(t)
	getMsgs := captureBrewMessages(t)

	handleBrewMessage(nil, "guild1", "channel1", "msg1", "user1")

	if len(getReactions()) != 0 {
		t.Error("expected no reaction with no active brew")
	}
	if len(getMsgs()) != 0 {
		t.Error("expected no message with no active brew")
	}
}

func TestBrewStateKey_WithGuild(t *testing.T) {
	key := brewStateKey("guild1", "channel1")
	if key != "guild1:channel1" {
		t.Errorf("key = %q, want guild1:channel1", key)
	}
}

func TestBrewStateKey_WithoutGuild(t *testing.T) {
	key := brewStateKey("", "channel1")
	if key != "dm:channel1" {
		t.Errorf("key = %q, want dm:channel1", key)
	}
}
