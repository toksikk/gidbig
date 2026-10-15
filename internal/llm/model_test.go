package llm

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func override(value string, ok bool) func() (string, bool) {
	return func() (string, bool) { return value, ok }
}

func TestModel_OverrideWins(t *testing.T) {
	c := &Client{textModel: "boot-model"}

	c.modelOverride = override("runtime-model", true)
	if got := c.Model(); got != "runtime-model" {
		t.Errorf("Model() = %q, want %q", got, "runtime-model")
	}

	// No override available: the boot-time model applies.
	c.modelOverride = override("", false)
	if got := c.Model(); got != "boot-model" {
		t.Errorf("Model() with no override = %q, want %q", got, "boot-model")
	}

	// A present but empty value must not disable the client's own fallback.
	c.modelOverride = override("", true)
	if got := c.Model(); got != "boot-model" {
		t.Errorf("Model() with an empty override = %q, want %q", got, "boot-model")
	}

	c.modelOverride = nil
	if got := c.Model(); got != "boot-model" {
		t.Errorf("Model() without a resolver = %q, want %q", got, "boot-model")
	}
}

func TestVisionModel_FollowsTextModelWhenNotExplicit(t *testing.T) {
	c := &Client{textModel: "boot-model", visionModel: "boot-model", visionExplicit: false}
	c.modelOverride = override("runtime-model", true)

	if got := c.VisionModel(); got != "runtime-model" {
		t.Errorf("VisionModel() = %q, want it to follow the text model %q", got, "runtime-model")
	}
}

func TestVisionModel_ExplicitBootValueIsKept(t *testing.T) {
	c := &Client{textModel: "boot-model", visionModel: "vision-boot", visionExplicit: true}
	c.modelOverride = override("runtime-model", true)

	if got := c.VisionModel(); got != "vision-boot" {
		t.Errorf("VisionModel() = %q, want the explicit %q", got, "vision-boot")
	}
}

func TestVisionModel_OverrideWinsOverExplicitBootValue(t *testing.T) {
	c := &Client{textModel: "boot-model", visionModel: "vision-boot", visionExplicit: true}
	c.visionModelOverride = override("vision-runtime", true)

	if got := c.VisionModel(); got != "vision-runtime" {
		t.Errorf("VisionModel() = %q, want %q", got, "vision-runtime")
	}
}

func TestInfo_ReflectsEffectiveModels(t *testing.T) {
	c := &Client{textModel: "boot-model", visionModel: "boot-model"}
	c.modelOverride = override("runtime-model", true)

	info := c.Info()
	if info.Model != "runtime-model" {
		t.Errorf("Info().Model = %q, want %q", info.Model, "runtime-model")
	}
	if info.VisionModel != "runtime-model" {
		t.Errorf("Info().VisionModel = %q, want %q", info.VisionModel, "runtime-model")
	}
}

func TestNew_WiresModelOverrides(t *testing.T) {
	c, err := New(Options{
		APIKey:        "test-key",
		Model:         "boot-model",
		ModelOverride: override("runtime-model", true),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := c.Model(); got != "runtime-model" {
		t.Errorf("Model() = %q, want %q", got, "runtime-model")
	}
	// llm.vision_model was not set, so the vision model follows the text model.
	if got := c.VisionModel(); got != "runtime-model" {
		t.Errorf("VisionModel() = %q, want %q", got, "runtime-model")
	}
}

// TestGenerateMessage_SendsTheEffectiveModel proves the override reaches the
// wire, not just the accessor.
func TestGenerateMessage_SendsTheEffectiveModel(t *testing.T) {
	var body string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer server.Close()

	c, err := New(Options{
		APIKey:        "test-key",
		Model:         "boot-model",
		BaseURL:       server.URL,
		ModelOverride: override("runtime-model", true),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, err := c.GenerateMessage(context.Background(), "system", "user"); err != nil {
		t.Fatalf("GenerateMessage: %v", err)
	}
	if !strings.Contains(body, "runtime-model") {
		t.Errorf("request body does not carry the effective model: %s", body)
	}
	if strings.Contains(body, "boot-model") {
		t.Errorf("request body still carries the boot-time model: %s", body)
	}
}

func TestMaxTokens_OverrideWins(t *testing.T) {
	c := &Client{maxTokens: 300}

	c.maxTokensOverride = func() (int, bool) { return 700, true }
	if got := c.MaxTokens(); got != 700 {
		t.Errorf("MaxTokens() = %d, want 700", got)
	}

	// No override available, or a non-positive one, keeps the boot value: a
	// blank row must never disable the completion cap.
	for _, tc := range []struct {
		name string
		fn   func() (int, bool)
	}{
		{"absent", func() (int, bool) { return 0, false }},
		{"zero", func() (int, bool) { return 0, true }},
		{"negative", func() (int, bool) { return -5, true }},
		{"nil", nil},
	} {
		c.maxTokensOverride = tc.fn
		if got := c.MaxTokens(); got != 300 {
			t.Errorf("%s: MaxTokens() = %d, want the boot 300", tc.name, got)
		}
	}
}

func TestNew_MaxTokensDefaultsAndOverride(t *testing.T) {
	c, err := New(Options{APIKey: "test-key"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := c.MaxTokens(); got != DefaultMaxTokens {
		t.Errorf("MaxTokens() = %d, want the default %d", got, DefaultMaxTokens)
	}
	if got := c.Info().MaxTokens; got != DefaultMaxTokens {
		t.Errorf("Info().MaxTokens = %d, want the default %d", got, DefaultMaxTokens)
	}

	c, err = New(Options{APIKey: "test-key", MaxTokens: -1})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := c.MaxTokens(); got != DefaultMaxTokens {
		t.Errorf("non-positive MaxTokens: got %d, want the default %d", got, DefaultMaxTokens)
	}

	c, err = New(Options{
		APIKey:            "test-key",
		MaxTokens:         300,
		MaxTokensOverride: func() (int, bool) { return 900, true },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := c.MaxTokens(); got != 900 {
		t.Errorf("MaxTokens() = %d, want the override 900", got)
	}
	if got := c.Info().MaxTokens; got != 900 {
		t.Errorf("Info().MaxTokens = %d, want the override 900", got)
	}
}

func TestGenerateMessage_SendsTheEffectiveMaxTokens(t *testing.T) {
	var body string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer server.Close()

	c, err := New(Options{
		APIKey:            "test-key",
		BaseURL:           server.URL,
		MaxTokens:         300,
		MaxTokensOverride: func() (int, bool) { return 640, true },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := c.GenerateMessage(context.Background(), "system", "user"); err != nil {
		t.Fatalf("GenerateMessage: %v", err)
	}
	if !strings.Contains(body, "640") {
		t.Errorf("request body does not carry the effective cap: %s", body)
	}
}

// TestVisionModel_EmptyOverrideFollowsTheTextModel covers the deliberate
// asymmetry with llm.model: a stored-but-empty vision value is authoritative and
// means "follow llm.model", even when config.yaml set an explicit vision model,
// so the override is never silently ignored.
func TestVisionModel_EmptyOverrideFollowsTheTextModel(t *testing.T) {
	c := &Client{
		textModel:      "boot-model",
		visionModel:    "boot-vision",
		visionExplicit: true,
		modelOverride:  override("runtime-model", true),
	}
	c.visionModelOverride = override("", true)

	if got := c.VisionModel(); got != "runtime-model" {
		t.Errorf("VisionModel() = %q, want the text model %q", got, "runtime-model")
	}

	// No row at all still keeps the explicit boot value.
	c.visionModelOverride = override("", false)
	if got := c.VisionModel(); got != "boot-vision" {
		t.Errorf("VisionModel() with no override = %q, want %q", got, "boot-vision")
	}
}

// TestMaxTokens_ZeroValueClientFloorsAtTheDefault guards the case the previous
// const made impossible: a Client that skipped New must not send
// max_completion_tokens: 0, which providers reject or treat as "no output".
func TestMaxTokens_ZeroValueClientFloorsAtTheDefault(t *testing.T) {
	if got := (&Client{}).MaxTokens(); got != DefaultMaxTokens {
		t.Errorf("MaxTokens() = %d, want the default %d", got, DefaultMaxTokens)
	}
	if got := (&Client{maxTokens: -1}).MaxTokens(); got != DefaultMaxTokens {
		t.Errorf("MaxTokens() with a negative boot value = %d, want the default %d", got, DefaultMaxTokens)
	}
}
