package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	openai "github.com/openai/openai-go/v3"
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

func TestInitializeValidation(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "openai-key")
	t.Setenv("OPENROUTER_API_KEY", "openrouter-key")

	tests := []struct {
		name     string
		provider string
		model    string
		baseURL  string
		want     string
	}{
		{name: "unsupported provider", provider: "other", want: "unsupported llm.provider"},
		{name: "OpenRouter model required", provider: "openrouter", want: "llm.model is required"},
		{name: "invalid base URL", provider: "openai", baseURL: "://bad", want: "llm.base_url"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Initialize(tt.provider, tt.model, "", tt.baseURL, "", "")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Initialize() error = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestInitializeRequiresProviderCredential(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "")
	err := Initialize("openrouter", "test/model", "", "", "", "")
	if err == nil || !strings.Contains(err.Error(), "OPENROUTER_API_KEY") {
		t.Fatalf("Initialize() error = %v, want OPENROUTER_API_KEY", err)
	}
}

func TestInitializeConfiguresOpenRouterRequest(t *testing.T) {
	previousGenerateMessageFn := generateMessageFn
	previousClient := client
	previousTextModel := textModel
	previousVisionModel := visionModel
	t.Cleanup(func() {
		generateMessageFn = previousGenerateMessageFn
		client = previousClient
		textModel = previousTextModel
		visionModel = previousVisionModel
	})
	t.Setenv("OPENROUTER_API_KEY", "router-secret")

	var requestBody struct {
		Model string `json:"model"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("request path = %q, want /chat/completions", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer router-secret" {
			t.Errorf("Authorization = %q", got)
		}
		if got := r.Header.Get("HTTP-Referer"); got != "https://gidbig.example" {
			t.Errorf("HTTP-Referer = %q", got)
		}
		if got := r.Header.Get("X-Title"); got != "Gidbig Tests" {
			t.Errorf("X-Title = %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Errorf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"test","choices":[{"message":{"role":"assistant","content":"hello"}}]}`))
	}))
	defer server.Close()

	if err := Initialize("openrouter", "openrouter/text-model", "openrouter/vision-model", server.URL, "https://gidbig.example", "Gidbig Tests"); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	got, err := GenerateMessage(context.Background(), "system", "user")
	if err != nil {
		t.Fatalf("GenerateMessage: %v", err)
	}
	if got != "hello" {
		t.Errorf("GenerateMessage = %q, want hello", got)
	}
	if requestBody.Model != "openrouter/text-model" {
		t.Errorf("request model = %q", requestBody.Model)
	}
	if VisionModel() != "openrouter/vision-model" {
		t.Errorf("VisionModel() = %q", VisionModel())
	}
}

func TestInitializeOpenAIDefaultModels(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "openai-key")
	if err := Initialize("", "   ", "", "https://api.example/v1", "", ""); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if Model() != openai.ChatModelGPT4oMini || VisionModel() != openai.ChatModelGPT4oMini {
		t.Errorf("default models = %q/%q", Model(), VisionModel())
	}
}

func TestCompletionContentRejectsEmptyChoices(t *testing.T) {
	for _, completion := range []*openai.ChatCompletion{nil, {}} {
		if _, err := CompletionContent(completion); err == nil {
			t.Error("CompletionContent() error = nil, want error")
		}
	}
}
