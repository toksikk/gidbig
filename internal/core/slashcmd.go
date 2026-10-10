package gidbig

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/bwmarrin/discordgo"
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

func buildUptimeBody() string {
	uptime := time.Since(startTime).Round(time.Second)
	startDateTime := startTime.Format("2006-01-02 15:04:05")
	return fmt.Sprintf("`Uptime: %s (since %s)`", uptime, startDateTime)
}

func interactionUser(i *discordgo.InteractionCreate) *discordgo.User {
	if i.Member != nil && i.Member.User != nil {
		return i.Member.User
	}
	return i.User
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
	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Flags: discordgo.MessageFlagsEphemeral},
	}); err != nil {
		slog.Error("could not respond to slash command", "error", err)
		return false
	}
	return true
}

// onCoreSlashInteractionCreate dispatches the /uptime command.
func onCoreSlashInteractionCreate(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionApplicationCommand {
		return
	}
	if i.ApplicationCommandData().Name != "uptime" {
		return
	}
	user := interactionUser(i)
	if user == nil || user.ID != conf.Discord.OwnerID {
		respondEphemeral(s, i, "Access denied.")
		return
	}
	respondEphemeral(s, i, buildUptimeBody())
}
