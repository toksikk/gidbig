package soundboard

import (
	"fmt"
	"log/slog"
	"strings"
	"unicode/utf8"

	"github.com/bwmarrin/discordgo"
)

// maxContentLen is Discord's hard limit for message content, which is what
// /list replies use.
const maxContentLen = 2000

// onInteractionCreate dispatches the /list and /play slash commands.
func (m *Module) onInteractionCreate(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionApplicationCommand {
		return
	}
	switch i.ApplicationCommandData().Name {
	case "list":
		respondEphemeral(s, i, m.buildListMessage())
	case "play":
		if deferInteraction(s, i) {
			go m.onPlayInteraction(s, i)
		}
	}
}

// buildListMessage renders every collection and its sounds, truncating at
// Discord's message-content limit on a UTF-8 boundary.
func (m *Module) buildListMessage() string {
	var b strings.Builder
	for _, c := range m.collections {
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

// onPlayInteraction resolves the requested collection/sound and enqueues it.
func (m *Module) onPlayInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
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
	for _, c := range m.collections {
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
	m.enqueue(user, guild, coll, sound)
	edit(fmt.Sprintf("Queued `%s` from `%s`.", sound.Name, coll.Prefix))
}

// interactionUser extracts the invoking user from an interaction.
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
