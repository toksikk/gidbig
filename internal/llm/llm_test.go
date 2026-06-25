package llm

import (
	"context"
	"errors"
	"testing"
)

func TestDetectChannelLanguage_EmptyMessages(t *testing.T) {
	prev := generateMessageFn
	t.Cleanup(func() { generateMessageFn = prev })
	called := false
	generateMessageFn = func(_ context.Context, _, _ string) (string, error) {
		called = true
		return "German", nil
	}

	lang, err := detectLanguageFromTexts("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if lang != "English" {
		t.Errorf("empty text: got %q, want English", lang)
	}
	if called {
		t.Error("LLM should not be called when text is empty")
	}
}

func TestDetectChannelLanguage_LLMError_FallsBackToEnglish(t *testing.T) {
	prev := generateMessageFn
	t.Cleanup(func() { generateMessageFn = prev })
	generateMessageFn = func(_ context.Context, _, _ string) (string, error) {
		return "", errors.New("api error")
	}

	lang, err := detectLanguageFromTexts("Bonjour le monde")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if lang != "English" {
		t.Errorf("on LLM error: got %q, want English", lang)
	}
}

func TestDetectChannelLanguage_ReturnsDetectedLanguage(t *testing.T) {
	tests := []struct {
		name     string
		llmReply string
		want     string
	}{
		{"german", "German", "German"},
		{"french", "French", "French"},
		{"whitespace trimmed", "  Spanish  ", "Spanish"},
		{"empty reply falls back", "", "English"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prev := generateMessageFn
			t.Cleanup(func() { generateMessageFn = prev })
			generateMessageFn = func(_ context.Context, _, _ string) (string, error) {
				return tt.llmReply, nil
			}

			got, err := detectLanguageFromTexts("some text")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolvePersonality(t *testing.T) {
	t.Cleanup(func() { activePersonality = defaultPersonality })

	tests := []struct {
		name   string
		custom string
		preset string
		want   string
	}{
		{"custom wins over preset", "be a pirate", "hal", "be a pirate"},
		{"custom wins over default", "be a pirate", "", "be a pirate"},
		{"whitespace custom is ignored", "   ", "dry", PersonalityPresets["dry"]},
		{"known preset hal", "", "hal", PersonalityPresets["hal"]},
		{"known preset schemer", "", "schemer", PersonalityPresets["schemer"]},
		{"unknown preset falls back to default", "", "nope", defaultPersonality},
		{"nothing set uses default", "", "", defaultPersonality},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			activePersonality = "sentinel"
			ResolvePersonality(tt.custom, tt.preset)
			if got := Personality(); got != tt.want {
				t.Errorf("Personality() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGenerateMessage_DelegatesToFn(t *testing.T) {
	prev := generateMessageFn
	t.Cleanup(func() { generateMessageFn = prev })
	generateMessageFn = func(_ context.Context, sys, usr string) (string, error) {
		return sys + "|" + usr, nil
	}

	got, err := GenerateMessage(context.Background(), "sys", "usr")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "sys|usr" {
		t.Errorf("got %q, want sys|usr", got)
	}
}

