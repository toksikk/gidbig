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
	if !cfg.DevMode {
		t.Error("dev_mode should be true")
	}
}

func TestDecodeConfig_missingToken(t *testing.T) {
	yaml := `
discord:
  owner_id: "123"
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
  oauth:
    client_id: "cid"
    client_secret: "csec"
    redirect_uri: "http://localhost/callback"
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
}
