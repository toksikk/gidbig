package gidbig

import (
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

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
			Description: "Play a sound effect (random if sound is omitted)",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "collection",
					Description: "Sound collection to play from",
					Required:    true,
					MaxLength:   100,
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

// maxContentLen is Discord's hard limit for message content, which is what
// /list replies use.
const maxContentLen = 2000

var playEnqueue = func(user *discordgo.User, guild *discordgo.Guild, coll *soundCollection, sound *soundClip) {
	go enqueuePlay(user, guild, coll, sound)
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
	result := b.String()
	if len(result) <= maxContentLen {
		return result
	}

	const marker = "\n...\n(remaining collections omitted)"
	result = result[:maxContentLen-len(marker)]
	for !utf8.ValidString(result) {
		result = result[:len(result)-1]
	}
	return result + marker
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

// onCoreSlashInteractionCreate dispatches /list, /uptime and /play.
func onCoreSlashInteractionCreate(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionApplicationCommand {
		return
	}
	switch i.ApplicationCommandData().Name {
	case "list":
		respondEphemeral(s, i, buildListMessage())
	case "uptime":
		user := interactionUser(i)
		if user == nil || user.ID != conf.Discord.OwnerID {
			respondEphemeral(s, i, "Access denied.")
			return
		}
		respondEphemeral(s, i, buildUptimeBody())
	case "play":
		if deferInteraction(s, i) {
			go onPlayInteraction(s, i, playEnqueue)
		}
	}
}

func onPlayInteraction(s *discordgo.Session, i *discordgo.InteractionCreate, enqueue func(*discordgo.User, *discordgo.Guild, *soundCollection, *soundClip)) {
	edit := func(content string) {
		if _, err := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &content}); err != nil {
			slog.Error("could not edit play response", "error", err)
		}
	}
	if i.GuildID == "" {
		edit("This command can only be used in a server.")
		return
	}
	guild, _ := s.State.Guild(i.GuildID)
	if guild == nil {
		slog.Warn("play: guild not found", "guildID", i.GuildID)
		edit("Could not find this server. Please try again.")
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
		edit("A collection is required.")
		return
	}
	user := interactionUser(i)
	if user == nil {
		edit("Could not identify the requesting user.")
		return
	}

	var coll *soundCollection
	for _, c := range COLLECTIONS {
		if strings.ToLower(c.Prefix) == collection {
			coll = c
			break
		}
	}
	if coll == nil {
		edit(fmt.Sprintf("Unknown collection `%s`.", collection))
		return
	}
	var sound *soundClip
	if soundname != "" {
		sound = coll.Lookup(soundname)
		if sound == nil {
			edit(fmt.Sprintf("Sound `%s` was not found in `%s`.", soundname, coll.Prefix))
			return
		}
	} else {
		sound = coll.Random()
		if sound == nil {
			edit(fmt.Sprintf("Collection `%s` has no sounds.", coll.Prefix))
			return
		}
	}

	slog.Debug("play: enqueuing", "collection", collection, "sound", soundname, "guild", guild.Name)
	enqueue(user, guild, coll, sound)
	edit(fmt.Sprintf("Queued `%s` from `%s`.", sound.Name, coll.Prefix))
}
