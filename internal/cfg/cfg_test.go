package cfg

import (
	"bytes"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/toksikk/gidbig/internal/llm"
)

func TestDecodeConfig_validConfig(t *testing.T) {
	yaml := `
discord:
  token: "test-token"
  owner_id: "123"
gippity:
  allowed_guilds: ["456"]
dev_mode: true
`
	cfg, err := decodeConfig(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Discord.Token != "test-token" {
		t.Errorf("token = %q, want %q", cfg.Discord.Token, "test-token")
	}
	if cfg.Discord.OwnerID != "123" {
		t.Errorf("owner_id = %q, want %q", cfg.Discord.OwnerID, "123")
	}
	if cfg.Gippity.AllowedGuilds[0] != "456" {
		t.Errorf("allowed_guilds[0] = %q, want %q", cfg.Gippity.AllowedGuilds[0], "456")
	}
	if !cfg.DevMode {
		t.Error("dev_mode should be true")
	}
}

func TestDecodeConfig_gippityReplyInThread(t *testing.T) {
	yaml := `
discord:
  token: "tok"
gippity:
  allowed_guilds: ["456"]
  reply_in_thread: true
`
	cfg, err := decodeConfig(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.Gippity.ReplyInThread {
		t.Error("gippity.reply_in_thread should be true")
	}

	omitted := `
discord:
  token: "tok"
gippity:
  allowed_guilds: ["456"]
`
	cfg, err = decodeConfig(strings.NewReader(omitted))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Gippity.ReplyInThread {
		t.Error("gippity.reply_in_thread should default to false")
	}
}

func TestDecodeConfig_gippityNonMentionFields(t *testing.T) {
	yaml := `
discord:
  token: "tok"
gippity:
  allowed_guilds: ["456"]
  respond_to_all_messages: true
  classifier_model: "gpt-4o-mini"
  classifier_rate_limit_per_hour: 5
  classifier_names: ["gustaff", "Gippity"]
  debug_channel: "dev-chan"
`
	cfg, err := decodeConfig(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.Gippity.RespondToAllMessages {
		t.Error("gippity.respond_to_all_messages should be true")
	}
	if cfg.Gippity.ClassifierModel != "gpt-4o-mini" {
		t.Errorf("gippity.classifier_model = %q, want gpt-4o-mini", cfg.Gippity.ClassifierModel)
	}
	if cfg.Gippity.ClassifierRateLimitPerHour != 5 {
		t.Errorf("gippity.classifier_rate_limit_per_hour = %d, want 5", cfg.Gippity.ClassifierRateLimitPerHour)
	}
	if want := []string{"gustaff", "Gippity"}; !reflect.DeepEqual(cfg.Gippity.ClassifierNames, want) {
		t.Errorf("gippity.classifier_names = %v, want %v", cfg.Gippity.ClassifierNames, want)
	}
	if cfg.Gippity.DebugChannel != "dev-chan" {
		t.Errorf("gippity.debug_channel = %q, want dev-chan", cfg.Gippity.DebugChannel)
	}

	omitted := `
discord:
  token: "tok"
gippity:
  allowed_guilds: ["456"]
`
	cfg, err = decodeConfig(strings.NewReader(omitted))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Gippity.RespondToAllMessages {
		t.Error("gippity.respond_to_all_messages should default to false")
	}
	if cfg.Gippity.ClassifierModel != "" {
		t.Errorf("gippity.classifier_model = %q, want empty", cfg.Gippity.ClassifierModel)
	}
	// Unlike classifier_model, this key has a built-in default: the effective
	// limit has always been 30/h, and cfg now applies it so a merged runtime
	// validation, the module and /status cannot disagree.
	if cfg.Gippity.ClassifierRateLimitPerHour != defaultGippityClassifierRateLimit {
		t.Errorf("gippity.classifier_rate_limit_per_hour = %d, want default %d", cfg.Gippity.ClassifierRateLimitPerHour, defaultGippityClassifierRateLimit)
	}
	if cfg.Gippity.DebugChannel != "" {
		t.Errorf("gippity.debug_channel = %q, want empty", cfg.Gippity.DebugChannel)
	}
}

func TestDecodeConfig_gippityTools(t *testing.T) {
	yaml := `
discord:
  token: "tok"
gippity:
  allowed_guilds: ["456"]
  tools:
    enabled: true
    max_iterations: 5
    allowed: ["search_chat_history", "bot_status"]
    reasoning_effort: "none"
`
	cfg, err := decodeConfig(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.Gippity.Tools.Enabled {
		t.Error("gippity.tools.enabled should be true")
	}
	if cfg.Gippity.Tools.MaxIterations != 5 {
		t.Errorf("gippity.tools.max_iterations = %d, want 5", cfg.Gippity.Tools.MaxIterations)
	}
	if len(cfg.Gippity.Tools.Allowed) != 2 || cfg.Gippity.Tools.Allowed[0] != "search_chat_history" {
		t.Errorf("gippity.tools.allowed = %v", cfg.Gippity.Tools.Allowed)
	}
	if cfg.Gippity.Tools.ReasoningEffort != "none" {
		t.Errorf("gippity.tools.reasoning_effort = %q, want none", cfg.Gippity.Tools.ReasoningEffort)
	}

	omitted := `
discord:
  token: "tok"
gippity:
  allowed_guilds: ["456"]
`
	cfg, err = decodeConfig(strings.NewReader(omitted))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Gippity.Tools.Enabled {
		t.Error("gippity.tools.enabled should default to false")
	}
	if cfg.Gippity.Tools.MaxIterations != defaultGippityToolMaxIterations {
		t.Errorf("gippity.tools.max_iterations = %d, want default %d", cfg.Gippity.Tools.MaxIterations, defaultGippityToolMaxIterations)
	}
	if len(cfg.Gippity.Tools.Allowed) != 0 {
		t.Errorf("gippity.tools.allowed = %v, want empty", cfg.Gippity.Tools.Allowed)
	}
	if cfg.Gippity.Tools.ReasoningEffort != "" {
		t.Errorf("gippity.tools.reasoning_effort = %q, want empty", cfg.Gippity.Tools.ReasoningEffort)
	}
}

func TestDecodeConfig_gippityToolsRejectsInvalidReasoningEffort(t *testing.T) {
	yaml := `
discord:
  token: "tok"
gippity:
  allowed_guilds: ["456"]
  tools:
    enabled: true
    reasoning_effort: "turbo"
`
	_, err := decodeConfig(strings.NewReader(yaml))
	if err == nil || !strings.Contains(err.Error(), "gippity.tools.reasoning_effort") {
		t.Fatalf("decodeConfig() error = %v, want error mentioning gippity.tools.reasoning_effort", err)
	}
}

func TestDecodeConfig_llmAPIKey(t *testing.T) {
	yaml := `
discord:
  token: "tok"
gippity:
  allowed_guilds: ["456"]
llm:
  provider: "openai"
  api_key: "sk-config-secret"
`
	cfg, err := decodeConfig(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.LLM.APIKey != "sk-config-secret" {
		t.Errorf("llm.api_key = %q, want sk-config-secret", cfg.LLM.APIKey)
	}

	omitted := `
discord:
  token: "tok"
gippity:
  allowed_guilds: ["456"]
`
	cfg, err = decodeConfig(strings.NewReader(omitted))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.LLM.APIKey != "" {
		t.Errorf("llm.api_key = %q, want empty", cfg.LLM.APIKey)
	}
}

func TestDecodeConfig_gippityToolsRejectsInvalidMaxIterations(t *testing.T) {
	yaml := `
discord:
  token: "tok"
gippity:
  allowed_guilds: ["456"]
  tools:
    enabled: true
    max_iterations: 0
`
	_, err := decodeConfig(strings.NewReader(yaml))
	if err == nil || !strings.Contains(err.Error(), "gippity.tools.max_iterations") {
		t.Fatalf("decodeConfig() error = %v, want error mentioning gippity.tools.max_iterations", err)
	}
}

func TestDecodeConfig_llmFields(t *testing.T) {
	yaml := `
discord:
  token: "tok"
gippity:
  allowed_guilds: ["456"]
llm:
  provider: "openrouter"
  model: "anthropic/claude-sonnet-4"
  vision_model: "google/gemini-2.5-flash"
  base_url: "https://openrouter.example/api/v1"
  http_referer: "https://gidbig.example"
  title: "Gidbig"
  personality: "be a pirate"
  personality_preset: "hal"
`
	cfg, err := decodeConfig(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.LLM.Personality != "be a pirate" {
		t.Errorf("llm.personality = %q, want %q", cfg.LLM.Personality, "be a pirate")
	}
	if cfg.LLM.Preset != "hal" {
		t.Errorf("llm.personality_preset = %q, want %q", cfg.LLM.Preset, "hal")
	}
	if cfg.LLM.Provider != "openrouter" || cfg.LLM.Model != "anthropic/claude-sonnet-4" {
		t.Errorf("llm provider/model = %q/%q", cfg.LLM.Provider, cfg.LLM.Model)
	}
	if cfg.LLM.VisionModel != "google/gemini-2.5-flash" {
		t.Errorf("llm.vision_model = %q", cfg.LLM.VisionModel)
	}
	if cfg.LLM.BaseURL != "https://openrouter.example/api/v1" {
		t.Errorf("llm.base_url = %q", cfg.LLM.BaseURL)
	}
	if cfg.LLM.HTTPReferer != "https://gidbig.example" || cfg.LLM.Title != "Gidbig" {
		t.Errorf("llm attribution = %q/%q", cfg.LLM.HTTPReferer, cfg.LLM.Title)
	}
}

func TestDecodeConfig_llmOmitted(t *testing.T) {
	yaml := `
discord:
  token: "tok"
gippity:
  allowed_guilds: ["456"]
`
	cfg, err := decodeConfig(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.LLM.Provider != "" || cfg.LLM.Model != "" || cfg.LLM.VisionModel != "" ||
		cfg.LLM.Personality != "" || cfg.LLM.Preset != "" {
		t.Errorf("llm fields should be empty when omitted, got %+v", cfg.LLM)
	}
}

func TestDecodeConfig_leetoclockFields(t *testing.T) {
	yaml := `
discord:
  token: "tok"
gippity:
  allowed_guilds: ["456"]
leetoclock:
  announcement_channels: ["chan1", "chan2"]
  debug_channel: "debugchan"
  debug: true
`
	cfg, err := decodeConfig(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"chan1", "chan2"}
	if len(cfg.Leetoclock.AnnouncementChannels) != len(want) {
		t.Fatalf("announcement_channels = %v, want %v", cfg.Leetoclock.AnnouncementChannels, want)
	}
	for i, c := range want {
		if cfg.Leetoclock.AnnouncementChannels[i] != c {
			t.Errorf("announcement_channels[%d] = %q, want %q", i, cfg.Leetoclock.AnnouncementChannels[i], c)
		}
	}
	if cfg.Leetoclock.DebugChannel != "debugchan" {
		t.Errorf("debug_channel = %q, want %q", cfg.Leetoclock.DebugChannel, "debugchan")
	}
	if !cfg.Leetoclock.Debug {
		t.Error("debug should be true")
	}
}

func TestDecodeConfig_leetoclockEmojis(t *testing.T) {
	yaml := `
discord:
  token: "tok"
gippity:
  allowed_guilds: ["456"]
leetoclock:
  emojis:
    zonk: "111"
    lol: "222"
    notamused: "333"
    wat: "444"
`
	cfg, err := decodeConfig(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Leetoclock.Emojis.Zonk != "111" || cfg.Leetoclock.Emojis.Lol != "222" ||
		cfg.Leetoclock.Emojis.NotAmused != "333" || cfg.Leetoclock.Emojis.Wat != "444" {
		t.Errorf("leetoclock.emojis = %+v, want 111/222/333/444", cfg.Leetoclock.Emojis)
	}
}

func TestDecodeConfig_leetoclockEmojisOmitted(t *testing.T) {
	yaml := `
discord:
  token: "tok"
gippity:
  allowed_guilds: ["456"]
leetoclock:
  debug: false
`
	cfg, err := decodeConfig(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Leetoclock.Emojis != (LeetoclockEmojisConfig{}) {
		t.Errorf("leetoclock.emojis should be empty when omitted, got %+v", cfg.Leetoclock.Emojis)
	}
}

func TestDecodeConfig_webEsoFields(t *testing.T) {
	yaml := `
discord:
  token: "tok"
gippity:
  allowed_guilds: ["456"]
web:
  port: 8080
  session_secret: "secret"
  eso:
    rate_limit_burst: 2
    rate_limit_per_minute: 120
    max_concurrent_requests: 1
    request_timeout_seconds: 3
`
	cfg, err := decodeConfig(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Web.Eso.RateLimitBurst != 2 {
		t.Errorf("web.eso.rate_limit_burst = %d, want 2", cfg.Web.Eso.RateLimitBurst)
	}
	if cfg.Web.Eso.RateLimitPerMinute != 120 {
		t.Errorf("web.eso.rate_limit_per_minute = %d, want 120", cfg.Web.Eso.RateLimitPerMinute)
	}
	if cfg.Web.Eso.MaxConcurrentRequests != 1 {
		t.Errorf("web.eso.max_concurrent_requests = %d, want 1", cfg.Web.Eso.MaxConcurrentRequests)
	}
	if cfg.Web.Eso.RequestTimeoutSeconds != 3 {
		t.Errorf("web.eso.request_timeout_seconds = %d, want 3", cfg.Web.Eso.RequestTimeoutSeconds)
	}
}

func TestDecodeConfig_webEsoOmitted(t *testing.T) {
	yaml := `
discord:
  token: "tok"
gippity:
  allowed_guilds: ["456"]
web:
  port: 8080
  session_secret: "secret"
`
	cfg, err := decodeConfig(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Web.Eso != (EsoAPIConfig{}) {
		t.Errorf("web.eso should be empty when omitted, got %+v", cfg.Web.Eso)
	}
}

func TestDecodeConfig_leetoclockOmitted(t *testing.T) {
	yaml := `
discord:
  token: "tok"
gippity:
  allowed_guilds: ["456"]
`
	cfg, err := decodeConfig(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Leetoclock.AnnouncementChannels) != 0 || cfg.Leetoclock.DebugChannel != "" || cfg.Leetoclock.Debug {
		t.Errorf("leetoclock fields should be empty when omitted, got %+v", cfg.Leetoclock)
	}
}

func TestDecodeConfig_missingToken(t *testing.T) {
	yaml := `
discord:
  owner_id: "123"
gippity:
  allowed_guilds: ["456"]
`
	_, err := decodeConfig(strings.NewReader(yaml))
	if err == nil {
		t.Fatal("expected error for missing discord.token, got nil")
	}
	if !strings.Contains(err.Error(), "discord.token") {
		t.Errorf("error should mention discord.token, got: %v", err)
	}
}

func TestDecodeConfig_invalidYAML(t *testing.T) {
	_, err := decodeConfig(strings.NewReader(":::not valid yaml:::"))
	if err == nil {
		t.Fatal("expected error for invalid YAML, got nil")
	}
}

func TestDecodeConfig_emptyToken(t *testing.T) {
	yaml := `
discord:
  token: ""
gippity:
  allowed_guilds: ["456"]
`
	_, err := decodeConfig(strings.NewReader(yaml))
	if err == nil {
		t.Fatal("expected error for empty discord.token, got nil")
	}
}

func TestDecodeConfig_webFields(t *testing.T) {
	yaml := `
discord:
  token: "tok"
web:
  port: 9090
  session_secret: "supersecret"
  oauth:
    client_id: "cid"
    client_secret: "csec"
    redirect_uri: "http://localhost/callback"
gippity:
  allowed_guilds: ["456"]
`
	cfg, err := decodeConfig(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Web.Port != 9090 {
		t.Errorf("port = %d, want 9090", cfg.Web.Port)
	}
	if cfg.Web.Oauth.ClientID != "cid" {
		t.Errorf("client_id = %q, want %q", cfg.Web.Oauth.ClientID, "cid")
	}
	if cfg.Web.SessionSecret != "supersecret" {
		t.Errorf("session_secret = %q, want %q", cfg.Web.SessionSecret, "supersecret")
	}
}

func TestDecodeConfig_webEnabledMissingSessionSecret(t *testing.T) {
	yaml := `
discord:
  token: "tok"
web:
  port: 8080
  oauth:
    client_id: "cid"
    client_secret: "csec"
    redirect_uri: "http://localhost/callback"
gippity:
  allowed_guilds: ["456"]
`
	_, err := decodeConfig(strings.NewReader(yaml))
	if err == nil {
		t.Fatal("expected error for missing session_secret when web is enabled, got nil")
	}
	if !strings.Contains(err.Error(), "session_secret") {
		t.Errorf("error should mention session_secret, got: %v", err)
	}
}

func TestDecodeConfig_webDisabledNoSessionSecretRequired(t *testing.T) {
	yaml := `
discord:
  token: "tok"
gippity:
  allowed_guilds: ["456"]
`
	_, err := decodeConfig(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("unexpected error when web server not configured: %v", err)
	}
}

func TestDecodeConfig_webPortSetNoOAuthMissingSessionSecret(t *testing.T) {
	yaml := `
discord:
  token: "tok"
web:
  port: 8080
gippity:
  allowed_guilds: ["456"]
`
	_, err := decodeConfig(strings.NewReader(yaml))
	if err == nil {
		t.Fatal("expected error for missing session_secret when web.port is set without OAuth, got nil")
	}
	if !strings.Contains(err.Error(), "session_secret") {
		t.Errorf("error should mention session_secret, got: %v", err)
	}
}

func TestDecodeConfig_missingGippityAllowedGuilds(t *testing.T) {
	yaml := `
discord:
  token: "tok"
`
	_, err := decodeConfig(strings.NewReader(yaml))
	if err == nil {
		t.Fatal("expected error for missing gippity.allowed_guilds, got nil")
	}
	if !strings.Contains(err.Error(), "gippity.allowed_guilds") {
		t.Errorf("error should mention gippity.allowed_guilds, got: %v", err)
	}
}

func TestDecodeConfig_tuningDefaultsApplied(t *testing.T) {
	yaml := `
discord:
  token: "tok"
gippity:
  allowed_guilds: ["456"]
`
	cfg, err := decodeConfig(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Gippity.RateLimitMessagesPerHour != defaultGippityRateLimitPerHour {
		t.Errorf("rate_limit_messages_per_hour = %d, want default %d", cfg.Gippity.RateLimitMessagesPerHour, defaultGippityRateLimitPerHour)
	}
	if cfg.Gippity.ClassifierRateLimitPerHour != defaultGippityClassifierRateLimit {
		t.Errorf("classifier_rate_limit_per_hour = %d, want default %d", cfg.Gippity.ClassifierRateLimitPerHour, defaultGippityClassifierRateLimit)
	}
	if cfg.Soundboard.QueueMaxDepth != defaultSoundboardQueueMaxDepth {
		t.Errorf("queue_max_depth = %d, want default %d", cfg.Soundboard.QueueMaxDepth, defaultSoundboardQueueMaxDepth)
	}
}

func TestDecodeConfig_tuningOverrides(t *testing.T) {
	yaml := `
discord:
  token: "tok"
gippity:
  allowed_guilds: ["456"]
  rate_limit_messages_per_hour: 5
soundboard:
  queue_max_depth: 12
`
	cfg, err := decodeConfig(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Gippity.RateLimitMessagesPerHour != 5 {
		t.Errorf("rate_limit_messages_per_hour = %d, want 5", cfg.Gippity.RateLimitMessagesPerHour)
	}
	if cfg.Soundboard.QueueMaxDepth != 12 {
		t.Errorf("queue_max_depth = %d, want 12", cfg.Soundboard.QueueMaxDepth)
	}
}

func TestDecodeConfig_rejectsInvalidTuning(t *testing.T) {
	tests := []struct {
		name      string
		field     string
		value     int
		wantError string
	}{
		{"zero rate limit", "rate_limit_messages_per_hour", 0, "gippity.rate_limit_messages_per_hour"},
		{"negative rate limit", "rate_limit_messages_per_hour", -1, "gippity.rate_limit_messages_per_hour"},
		{"zero queue depth", "queue_max_depth", 0, "soundboard.queue_max_depth"},
		{"negative queue depth", "queue_max_depth", -1, "soundboard.queue_max_depth"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			yaml := "discord:\n  token: tok\ngippity:\n  allowed_guilds: [guild]\n"
			if strings.HasPrefix(tt.field, "rate_") {
				yaml += fmt.Sprintf("  %s: %d\n", tt.field, tt.value)
			} else {
				yaml += fmt.Sprintf("soundboard:\n  %s: %d\n", tt.field, tt.value)
			}

			_, err := decodeConfig(strings.NewReader(yaml))
			if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("decodeConfig() error = %v, want error containing %q", err, tt.wantError)
			}
		})
	}
}

// TestDecode_DoesNotValidate pins the split: Decode applies the defaults but
// never rejects a config, so a runtime settings write can decode and re-check
// separately.
func TestDecode_DoesNotValidate(t *testing.T) {
	// Deliberately missing token and allowed_guilds, with an invalid queue
	// depth: only Validate may reject this.
	decoded, err := Decode(strings.NewReader("soundboard:\n  queue_max_depth: 0\n"))
	if err != nil {
		t.Fatalf("Decode() error = %v, want nil", err)
	}
	if decoded.Discord.Token != "" {
		t.Errorf("token = %q, want empty", decoded.Discord.Token)
	}
	if decoded.Gippity.RateLimitMessagesPerHour != defaultGippityRateLimitPerHour {
		t.Errorf("rate limit = %d, want the default %d",
			decoded.Gippity.RateLimitMessagesPerHour, defaultGippityRateLimitPerHour)
	}
	if decoded.Gippity.Tools.MaxIterations != defaultGippityToolMaxIterations {
		t.Errorf("tool max iterations = %d, want the default %d",
			decoded.Gippity.Tools.MaxIterations, defaultGippityToolMaxIterations)
	}
	if decoded.Soundboard.QueueMaxDepth != 0 {
		t.Errorf("queue depth = %d, want the explicit 0", decoded.Soundboard.QueueMaxDepth)
	}
	if err := Validate(decoded); err == nil {
		t.Error("Validate() = nil for a config with no token, want an error")
	}
}

func TestValidate_RejectsInvalidConfigs(t *testing.T) {
	valid := func() *Config {
		c := &Config{}
		c.Discord.Token = "tok"
		c.Gippity.AllowedGuilds = []string{"guild"}
		c.Gippity.RateLimitMessagesPerHour = 30
		c.Gippity.Tools.MaxIterations = 3
		c.Gippity.ClassifierRateLimitPerHour = defaultGippityClassifierRateLimit
		c.Soundboard.QueueMaxDepth = 6
		c.LLM.MaxTokens = defaultLLMMaxTokens
		return c
	}

	tests := []struct {
		name      string
		mutate    func(*Config)
		wantError string
	}{
		{"missing token", func(c *Config) { c.Discord.Token = "" }, "discord.token"},
		{"missing allowed guilds", func(c *Config) { c.Gippity.AllowedGuilds = nil }, "gippity.allowed_guilds"},
		{"non-positive rate limit", func(c *Config) { c.Gippity.RateLimitMessagesPerHour = 0 }, "rate_limit_messages_per_hour"},
		{"non-positive classifier rate limit", func(c *Config) { c.Gippity.ClassifierRateLimitPerHour = 0 }, "classifier_rate_limit_per_hour"},
		{"web port without session secret", func(c *Config) { c.Web.Port = 8080 }, "web.session_secret"},
		{"tools enabled with zero iterations", func(c *Config) {
			c.Gippity.Tools.Enabled = true
			c.Gippity.Tools.MaxIterations = 0
		}, "max_iterations"},
		{"invalid reasoning effort", func(c *Config) { c.Gippity.Tools.ReasoningEffort = "bogus" }, "reasoning_effort"},
		{"non-positive queue depth", func(c *Config) { c.Soundboard.QueueMaxDepth = 0 }, "queue_max_depth"},
		{"non-positive max tokens", func(c *Config) { c.LLM.MaxTokens = 0 }, "llm.max_tokens"},
		{"negative log window", func(c *Config) { c.Logs.WindowLines = -1 }, "logs.window_lines"},
		{"oversized log window", func(c *Config) { c.Logs.WindowLines = 100001 }, "logs.window_lines"},
	}

	if err := Validate(valid()); err != nil {
		t.Fatalf("Validate(valid) error = %v, want nil", err)
	}
	if err := Validate(nil); err == nil {
		t.Error("Validate(nil) = nil, want an error")
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := valid()
			tt.mutate(c)
			err := Validate(c)
			if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("Validate() error = %v, want error containing %q", err, tt.wantError)
			}
		})
	}
}

// TestDefaultLLMMaxTokensMatchesLLM keeps the duplicated constant honest: cfg is
// a leaf package and does not import llm, so nothing else ties them together.
func TestDefaultLLMMaxTokensMatchesLLM(t *testing.T) {
	if int64(defaultLLMMaxTokens) != llm.DefaultMaxTokens {
		t.Errorf("cfg defaultLLMMaxTokens = %d, llm.DefaultMaxTokens = %d; keep them equal",
			defaultLLMMaxTokens, llm.DefaultMaxTokens)
	}
}

func TestDecode_LLMMaxTokensDefault(t *testing.T) {
	decoded, err := Decode(strings.NewReader("llm:\n  model: vendor/model\n"))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if decoded.LLM.MaxTokens != defaultLLMMaxTokens {
		t.Errorf("llm.max_tokens = %d, want the default %d", decoded.LLM.MaxTokens, defaultLLMMaxTokens)
	}

	explicit, err := Decode(strings.NewReader("llm:\n  max_tokens: 700\n"))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if explicit.LLM.MaxTokens != 700 {
		t.Errorf("llm.max_tokens = %d, want the explicit 700", explicit.LLM.MaxTokens)
	}
}

func TestValidate_ShortSessionSecretWarns(t *testing.T) {
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	for _, tt := range []struct {
		secret   string
		wantWarn bool
	}{
		{"short", true},
		{strings.Repeat("x", minSessionSecretLen), false},
	} {
		logs.Reset()
		c := &Config{}
		c.Discord.Token = "tok"
		c.Gippity.AllowedGuilds = []string{"guild"}
		c.Gippity.RateLimitMessagesPerHour = 30
		c.Gippity.Tools.MaxIterations = 3
		c.Gippity.ClassifierRateLimitPerHour = defaultGippityClassifierRateLimit
		c.Soundboard.QueueMaxDepth = 6
		c.LLM.MaxTokens = defaultLLMMaxTokens
		c.Web.Port = 8080
		c.Web.SessionSecret = tt.secret
		if err := Validate(c); err != nil {
			t.Fatalf("Validate(len %d) error = %v, want nil", len(tt.secret), err)
		}
		if got := strings.Contains(logs.String(), "web.session_secret is short"); got != tt.wantWarn {
			t.Errorf("len %d: warned = %v, want %v (logs: %s)", len(tt.secret), got, tt.wantWarn, logs.String())
		}
	}
}

func TestLogWindowLines(t *testing.T) {
	c := &Config{}
	if got := c.LogWindowLines(); got != DefaultLogWindowLines {
		t.Errorf("unset = %d, want %d", got, DefaultLogWindowLines)
	}
	c.Logs.WindowLines = 500
	if got := c.LogWindowLines(); got != 500 {
		t.Errorf("explicit = %d, want 500", got)
	}
	c.Logs.WindowLines = 100000
	c.Discord.Token = "tok"
	c.Gippity.AllowedGuilds = []string{"g"}
	c.Gippity.RateLimitMessagesPerHour = 1
	c.Gippity.ClassifierRateLimitPerHour = 1
	c.Soundboard.QueueMaxDepth = 1
	c.LLM.MaxTokens = 1
	if err := Validate(c); err != nil {
		t.Errorf("Validate(max window) = %v", err)
	}
}

func TestDecodeConfig_tmdb(t *testing.T) {
	base := "discord:\n  token: t\ngippity:\n  allowed_guilds: [\"g\"]\n"

	c, err := decodeConfig(strings.NewReader(base))
	if err != nil {
		t.Fatalf("empty tmdb section: %v", err)
	}
	if c.TMDB.ReadAccessToken != "" || c.TMDBRegion() != "DE" || c.TMDBLanguage() != "de-DE" {
		t.Errorf("defaults = %q %q %q, want empty, DE, de-DE", c.TMDB.ReadAccessToken, c.TMDBRegion(), c.TMDBLanguage())
	}

	c, err = decodeConfig(strings.NewReader(base + "tmdb:\n  read_access_token: tok\n  region: AT\n  language: en\n"))
	if err != nil {
		t.Fatalf("valid tmdb section: %v", err)
	}
	if c.TMDB.ReadAccessToken != "tok" || c.TMDBRegion() != "AT" || c.TMDBLanguage() != "en" {
		t.Errorf("values = %q %q %q", c.TMDB.ReadAccessToken, c.TMDBRegion(), c.TMDBLanguage())
	}

	for _, bad := range []string{"tmdb:\n  region: de\n", "tmdb:\n  region: DEU\n", "tmdb:\n  language: DE-de\n", "tmdb:\n  language: german\n"} {
		if _, err := decodeConfig(strings.NewReader(base + bad)); err == nil {
			t.Errorf("decodeConfig(%q) = nil error, want validation failure", bad)
		}
	}
}

func TestDecodeConfig_tv(t *testing.T) {
	base := "discord:\n  token: t\ngippity:\n  allowed_guilds: [\"g\"]\n"
	c, err := decodeConfig(strings.NewReader(base))
	if err != nil {
		t.Fatalf("empty tv section: %v", err)
	}
	if c.TV.Enabled || len(c.TV.Channels) != 0 || c.TVCacheDir() != DefaultTVCacheDir || c.TVRefreshInterval() != DefaultTVRefreshInterval {
		t.Errorf("defaults = %+v dir=%q interval=%s", c.TV, c.TVCacheDir(), c.TVRefreshInterval())
	}
	c, err = decodeConfig(strings.NewReader(base + "tv:\n  enabled: true\n  channels: [ZDF.de]\n  cache_dir: /tmp/x\n  refresh_interval: 12h\n"))
	if err != nil {
		t.Fatalf("valid tv section: %v", err)
	}
	if !c.TV.Enabled || len(c.TV.Channels) != 1 || c.TVCacheDir() != "/tmp/x" || c.TVRefreshInterval() != 12*time.Hour {
		t.Errorf("values = %+v", c.TV)
	}
	if _, err := decodeConfig(strings.NewReader(base + "tv:\n  enabled: true\n  refresh_interval: 30m\n")); err == nil {
		t.Error("refresh_interval 30m accepted, want error")
	}
	if got := c.TVRegions(); len(got) != 1 || got[0] != "de" || c.TVTimezone() != "Europe/Berlin" {
		t.Errorf("default regions/timezone = %v %q", got, c.TVTimezone())
	}
	if _, err := decodeConfig(strings.NewReader(base + "tv:\n  enabled: true\n  regions: [de, at]\n  channels: [ZDF.de, ORF1.at]\n  timezone: Europe/Vienna\n")); err != nil {
		t.Errorf("valid regions/timezone rejected: %v", err)
	}
	for name, bad := range map[string]string{
		"region without channels": "tv:\n  enabled: true\n  regions: [at]\n",
		"de plus at, no channels": "tv:\n  enabled: true\n  regions: [de, at]\n",
		"upper-case region":       "tv:\n  enabled: true\n  regions: [DE]\n",
		"long region":             "tv:\n  enabled: true\n  regions: [deu]\n",
		"duplicate region":        "tv:\n  enabled: true\n  regions: [de, de]\n",
		"unknown timezone":        "tv:\n  enabled: true\n  timezone: Mars/Base\n",
	} {
		if _, err := decodeConfig(strings.NewReader(base + bad)); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if _, err := decodeConfig(strings.NewReader(base + "tv:\n  regions: [at]\n  timezone: Mars/Base\n")); err != nil {
		t.Errorf("disabled tv validated: %v", err)
	}
	if _, err := decodeConfig(strings.NewReader(base + "tv:\n  refresh_interval: 30m\n")); err != nil {
		t.Errorf("disabled tv with short interval rejected: %v", err)
	}
}

func TestDecodeConfig_triviaDefaults(t *testing.T) {
	yaml := `
discord:
  token: "tok"
gippity:
  allowed_guilds: ["456"]
`
	c, err := decodeConfig(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.Trivia.DropinChance != defaultTriviaDropinChance {
		t.Errorf("dropin_chance = %v, want %v", c.Trivia.DropinChance, defaultTriviaDropinChance)
	}
	if got := c.TriviaDropinCooldown(); got != 24*time.Hour {
		t.Errorf("cooldown = %v, want 24h", got)
	}
}

func TestDecodeConfig_triviaExplicitZeroDisables(t *testing.T) {
	yaml := `
discord:
  token: "tok"
gippity:
  allowed_guilds: ["456"]
trivia:
  dropin_chance: 0
  dropin_cooldown: 2h
`
	c, err := decodeConfig(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.Trivia.DropinChance != 0 {
		t.Errorf("dropin_chance = %v, want 0", c.Trivia.DropinChance)
	}
	if got := c.TriviaDropinCooldown(); got != 2*time.Hour {
		t.Errorf("cooldown = %v, want 2h", got)
	}
}

func TestValidate_triviaRejectsBadValues(t *testing.T) {
	for _, tc := range []struct {
		name     string
		chance   float64
		cooldown string
		want     string
	}{
		{"chance above one", 1.5, "24h", "trivia.dropin_chance"},
		{"chance negative", -0.1, "24h", "trivia.dropin_chance"},
		{"cooldown garbage", 0.01, "soon", "trivia.dropin_cooldown"},
		{"cooldown zero", 0.01, "0s", "trivia.dropin_cooldown"},
		{"cooldown negative", 0.01, "-1h", "trivia.dropin_cooldown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &Config{}
			c.Discord.Token = "tok"
			c.Gippity.AllowedGuilds = []string{"1"}
			c.Gippity.RateLimitMessagesPerHour = 1
			c.Gippity.ClassifierRateLimitPerHour = 1
			c.Soundboard.QueueMaxDepth = 1
			c.LLM.MaxTokens = 1
			c.Trivia.DropinChance = tc.chance
			c.Trivia.DropinCooldown = tc.cooldown
			err := Validate(c)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate() = %v, want error mentioning %s", err, tc.want)
			}
		})
	}
}
