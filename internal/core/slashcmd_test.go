package gidbig

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/toksikk/gidbig/internal/bot"
	"github.com/toksikk/gidbig/internal/cfg"
)

type discordRequest struct {
	method string
	path   string
	body   []byte
}

func discordTestSession(t *testing.T) (*discordgo.Session, <-chan discordRequest) {
	t.Helper()
	requests := make(chan discordRequest, 10)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests <- discordRequest{method: r.Method, path: r.URL.Path, body: body}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(server.Close)

	origAPI, origWebhooks := discordgo.EndpointAPI, discordgo.EndpointWebhooks
	discordgo.EndpointAPI = server.URL + "/"
	discordgo.EndpointWebhooks = server.URL + "/webhooks/"
	t.Cleanup(func() {
		discordgo.EndpointAPI = origAPI
		discordgo.EndpointWebhooks = origWebhooks
	})

	s, err := discordgo.New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	return s, requests
}

func coreInteraction(name, guildID, userID string, options ...*discordgo.ApplicationCommandInteractionDataOption) *discordgo.InteractionCreate {
	i := &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		ID: "interaction", AppID: "app", Token: "token", Type: discordgo.InteractionApplicationCommand,
		GuildID: guildID, Data: discordgo.ApplicationCommandInteractionData{Name: name, Options: options},
	}}
	if guildID == "" {
		i.User = &discordgo.User{ID: userID}
	} else {
		i.Member = &discordgo.Member{User: &discordgo.User{ID: userID}}
	}
	return i
}

func awaitDiscordRequest(t *testing.T, requests <-chan discordRequest) discordRequest {
	t.Helper()
	select {
	case req := <-requests:
		return req
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Discord request")
		return discordRequest{}
	}
}

func responseContent(t *testing.T, body []byte) string {
	t.Helper()
	var payload struct {
		Data    *discordgo.InteractionResponseData `json:"data"`
		Content *string                            `json:"content"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Data != nil {
		return payload.Data.Content
	}
	if payload.Content != nil {
		return *payload.Content
	}
	return ""
}

func TestCoreSlashCommands_UptimeMetadata(t *testing.T) {
	commands := coreSlashCommands()
	if len(commands) != 1 || commands[0].Name != "uptime" {
		t.Fatalf("unexpected core commands: %#v", commands)
	}
}

func TestBuildUptimeBody_UsesInjectedStart(t *testing.T) {
	m := NewCore()
	start := time.Date(2026, 10, 6, 5, 47, 0, 0, time.UTC)
	m.startTime = start

	body := m.buildUptimeBody()
	if !strings.Contains(body, start.Format("2006-01-02 15:04:05")) {
		t.Errorf("body = %q, want it to contain the injected start time", body)
	}
}

func TestCoreSlashHandlers_Uptime(t *testing.T) {
	mod := NewCore()
	mod.conf = &cfg.Config{}
	mod.conf.Discord.OwnerID = "owner"

	tests := []struct {
		name, command, user, contains string
	}{
		{"uptime owner", "uptime", "owner", "Uptime:"},
		{"uptime denied", "uptime", "user", "Access denied."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, requests := discordTestSession(t)
			// Uptime is owner-only through the OwnerOnly middleware the command
			// is registered with, so exercise the gate here.
			handler := bot.OwnerOnly("owner")(mod.onCoreSlashInteractionCreate)
			handler(s, coreInteraction(tt.command, "guild", tt.user))
			req := awaitDiscordRequest(t, requests)
			if !strings.Contains(responseContent(t, req.body), tt.contains) {
				t.Errorf("response %s does not contain %q", req.body, tt.contains)
			}
			var response discordgo.InteractionResponse
			if err := json.Unmarshal(req.body, &response); err != nil {
				t.Fatal(err)
			}
			if response.Data.Flags != discordgo.MessageFlagsEphemeral {
				t.Errorf("response flags = %v, want ephemeral", response.Data.Flags)
			}
		})
	}
}
