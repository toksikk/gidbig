package gippity

import (
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
)

func TestEnrichSystemMessage_returnsInputUnchanged(t *testing.T) {
	cases := []string{
		"",
		"hello world",
		"Du bist ein Discord Chatbot.",
		"multi\nline\nmessage",
	}
	for _, input := range cases {
		got := enrichSystemMessage(input)
		if got != input {
			t.Errorf("enrichSystemMessage(%q) = %q, want %q", input, got, input)
		}
	}
}

func TestRemoveSpoilerTagContent(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"no spoilers here", "no spoilers here"},
		{"||secret||", "||Spoiler||"},
		{"before ||hidden|| after", "before ||Spoiler|| after"},
		{"||first|| and ||second||", "||Spoiler|| and ||Spoiler||"},
		{"||||", "||||"},
	}
	for _, tc := range cases {
		msg := &LLMChatMessage{Message: tc.input}
		removeSpoilerTagContent(msg)
		if msg.Message != tc.want {
			t.Errorf("removeSpoilerTagContent(%q) = %q, want %q", tc.input, msg.Message, tc.want)
		}
	}
}

func TestRemoveSpoilerTagContentInStringMessage(t *testing.T) {
	got := removeSpoilerTagContentInStringMessage("tell me ||the answer||")
	if got != "tell me ||Spoiler||" {
		t.Errorf("unexpected result: %q", got)
	}
}

func TestReplaceAllUserIDsWithUsernamesInStringMessage_NoMentions(t *testing.T) {
	input := "Hello world"
	got := replaceAllUserIDsWithUsernamesInStringMessage(input, "guild123")
	if got != input {
		t.Errorf("replaceAllUserIDsWithUsernamesInStringMessage(%q) = %q, want unchanged", input, got)
	}
}

func TestConvertLLMChatMessageToLLMCompatibleFlowingText(t *testing.T) {
	msg := LLMChatMessage{
		TimestampString: "2026-05-01 12:00:00",
		Username:        "Alice",
		Message:         "Hello there",
	}
	result := convertLLMChatMessageToLLMCompatibleFlowingText(msg)
	if !strings.Contains(result, "2026-05-01 12:00:00") {
		t.Errorf("result missing timestamp: %q", result)
	}
	if !strings.Contains(result, "Alice") {
		t.Errorf("result missing username: %q", result)
	}
	if !strings.Contains(result, "Hello there") {
		t.Errorf("result missing message: %q", result)
	}
}

func TestFetchReferencedMessage_FoundInDB(t *testing.T) {
	setupGippityTest(t)
	idToNameCache["user-2"] = "Bob"
	idToNameCache["channel-1"] = "general"
	idToNameCache["allowed-guild"] = "Test Guild"

	if _, err := database.Exec(`INSERT INTO chat_history (user_id, channel_id, timestamp, message, message_id, guild_id, is_bot_mention) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"user-2", "channel-1", 1000, "the referenced content", "ref-db-msg", "allowed-guild", 0); err != nil {
		t.Fatalf("insert: %v", err)
	}

	ref := &discordgo.MessageReference{
		MessageID: "ref-db-msg",
		ChannelID: "channel-1",
		GuildID:   "allowed-guild",
	}
	msg, err := fetchReferencedMessage(discordSession, ref)
	if err != nil {
		t.Fatalf("fetchReferencedMessage: %v", err)
	}
	if msg == nil {
		t.Fatal("expected non-nil message")
	}
	if msg.Content != "the referenced content" {
		t.Errorf("Content = %q, want %q", msg.Content, "the referenced content")
	}
	if msg.Author == nil || msg.Author.ID != "user-2" {
		t.Errorf("Author.ID = %q, want %q", msg.Author.ID, "user-2")
	}
}

func TestFetchReferencedMessage_NotInDB_FallsBackToAPI(t *testing.T) {
	setupGippityTest(t)

	apiCalled := false
	prevCMF := channelMessageFunc
	t.Cleanup(func() { channelMessageFunc = prevCMF })
	channelMessageFunc = func(_ *discordgo.Session, _, msgID string) (*discordgo.Message, error) {
		apiCalled = true
		return &discordgo.Message{
			ID:      msgID,
			Content: "fetched from api",
			Author:  &discordgo.User{ID: "user-api", Username: "ApiUser"},
		}, nil
	}

	ref := &discordgo.MessageReference{
		MessageID: "api-msg-id",
		ChannelID: "channel-1",
		GuildID:   "allowed-guild",
	}
	// "api-msg-id" is not in the DB — fetchReferencedMessage must fall back to channelMessageFunc.
	msg, err := fetchReferencedMessage(discordSession, ref)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !apiCalled {
		t.Error("expected channelMessageFunc to be called as fallback")
	}
	if msg == nil || msg.Content != "fetched from api" {
		t.Errorf("unexpected message: %v", msg)
	}
}

func TestConvertLLMChatMessageToLLMCompatibleFlowingText_WithImageDescriptions(t *testing.T) {
	msg := LLMChatMessage{
		TimestampString:   "2026-05-01 12:00:00",
		Username:          "Alice",
		Message:           "look at this",
		ImageDescriptions: []string{"a cat sitting on a mat", "a blue sky"},
	}
	result := convertLLMChatMessageToLLMCompatibleFlowingText(msg)
	if !strings.Contains(result, "[Image 1: a cat sitting on a mat]") {
		t.Errorf("result missing image 1 description: %q", result)
	}
	if !strings.Contains(result, "[Image 2: a blue sky]") {
		t.Errorf("result missing image 2 description: %q", result)
	}
}
