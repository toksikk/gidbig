package gidbig

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
)

// coreSlashCommands defines the /list, /uptime and /play slash commands.
func coreSlashCommands() []*discordgo.ApplicationCommand {
	return []*discordgo.ApplicationCommand{
		{
			Name:        "list",
			Description: "List all available sound effects",
		},
		{
			Name:        "uptime",
			Description: "Show bot uptime (owner only)",
		},
		{
			Name:        "play",
			Description: "Play a sound effect (random from collection if sound omitted)",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "collection",
					Description: "Sound collection to play from",
					Required:    true,
					Choices:     playCollectionChoices(),
				},
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "sound",
					Description: "Specific sound effect name (optional)",
					Required:    false,
					MaxLength:   100,
				},
			},
		},
	}
}

// playCollectionChoices builds one choice per collection prefix, capped at 25.
func playCollectionChoices() []*discordgo.ApplicationCommandOptionChoice {
	const maxChoices = 25
	limit := len(COLLECTIONS)
	if limit > maxChoices {
		limit = maxChoices
	}
	choices := make([]*discordgo.ApplicationCommandOptionChoice, 0, limit)
	for _, c := range COLLECTIONS[:limit] {
		name := strings.ToLower(c.Prefix)
		choices = append(choices, &discordgo.ApplicationCommandOptionChoice{
			Name:  name,
			Value: c.Prefix,
		})
	}
	return choices
}

func buildListMessage() string {
	var b strings.Builder
	for _, c := range COLLECTIONS {
		b.WriteString("**" + c.Prefix + "**\n")
		for _, s := range c.Sounds {
			b.WriteString(s.Name + "\n")
		}
		b.WriteString("\n")
	}
	return b.String()
}

func buildUptimeBody() string {
	uptime := time.Since(startTime).Round(time.Second)
	startDateTime := startTime.Format("2006-01-02 15:04:05")
	return fmt.Sprintf("`Uptime: %s (since %s)`", uptime, startDateTime)
}

func interactionUserID(i *discordgo.InteractionCreate) string {
	if i.Member != nil && i.Member.User != nil {
		return i.Member.User.ID
	}
	if i.User != nil {
		return i.User.ID
	}
	return ""
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

// deferInteraction acknowledges with a deferred response. The response
// stays empty (silent success) or gets edited later for error content.
func deferInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) bool {
	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	}); err != nil {
		slog.Error("could not respond to slash command", "error", err)
		return false
	}
	return true
}

// onCoreSlashInteractionCreate dispatches /list, /uptime and /play.
func onCoreSlashInteractionCreate(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionApplicationCommand {
		return
	}
	switch i.ApplicationCommandData().Name {
	case "list":
		respondEphemeral(s, i, buildListMessage())
	case "uptime":
		if interactionUserID(i) != conf.Discord.OwnerID {
			respondEphemeral(s, i, "Access denied.")
			return
		}
		respondEphemeral(s, i, buildUptimeBody())
	case "play":
		go onPlayInteraction(s, i)
	}
}

func onPlayInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.GuildID == "" {
		return
	}
	if !deferInteraction(s, i) {
		return
	}
	guild, _ := s.State.Guild(i.GuildID)
	if guild == nil {
		slog.Warn("play: guild not found", "guildID", i.GuildID)
		return
	}

	collection := ""
	soundname := ""
	if opt := i.ApplicationCommandData().GetOption("collection"); opt != nil {
		if v, ok := opt.Value.(string); ok {
			collection = strings.ToLower(v)
		}
	}
	if opt := i.ApplicationCommandData().GetOption("sound"); opt != nil {
		if v, ok := opt.Value.(string); ok {
			soundname = strings.ToLower(v)
		}
	}

	if collection == "" {
		return
	}
	userID := interactionUserID(i)
	if userID == "" {
		return
	}
	user, err := s.User(userID)
	if err != nil {
		slog.Warn("play: could not fetch user", "userID", userID, "error", err)
		return
	}

	sound, coll := findSoundAndCollection("!"+collection, soundname)
	// Collection must exist; an unknown explicit sound stays silent (legacy !play behavior).
	if coll == nil {
		return
	}
	if soundname != "" && sound == nil {
		return
	}

	slog.Debug("play: enqueuing", "collection", collection, "sound", soundname, "guild", guild.Name)
	go enqueuePlay(user, guild, coll, sound)
}
