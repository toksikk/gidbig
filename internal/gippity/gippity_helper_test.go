package gippity

import (
	"strings"
	"testing"
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
