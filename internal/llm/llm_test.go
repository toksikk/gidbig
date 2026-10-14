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

// stubClient returns a Client whose completion call is replaced by fn.
func stubClient(fn func(ctx context.Context, systemPrompt, userPrompt string) (string, error)) *Client {
	c := &Client{}
	c.generateFn = fn
	return c
}

func TestDetectChannelLanguage_EmptyMessages(t *testing.T) {
	called := false
	c := stubClient(func(_ context.Context, _, _ string) (string, error) {
		called = true
		return "German", nil
	})

	lang, err := c.detectLanguageFromTexts("")
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
	c := stubClient(func(_ context.Context, _, _ string) (string, error) {
		return "", errors.New("api error")
	})

	lang, err := c.detectLanguageFromTexts("Bonjour le monde")
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
			c := stubClient(func(_ context.Context, _, _ string) (string, error) {
				return tt.llmReply, nil
			})

			got, err := c.detectLanguageFromTexts("some text")
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
		{"known preset genalpha", "", "genalpha", PersonalityPresets["genalpha"]},
		{"unknown preset falls back to default", "", "nope", defaultPersonality},
		{"nothing set uses default", "", "", defaultPersonality},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolvePersonality(tt.custom, tt.preset); got != tt.want {
				t.Errorf("resolvePersonality(%q, %q) = %q, want %q", tt.custom, tt.preset, got, tt.want)
			}
		})
	}
}

func TestGenerateMessage_DelegatesToFn(t *testing.T) {
	c := stubClient(func(_ context.Context, sys, usr string) (string, error) {
		return sys + "|" + usr, nil
	})

	got, err := c.GenerateMessage(context.Background(), "sys", "usr")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "sys|usr" {
		t.Errorf("got %q, want sys|usr", got)
	}
}

func TestClient_NilSafe(t *testing.T) {
	var c *Client
	if c.Model() != "" || c.VisionModel() != "" || c.Personality() != "" {
		t.Error("nil client accessors should return empty strings")
	}
	if c.GetClient() != nil {
		t.Error("nil client GetClient should return nil")
	}
	if _, err := c.GenerateMessage(context.Background(), "sys", "usr"); err == nil {
		t.Error("nil client GenerateMessage should error")
	}
	if lang, err := c.DetectChannelLanguage(nil, "channel"); err != nil || lang != "English" {
		t.Errorf("nil client DetectChannelLanguage = %q, %v; want English, nil", lang, err)
	}
}

func TestNewValidation(t *testing.T) {
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
			_, err := New(Options{Provider: tt.provider, Model: tt.model, BaseURL: tt.baseURL})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("New() error = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestNewRequiresProviderCredential(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "")
	_, err := New(Options{Provider: "openrouter", Model: "test/model"})
	if err == nil || !strings.Contains(err.Error(), "OPENROUTER_API_KEY") {
		t.Fatalf("New() error = %v, want OPENROUTER_API_KEY", err)
	}
}

func TestNewAPIKeyPrecedence(t *testing.T) {
	tests := []struct {
		name       string
		envKey     string
		configKey  string
		wantHeader string
	}{
		{name: "environment wins over config", envKey: "env-secret", configKey: "config-secret", wantHeader: "Bearer env-secret"},
		{name: "config used when environment unset", envKey: "", configKey: "config-secret", wantHeader: "Bearer config-secret"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("OPENAI_API_KEY", tt.envKey)

			var gotAuth string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotAuth = r.Header.Get("Authorization")
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"id":"test","choices":[{"message":{"role":"assistant","content":"hello"}}]}`))
			}))
			defer server.Close()

			c, err := New(Options{Provider: "openai", Model: "test-model", BaseURL: server.URL, APIKey: tt.configKey})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if _, err := c.GenerateMessage(context.Background(), "system", "user"); err != nil {
				t.Fatalf("GenerateMessage: %v", err)
			}
			if gotAuth != tt.wantHeader {
				t.Errorf("Authorization = %q, want %q", gotAuth, tt.wantHeader)
			}
		})
	}
}

func TestNewMissingCredentialMentionsBothSources(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	_, err := New(Options{Provider: "openai", Model: "test-model"})
	if err == nil {
		t.Fatal("New() error = nil, want missing credential error")
	}
	if !strings.Contains(err.Error(), "OPENAI_API_KEY") || !strings.Contains(err.Error(), "llm.api_key") {
		t.Fatalf("New() error = %v, want mention of OPENAI_API_KEY and llm.api_key", err)
	}
}

func TestNewConfiguresOpenRouterRequest(t *testing.T) {
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

	c, err := New(Options{
		Provider:    "openrouter",
		Model:       "openrouter/text-model",
		VisionModel: "openrouter/vision-model",
		BaseURL:     server.URL,
		HTTPReferer: "https://gidbig.example",
		Title:       "Gidbig Tests",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got, err := c.GenerateMessage(context.Background(), "system", "user")
	if err != nil {
		t.Fatalf("GenerateMessage: %v", err)
	}
	if got != "hello" {
		t.Errorf("GenerateMessage = %q, want hello", got)
	}
	if requestBody.Model != "openrouter/text-model" {
		t.Errorf("request model = %q", requestBody.Model)
	}
	if c.VisionModel() != "openrouter/vision-model" {
		t.Errorf("VisionModel() = %q", c.VisionModel())
	}
}

func TestNewOpenAIDefaultModels(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "openai-key")
	c, err := New(Options{BaseURL: "https://api.example/v1"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.Model() != openai.ChatModelGPT4oMini || c.VisionModel() != openai.ChatModelGPT4oMini {
		t.Errorf("default models = %q/%q", c.Model(), c.VisionModel())
	}
	if c.Personality() != defaultPersonality {
		t.Error("default personality not applied")
	}
}

func TestCompletionContentRejectsEmptyChoices(t *testing.T) {
	for _, completion := range []*openai.ChatCompletion{nil, {}} {
		if _, err := CompletionContent(completion); err == nil {
			t.Error("CompletionContent() error = nil, want error")
		}
	}
}
