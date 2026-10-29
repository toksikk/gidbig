package tmdb

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/toksikk/gidbig/internal/bot"
)

// shutdownSession routes discordgo REST calls to a local server and returns the
// captured request bodies. It swaps process-global endpoints; do not run in
// parallel.
func shutdownSession(t *testing.T) (*discordgo.Session, <-chan []byte) {
	t.Helper()
	requests := make(chan []byte, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests <- body
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

func awaitBody(t *testing.T, requests <-chan []byte) []byte {
	t.Helper()
	select {
	case b := <-requests:
		return b
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for a Discord request")
		return nil
	}
}

func TestHandlersReplyShuttingDownAfterShutdown(t *testing.T) {
	str := discordgo.ApplicationCommandOptionString
	tests := map[string]struct {
		call      func(m *Module, s *discordgo.Session, i *discordgo.InteractionCreate)
		i         *discordgo.Interaction
		deferType discordgo.InteractionResponseType
		followup  bool
	}{
		"command": {
			call: (*Module).onCommand,
			i: &discordgo.Interaction{Type: discordgo.InteractionApplicationCommand, Data: discordgo.ApplicationCommandInteractionData{
				Name: commandName,
				Options: []*discordgo.ApplicationCommandInteractionDataOption{
					{Name: "title", Type: str, Value: "Dune"},
					{Name: "region", Type: str, Value: "DE"},
				},
			}},
			deferType: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		},
		"pick": {
			call: (*Module).onPick,
			i: &discordgo.Interaction{Type: discordgo.InteractionMessageComponent, Data: discordgo.MessageComponentInteractionData{
				CustomID: pickCustomID("123", "DE"),
				Values:   []string{"movie:1"},
			}},
			deferType: discordgo.InteractionResponseDeferredMessageUpdate,
			followup:  true,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			s, requests := shutdownSession(t)
			b := bot.New(bot.Deps{Session: s})
			b.Shutdown()

			m := New()
			m.router = b.Router
			tt.i.ID, tt.i.AppID, tt.i.Token = "interaction", "app", "token"
			tt.i.Member = &discordgo.Member{User: &discordgo.User{ID: "123"}}
			tt.call(m, s, &discordgo.InteractionCreate{Interaction: tt.i})

			var deferred discordgo.InteractionResponse
			if err := json.Unmarshal(awaitBody(t, requests), &deferred); err != nil {
				t.Fatal(err)
			}
			if deferred.Type != tt.deferType {
				t.Errorf("defer type = %v, want %v", deferred.Type, tt.deferType)
			}
			var edit struct {
				Content    *string                `json:"content"`
				Components []json.RawMessage      `json:"components"`
				Flags      discordgo.MessageFlags `json:"flags"`
			}
			if err := json.Unmarshal(awaitBody(t, requests), &edit); err != nil {
				t.Fatal(err)
			}
			if edit.Content == nil || *edit.Content != bot.MsgShuttingDown {
				t.Errorf("edited content = %v, want %q", edit.Content, bot.MsgShuttingDown)
			}
			if tt.followup {
				// The pick keeps the search menu and answers ephemerally.
				if edit.Flags&discordgo.MessageFlagsEphemeral == 0 || edit.Components != nil {
					t.Errorf("follow-up flags = %v components = %v, want ephemeral, untouched menu", edit.Flags, edit.Components)
				}
			} else if edit.Components == nil || len(edit.Components) != 0 {
				t.Errorf("components = %v, want empty list", edit.Components)
			}
		})
	}
}
