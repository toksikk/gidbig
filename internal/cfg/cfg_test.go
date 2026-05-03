package cfg

import (
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
