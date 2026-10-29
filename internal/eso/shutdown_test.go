package eso

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

func TestHandlerRepliesShuttingDownAfterShutdown(t *testing.T) {
	s, requests := shutdownSession(t)
	b := bot.New(bot.Deps{Session: s})
	b.Shutdown()

	m := New()
	m.router = b.Router
	m.onInteractionCreate(s, &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		ID: "interaction", AppID: "app", Token: "token", Type: discordgo.InteractionApplicationCommand,
		Data: discordgo.ApplicationCommandInteractionData{Name: "eso"},
	}})

	awaitBody(t, requests) // deferred
	var edit struct {
		Content *string `json:"content"`
	}
	if err := json.Unmarshal(awaitBody(t, requests), &edit); err != nil {
		t.Fatal(err)
	}
	if edit.Content == nil || *edit.Content != bot.MsgShuttingDown {
		t.Errorf("edited content = %v, want %q", edit.Content, bot.MsgShuttingDown)
	}
}
