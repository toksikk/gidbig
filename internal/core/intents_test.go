package gidbig

import (
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/toksikk/gidbig/internal/cfg"
)

func TestNewDiscordSession_RequestsMessageContentIntent(t *testing.T) {
	conf := &cfg.Config{}
	conf.Discord.Token = "test"
	s, err := newDiscordSession(conf)
	if err != nil {
		t.Fatalf("newDiscordSession: %v", err)
	}
	if s.Identify.Intents&discordgo.IntentMessageContent == 0 {
		t.Error("Message Content intent not requested")
	}
	if s.Identify.Intents&discordgo.IntentGuildMessageReactions == 0 {
		t.Error("guild message reactions intent not requested")
	}
}
