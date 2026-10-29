package gidbig

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/toksikk/gidbig/internal/bot"
)

// coreSlashCommands defines the /uptime slash command.
func coreSlashCommands() []*discordgo.ApplicationCommand {
	return []*discordgo.ApplicationCommand{
		{
			Name:        "uptime",
			Description: "Show bot uptime (owner only)",
		},
	}
}

func (m *Module) buildUptimeBody() string {
	uptime := time.Since(m.startTime).Round(time.Second)
	startDateTime := m.startTime.Format("2006-01-02 15:04:05")
	return fmt.Sprintf("`Uptime: %s (since %s)`", uptime, startDateTime)
}

func respondEphemeral(s *discordgo.Session, i *discordgo.InteractionCreate, content string) {
	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: content,
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	}); err != nil {
		slog.Error("could not respond to slash command", "error", err)
	}
}

func deferInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) bool {
	if err := bot.DeferReply(s, i, true); err != nil {
		slog.Error("could not respond to slash command", "error", err)
		return false
	}
	return true
}
