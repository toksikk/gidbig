// Package logscmd exposes the owner-only /admin logs show command, which reads
// the in-memory log window kept by internal/logwindow.
//
// The module is an AdminProvider only: the /admin OwnerOnly middleware is the
// access control and the admin module defers every reply ephemerally.
package logscmd

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/toksikk/gidbig/internal/bot"
	"github.com/toksikk/gidbig/internal/logwindow"
)

const (
	groupName = "logs"
	// DefaultCount is how many lines /admin logs show returns without count.
	DefaultCount = 50
	// MaxCount is the largest accepted count option.
	MaxCount = 200
	// MaxMessages caps the reply at this many Discord messages.
	MaxMessages = 2
	// maxMessageRunes is the per-message budget Render fills, code block
	// included. The slack below Discord's 2000 character limit covers the
	// omission note and its newline on the first message.
	maxMessageRunes = 1900
	// fence wraps each message so log text renders verbatim.
	fence = "```\n"
	// EmptyMessage is the reply when nothing matches.
	EmptyMessage = "No matching log lines."
)

// Reader is the slice of the log window the command uses.
type Reader interface {
	Lines(q logwindow.Query) []string
}

// Module implements bot.Module and bot.AdminProvider.
type Module struct {
	window Reader
}

// New returns a Module reading from window; a nil window answers every query
// with EmptyMessage.
func New(window Reader) *Module { return &Module{window: window} }

// Name returns the module's identifier.
func (m *Module) Name() string { return "logscmd" }

// Init registers nothing: the group is reached through /admin.
func (m *Module) Init(bot.Deps) error {
	slog.Info("logscmd: initialized")
	return nil
}

// Background returns no background tasks.
func (m *Module) Background() []bot.BackgroundTask { return nil }

// Shutdown is a no-op.
func (m *Module) Shutdown() error { return nil }

// AdminSubcommandGroup returns the /admin logs group definition.
func (m *Module) AdminSubcommandGroup() *discordgo.ApplicationCommandOption {
	minCount := 1.0
	return &discordgo.ApplicationCommandOption{
		Type:        discordgo.ApplicationCommandOptionSubCommandGroup,
		Name:        groupName,
		Description: "Recent log lines from memory",
		Options: []*discordgo.ApplicationCommandOption{{
			Type:        discordgo.ApplicationCommandOptionSubCommand,
			Name:        "show",
			Description: "Show the newest log lines (full window at /admin/logs)",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "level",
					Description: "Lowest level shown (default info)",
					Choices: []*discordgo.ApplicationCommandOptionChoice{
						{Name: "debug", Value: "debug"},
						{Name: "info", Value: "info"},
						{Name: "warn", Value: "warn"},
						{Name: "error", Value: "error"},
					},
				},
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "filter",
					Description: "Case-insensitive text the line must contain",
				},
				{
					Type:        discordgo.ApplicationCommandOptionInteger,
					Name:        "count",
					Description: fmt.Sprintf("Newest lines to show (default %d)", DefaultCount),
					MinValue:    &minCount,
					MaxValue:    MaxCount,
				},
			},
		}},
	}
}

// QueryFromOptions builds the query for a show subcommand's options.
func QueryFromOptions(options []*discordgo.ApplicationCommandInteractionDataOption) logwindow.Query {
	q := logwindow.Query{MinLevel: slog.LevelInfo, Count: DefaultCount}
	for _, o := range options {
		switch o.Name {
		case "level":
			if l, ok := logwindow.ParseLevel(o.StringValue()); ok {
				q.MinLevel = l
			}
		case "filter":
			q.Filter = o.StringValue()
		case "count":
			if c := int(o.IntValue()); c >= 1 && c <= MaxCount {
				q.Count = c
			}
		}
	}
	return q
}

// HandleAdminSubcommand answers /admin logs show. The interaction is already
// deferred ephemerally by the admin module.
func (m *Module) HandleAdminSubcommand(s *discordgo.Session, i *discordgo.InteractionCreate, sub *discordgo.ApplicationCommandInteractionDataOption) {
	m.handle(s, i, sub)
}

// handle is HandleAdminSubcommand over the narrow responder, so it is testable
// without a Discord session.
func (m *Module) handle(s responder, i *discordgo.InteractionCreate, sub *discordgo.ApplicationCommandInteractionDataOption) {
	if sub.Name != "show" {
		return
	}
	var lines []string
	if m.window != nil {
		lines = m.window.Lines(QueryFromOptions(sub.Options))
	}
	send(s, i.Interaction, Render(lines, MaxMessages, maxMessageRunes))
}

// responder is the slice of *discordgo.Session the reply needs.
type responder interface {
	InteractionResponseEdit(interaction *discordgo.Interaction, newresp *discordgo.WebhookEdit, options ...discordgo.RequestOption) (*discordgo.Message, error)
	FollowupMessageCreate(interaction *discordgo.Interaction, wait bool, data *discordgo.WebhookParams, options ...discordgo.RequestOption) (*discordgo.Message, error)
}

// send edits the deferred response with the first message and posts at most
// MaxMessages-1 ephemeral follow-ups. It stops at the first error.
func send(s responder, interaction *discordgo.Interaction, msgs []string) {
	if len(msgs) > MaxMessages {
		msgs = msgs[:MaxMessages]
	}
	if _, err := s.InteractionResponseEdit(interaction, &discordgo.WebhookEdit{Content: &msgs[0]}); err != nil {
		slog.Warn("logscmd: failed to edit interaction response", "error", err)
		return
	}
	for _, extra := range msgs[1:] {
		if _, err := s.FollowupMessageCreate(interaction, true, &discordgo.WebhookParams{
			Content: extra,
			Flags:   discordgo.MessageFlagsEphemeral,
		}); err != nil {
			slog.Warn("logscmd: failed to send follow-up", "error", err)
			return
		}
	}
}

// Render packs the newest lines into at most maxMessages fenced messages of at
// most limit runes each. When older lines do not fit, the first message starts
// with a note naming how many were omitted. Lines are joined with "\n", so the
// text inside the fences is exactly what the web download shows for the same
// lines. A single line longer than a message is truncated.
func Render(lines []string, maxMessages, limit int) []string {
	if len(lines) == 0 {
		return []string{EmptyMessage}
	}
	body := limit - runeLen(fence) - runeLen("```")
	// The oldest message may carry the omission note; reserve its widest form.
	reserve := runeLen(omittedNote(len(lines))) + 1
	// Fill messages from the newest line backwards; groups[0] is the newest.
	var groups [][]string
	var cur []string
	curLen := 0
	idx := len(lines) - 1
	for ; idx >= 0; idx-- {
		l := sanitize(lines[idx])
		if r := []rune(l); len(r) > body-reserve {
			l = string(r[:body-reserve-1]) + "…"
		}
		n := runeLen(l)
		if len(cur) > 0 {
			n++
		}
		room := body
		if len(groups) == maxMessages-1 {
			room -= reserve
		}
		if curLen+n > room {
			if len(groups) == maxMessages-1 {
				break
			}
			groups = append(groups, cur)
			cur, curLen, n = nil, 0, runeLen(l)
		}
		cur = append([]string{l}, cur...)
		curLen += n
	}
	if len(cur) > 0 {
		groups = append(groups, cur)
	}
	omitted := idx + 1
	out := make([]string, 0, len(groups))
	for gi := len(groups) - 1; gi >= 0; gi-- {
		msg := fence + strings.Join(groups[gi], "\n") + "\n```"
		if gi == len(groups)-1 && omitted > 0 {
			msg = omittedNote(omitted) + "\n" + msg
		}
		out = append(out, msg)
	}
	return out
}

func omittedNote(n int) string {
	return fmt.Sprintf("… %d older lines omitted — see /admin/logs", n)
}

// sanitize keeps a log line from closing the code fence.
func sanitize(l string) string {
	return strings.ReplaceAll(l, "```", "`\u200b``")
}

func runeLen(s string) int { return len([]rune(s)) }
