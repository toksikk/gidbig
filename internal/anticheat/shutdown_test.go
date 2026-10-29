package anticheat

import (
	"encoding/json"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/toksikk/gidbig/internal/bot"
)

// stoppedRouter returns a router whose bot has shut down, so Router.Go refuses
// new work.
func stoppedRouter(t *testing.T) *bot.Router {
	t.Helper()
	s, err := discordgo.New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	b := bot.New(bot.Deps{Session: s})
	b.Shutdown()
	return b.Router
}

func TestHandlerRepliesShuttingDownAfterShutdown(t *testing.T) {
	m := newTestModule(staticFetch(sampleCSV))
	m.router = stoppedRouter(t)
	s, requests := anticheatDiscordSession(t)
	m.onInteractionCreate(s, anticheatInteraction(discordgo.InteractionApplicationCommand, commandName, ptr("halo")))

	awaitRequest(t, requests) // deferred
	editReq := awaitRequest(t, requests)
	var edit struct {
		Content *string `json:"content"`
	}
	if err := json.Unmarshal(editReq.body, &edit); err != nil {
		t.Fatal(err)
	}
	if edit.Content == nil || *edit.Content != bot.MsgShuttingDown {
		t.Errorf("edited content = %v, want %q", edit.Content, bot.MsgShuttingDown)
	}
}

func ptr(s string) *string { return &s }
