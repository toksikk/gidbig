package cfg

import (
	"fmt"
	"strings"
	"testing"
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
