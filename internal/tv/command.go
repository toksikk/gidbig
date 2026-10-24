package tv

import (
	"log/slog"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/toksikk/gidbig/internal/util"
)

const (
	commandName = "tv"
	// maxChoices is Discord's autocomplete choice limit.
	maxChoices  = 25
	maxQueryLen = 100
)

// command defines /tv with the now, channel and search subcommands.
func command() *discordgo.ApplicationCommand {
	channelOpt := func(required bool) *discordgo.ApplicationCommandOption {
		return &discordgo.ApplicationCommandOption{
			Type:         discordgo.ApplicationCommandOptionString,
			Name:         "channel",
			Description:  "Sender, z.B. ARD, ZDF, ProSieben",
			Required:     required,
			Autocomplete: true,
			MaxLength:    maxQueryLen,
		}
	}
	return &discordgo.ApplicationCommand{
		Name:        commandName,
		Description: "Deutsches TV-Programm",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "now",
				Description: "Was läuft gerade und danach",
				Options:     []*discordgo.ApplicationCommandOption{channelOpt(false)},
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "channel",
				Description: "Tagesprogramm eines Senders",
				Options: []*discordgo.ApplicationCommandOption{
					channelOpt(true),
					{
						Type:        discordgo.ApplicationCommandOptionString,
						Name:        "day",
						Description: "heute, morgen, übermorgen oder JJJJ-MM-TT",
						MaxLength:   20,
					},
				},
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "search",
				Description: "Sendung nach Titel suchen",
				Options: []*discordgo.ApplicationCommandOption{{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "title",
					Description: "Titel oder Teil davon",
					Required:    true,
					MaxLength:   maxQueryLen,
				}},
			},
		},
	}
}

func (m *Module) onInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.ApplicationCommandData().Name != commandName {
		return
	}
	switch i.Type {
	case discordgo.InteractionApplicationCommandAutocomplete:
		m.onAutocomplete(s, i)
	case discordgo.InteractionApplicationCommand:
		content, ephemeral := m.answer(i.ApplicationCommandData())
		resp := &discordgo.InteractionResponseData{Content: content, AllowedMentions: util.NoMentions()}
		if ephemeral {
			resp.Flags = discordgo.MessageFlagsEphemeral
		}
		if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: resp,
		}); err != nil {
			slog.Error("tv: failed to respond to interaction", "ChannelID", i.ChannelID, "error", err)
		}
	}
}

// answer renders the reply for one /tv invocation. Errors and the not-ready
// notice are ephemeral; programme listings are public.
func (m *Module) answer(data discordgo.ApplicationCommandInteractionData) (string, bool) {
	ix := m.index()
	if ix == nil {
		return msgNotReady, true
	}
	if len(data.Options) == 0 {
		return "Unbekannter Unterbefehl.", true
	}
	sub := data.Options[0]
	opts := map[string]string{}
	for _, o := range sub.Options {
		if o.Type == discordgo.ApplicationCommandOptionString {
			opts[o.Name] = strings.TrimSpace(o.StringValue())
		}
	}
	now := m.now()
	switch sub.Name {
	case "now":
		chs, msg := resolveOrAll(ix, opts["channel"])
		if msg != "" {
			return msg, true
		}
		return renderNow(ix, chs, now, false), false
	case "channel":
		ch, ok := ix.resolveChannel(opts["channel"])
		if !ok {
			return unknownChannel(ix, opts["channel"]), true
		}
		day, ok := parseDay(opts["day"], now)
		if !ok {
			return msgBadDay, true
		}
		return renderDay(ix, ch, day, false), false
	case "search":
		if opts["title"] == "" {
			return "Bitte einen Titel angeben.", true
		}
		return renderSearch(ix, opts["title"], now, searchLimit, false), false
	}
	return "Unbekannter Unterbefehl.", true
}

// resolveOrAll resolves one channel, or returns every channel for empty input.
// msg is a user-facing error when the name is unknown.
func resolveOrAll(ix *index, name string) ([]int, string) {
	if name == "" {
		all := make([]int, len(ix.channels))
		for i := range all {
			all[i] = i
		}
		return all, ""
	}
	ch, ok := ix.resolveChannel(name)
	if !ok {
		return nil, unknownChannel(ix, name)
	}
	return []int{ch}, ""
}

// unknownChannel explains an unresolved channel and suggests close matches.
func unknownChannel(ix *index, name string) string {
	msg := "Sender unbekannt: „" + sanitize(name) + "“."
	if sug := ix.suggest(name, 5); len(sug) > 0 {
		names := make([]string, len(sug))
		for i, ch := range sug {
			names[i] = ix.channels[ch].Name
		}
		msg += " Meintest du: " + strings.Join(names, ", ") + "?"
	}
	return msg
}

// onAutocomplete suggests channel names for the focused channel option.
func (m *Module) onAutocomplete(s *discordgo.Session, i *discordgo.InteractionCreate) {
	choices := m.channelChoices(i.ApplicationCommandData())
	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionApplicationCommandAutocompleteResult,
		Data: &discordgo.InteractionResponseData{Choices: choices},
	}); err != nil {
		slog.Error("tv: failed to answer autocomplete", "error", err)
	}
}

func (m *Module) channelChoices(data discordgo.ApplicationCommandInteractionData) []*discordgo.ApplicationCommandOptionChoice {
	choices := []*discordgo.ApplicationCommandOptionChoice{}
	ix := m.index()
	if ix == nil || len(data.Options) == 0 {
		return choices
	}
	input := ""
	for _, o := range data.Options[0].Options {
		if o.Focused && o.Name == "channel" {
			input = o.StringValue()
		}
	}
	for _, ch := range ix.suggest(input, maxChoices) {
		c := ix.channels[ch]
		choices = append(choices, &discordgo.ApplicationCommandOptionChoice{Name: c.Name, Value: c.ID})
	}
	return choices
}
