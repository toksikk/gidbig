package tv

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/toksikk/gidbig/internal/util"
)

const (
	commandName = "tv"
	// maxChoices is Discord's autocomplete choice limit.
	maxChoices  = 25
	maxQueryLen = 100
)

// fromChoices are the start times offered by /tv channel; "now" is the
// default and means the current time (or prime time on another day).
var fromChoices = []string{"now", "06:00", "12:00", "18:00", "20:15", "22:00"}

// primeTime is the start used for from:now on a day other than today.
const primeTime = "20:15"

// command defines /tv with the now, channel and search subcommands.
func command() *discordgo.ApplicationCommand {
	channelOpt := func(required bool) *discordgo.ApplicationCommandOption {
		return &discordgo.ApplicationCommandOption{
			Type:         discordgo.ApplicationCommandOptionString,
			Name:         "channel",
			Description:  "Channel, e.g. ARD, ZDF, ProSieben",
			Required:     required,
			Autocomplete: true,
			MaxLength:    maxQueryLen,
		}
	}
	hoursOpt := &discordgo.ApplicationCommandOption{
		Type:        discordgo.ApplicationCommandOptionInteger,
		Name:        "hours",
		Description: "Time span to list (default 6 hours)",
		Choices: []*discordgo.ApplicationCommandOptionChoice{
			{Name: "1 hour", Value: 1},
			{Name: "3 hours", Value: 3},
			{Name: "6 hours", Value: 6},
		},
	}
	publicOpt := &discordgo.ApplicationCommandOption{
		Type:        discordgo.ApplicationCommandOptionBoolean,
		Name:        "public",
		Description: "Post the answer to the channel instead of only to you",
	}
	dayChoices := []*discordgo.ApplicationCommandOptionChoice{
		{Name: "today", Value: 0},
		{Name: "tomorrow", Value: 1},
	}
	for d := 2; d <= maxDayOffset; d++ {
		dayChoices = append(dayChoices, &discordgo.ApplicationCommandOptionChoice{Name: fmt.Sprintf("in %d days", d), Value: d})
	}
	timeChoices := make([]*discordgo.ApplicationCommandOptionChoice, len(fromChoices))
	for i, f := range fromChoices {
		name := f
		if f == primeTime {
			name = f + " (prime time)"
		}
		timeChoices[i] = &discordgo.ApplicationCommandOptionChoice{Name: name, Value: f}
	}
	return &discordgo.ApplicationCommand{
		Name:        commandName,
		Description: "TV program",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "now",
				Description: "What is on now; with a channel, the next hours",
				Options:     []*discordgo.ApplicationCommandOption{channelOpt(false), hoursOpt, publicOpt},
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "channel",
				Description: "A channel's program for up to 6 hours",
				Options: []*discordgo.ApplicationCommandOption{
					channelOpt(true),
					{
						Type:        discordgo.ApplicationCommandOptionInteger,
						Name:        "day",
						Description: "Day (default today)",
						Choices:     dayChoices,
					},
					{
						Type:        discordgo.ApplicationCommandOptionString,
						Name:        "from",
						Description: "Start time (default now; prime time on other days)",
						Choices:     timeChoices,
					},
					hoursOpt,
					publicOpt,
				},
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "search",
				Description: "Find upcoming shows by title",
				Options: []*discordgo.ApplicationCommandOption{
					{
						Type:        discordgo.ApplicationCommandOptionString,
						Name:        "title",
						Description: "Title or part of it",
						Required:    true,
						MaxLength:   maxQueryLen,
					},
					publicOpt,
				},
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
		content, public := m.answer(i.ApplicationCommandData())
		resp := &discordgo.InteractionResponseData{Content: content, AllowedMentions: util.NoMentions()}
		if public {
			resp.Flags = discordgo.MessageFlagsSuppressNotifications
		} else {
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

// cmdOptions holds the parsed options of one /tv subcommand.
type cmdOptions struct {
	str    map[string]string
	num    map[string]int
	public bool
}

func readOptions(opts []*discordgo.ApplicationCommandInteractionDataOption) cmdOptions {
	o := cmdOptions{str: map[string]string{}, num: map[string]int{}}
	for _, opt := range opts {
		switch opt.Type {
		case discordgo.ApplicationCommandOptionString:
			o.str[opt.Name] = strings.TrimSpace(opt.StringValue())
		case discordgo.ApplicationCommandOptionInteger:
			o.num[opt.Name] = int(opt.IntValue())
		case discordgo.ApplicationCommandOptionBoolean:
			if opt.Name == "public" {
				o.public = opt.BoolValue()
			}
		}
	}
	return o
}

// hours returns the requested window, clamped to 1..maxWindowHours.
func (o cmdOptions) hours() int {
	h, ok := o.num["hours"]
	if !ok {
		return maxWindowHours
	}
	return max(1, min(maxWindowHours, h))
}

// answer renders the reply for one /tv invocation and whether it is public.
// Listings are private unless public:true was given; errors and the
// not-ready notice are always private.
func (m *Module) answer(data discordgo.ApplicationCommandInteractionData) (string, bool) {
	ix := m.index()
	if ix == nil {
		return msgNotReady, false
	}
	if len(data.Options) == 0 {
		return "Unknown subcommand.", false
	}
	sub := data.Options[0]
	o := readOptions(sub.Options)
	now := m.now()
	switch sub.Name {
	case "now":
		if o.str["channel"] == "" {
			return discordNowAll(ix, now), o.public
		}
		ch, ok := ix.resolveChannel(o.str["channel"])
		if !ok {
			return unknownChannel(ix, o.str["channel"]), false
		}
		return discordWindow(ix, ch, now, o.hours()), o.public
	case "channel":
		ch, ok := ix.resolveChannel(o.str["channel"])
		if !ok {
			return unknownChannel(ix, o.str["channel"]), false
		}
		from, ok := windowStart(now, ix.loc, o.num["day"], o.str["from"])
		if !ok {
			return msgBadSlashDay, false
		}
		return discordWindow(ix, ch, from, o.hours()), o.public
	case "search":
		if o.str["title"] == "" {
			return "Please give a title.", false
		}
		return discordSearch(ix, o.str["title"], now), o.public
	}
	return "Unknown subcommand.", false
}

// windowStart resolves the day offset and start choice to a time. from "now"
// (or empty) is the current time today and primeTime on any other day.
func windowStart(now time.Time, loc *time.Location, day int, from string) (time.Time, bool) {
	if day < 0 || day > maxDayOffset {
		return time.Time{}, false
	}
	if from == "" || from == "now" {
		if day == 0 {
			return now, true
		}
		from = primeTime
	}
	hm, err := time.Parse("15:04", from)
	if err != nil {
		return time.Time{}, false
	}
	y, mo, d := now.In(loc).Date()
	return time.Date(y, mo, d+day, hm.Hour(), hm.Minute(), 0, 0, loc), true
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
	msg := "Unknown channel “" + sanitize(name) + "”."
	if sug := ix.suggest(name, 5); len(sug) > 0 {
		names := make([]string, len(sug))
		for i, ch := range sug {
			names[i] = ix.channels[ch].Name
		}
		msg += " Did you mean: " + strings.Join(names, ", ") + "?"
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
