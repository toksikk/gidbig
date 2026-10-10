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

func TestCoreSlashHandlers_Uptime(t *testing.T) {
	origConf := conf
	conf = &cfg.Config{}
	conf.Discord.OwnerID = "owner"
	t.Cleanup(func() { conf = origConf })

	tests := []struct {
		name, command, user, contains string
	}{
		{"uptime owner", "uptime", "owner", "Uptime:"},
		{"uptime denied", "uptime", "user", "Access denied."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, requests := discordTestSession(t)
			onCoreSlashInteractionCreate(s, coreInteraction(tt.command, "guild", tt.user))
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
