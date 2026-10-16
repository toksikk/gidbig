// Package logs exposes the bot's own recent log lines to the owner, through the
// /admin logs slash command and the owner-only web page /admin/logs. Both
// surfaces read the same in-memory window (internal/logbuf) and render the same
// text, so the command and the page cannot disagree about what the bot logged.
//
// The module is an AdminProvider only: the group lives under /admin, whose
// OwnerOnly middleware is the access control, and the admin module defers the
// interaction ephemerally. Keeping it a provider rather than a second top-level
// command also keeps the global slash command names unique.
package logs

import (
	"fmt"
	"log/slog"
	"strings"
	"unicode/utf8"

	"github.com/bwmarrin/discordgo"
	"github.com/toksikk/gidbig/internal/bot"
	"github.com/toksikk/gidbig/internal/logbuf"
	"github.com/toksikk/gidbig/internal/util"
)

const (
	// groupName is the /admin subcommand group this module owns.
	groupName = "logs"
	// DefaultShowLines is the window a caller gets without asking for a size.
	DefaultShowLines = 50
	// MaxShowLines bounds one window. The command's option and the panel's count
	// parameter share it, so neither surface can ask for more than the other.
	MaxShowLines = 100
	// MaxFilterRunes bounds the substring filter; a longer one is cut, so a
	// crafted request cannot turn into a megabyte of scanning.
	MaxFilterRunes = 100
	// discordContentRunes keeps one reply under Discord's 2000-rune content
	// limit, with room for the fenced block around it.
	discordContentRunes = 1900
	// noLinesMessage is what both surfaces show for a window that matched
	// nothing, so an empty window is never mistaken for a broken one.
	noLinesMessage = "no captured log lines matched this query"
	// emptyBufferMessage is the header for a window over an empty buffer.
	emptyBufferMessage = "the log buffer is empty"
	// UnavailableMessage is the state both surfaces report when no buffer is
	// wired. It lives here so the command and the panel cannot word it
	// differently.
	UnavailableMessage = "Log lines are unavailable: no log buffer is wired."
)

// AllLevels is the floor that keeps every captured line. Nothing is captured
// below debug, so it is the widest window a Query can ask for; it is also what
// an omitted level means on either surface.
const AllLevels = slog.LevelDebug

// LevelOption is one selectable level floor. The Discord choices and the panel's
// select are both built from these, so the surfaces offer the same set.
type LevelOption struct {
	// Value is what a caller passes ("all", "debug", "info", "warn", "error").
	Value string
	// Label is what the surface shows for it.
	Label string
	// Level is the floor the option selects.
	Level slog.Level
}

// levelOptions are the floors both surfaces offer. Callers must not modify them.
var levelOptions = []LevelOption{
	{Value: "all", Label: "All levels", Level: AllLevels},
	{Value: "debug", Label: "Debug and above", Level: slog.LevelDebug},
	{Value: "info", Label: "Info and above", Level: slog.LevelInfo},
	{Value: "warn", Label: "Warn and above", Level: slog.LevelWarn},
	{Value: "error", Label: "Errors only", Level: slog.LevelError},
}

// LevelOptions returns the selectable level floors, widest first.
func LevelOptions() []LevelOption { return levelOptions }

// ParseLevel resolves a level name to its option. An empty name and "all" mean
// every level; an unknown name is an error, which both surfaces report instead
// of silently showing a window the caller did not ask for.
func ParseLevel(value string) (LevelOption, error) {
	name := strings.ToLower(strings.TrimSpace(value))
	if name == "" {
		return levelOptions[0], nil
	}
	for _, o := range levelOptions {
		if o.Value == name {
			return o, nil
		}
	}
	return LevelOption{}, fmt.Errorf("unknown log level %q", value)
}

// Query selects a window of log lines. The command fills it from its options and
// the panel from its query parameters; Window normalises it either way.
type Query struct {
	// MinLevel drops lines below it. AllLevels shows everything captured; the
	// zero value is slog.LevelInfo, so a caller that wants the whole window has
	// to say so explicitly, which both surfaces do.
	MinLevel slog.Level
	// Filter keeps lines containing it, ignoring case. Empty keeps all lines.
	Filter string
	// Count caps the window. Below one means DefaultShowLines, above MaxShowLines
	// is clamped to it.
	Count int
}

// normalize applies the window's bounds, so a caller cannot ask for a window
// larger than the buffer bound or a filter longer than the cap.
func (q Query) normalize() Query {
	if q.Count < 1 {
		q.Count = DefaultShowLines
	}
	if q.Count > MaxShowLines {
		q.Count = MaxShowLines
	}
	q.Filter = truncateFilter(strings.TrimSpace(q.Filter))
	return q
}

// Window is one answer to a Query: the selected lines plus the context both
// surfaces show around them.
type Window struct {
	// Query is the normalised query the window answers.
	Query Query
	// Lines are the selected lines, oldest first.
	Lines []logbuf.Line
	// Held is how many lines the buffer held when the window was taken and
	// Evicted how many were dropped since the process started.
	Held    int
	Evicted uint64
	// Dropped counts matching lines the window left out to fit one Discord
	// message. The panel shows the whole window, so it leaves this at zero.
	Dropped int
	// Unavailable is set when no buffer is wired: the window is then missing
	// rather than empty, and both surfaces say so.
	Unavailable bool
}

// Header describes the window in one line: how many of the held lines it shows,
// the floor and filter in force, and what was left out. It is a plain line of
// text on both surfaces.
func (w Window) Header() string {
	if w.Unavailable {
		return UnavailableMessage
	}
	if w.Held == 0 {
		return emptyBufferMessage
	}
	parts := []string{fmt.Sprintf("%d of %d held lines", len(w.Lines)+w.Dropped, w.Held)}
	if w.Query.MinLevel > AllLevels {
		parts = append(parts, "from "+logbuf.LevelLabel(w.Query.MinLevel)+" up")
	}
	if w.Query.Filter != "" {
		parts = append(parts, fmt.Sprintf("filter %q", w.Query.Filter))
	}
	if w.Dropped > 0 {
		parts = append(parts, fmt.Sprintf("%d matched lines dropped, newest kept", w.Dropped))
	}
	if w.Evicted > 0 {
		parts = append(parts, fmt.Sprintf("%d older lines evicted", w.Evicted))
	}
	return strings.Join(parts, " · ")
}

// Text renders the selected lines, oldest first and one per line. It is the body
// both surfaces show; a window that matched nothing says so instead of rendering
// as an empty block.
func (w Window) Text() string {
	if w.Unavailable {
		return UnavailableMessage
	}
	if len(w.Lines) == 0 {
		return noLinesMessage
	}
	var sb strings.Builder
	for i, line := range w.Lines {
		if i > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(line.String())
	}
	return sb.String()
}

// Module implements bot.Module and bot.AdminProvider.
type Module struct {
	buf *logbuf.Buffer
}

// New returns a Module reading buf. A nil buffer keeps every answer at
// UnavailableMessage instead of panicking.
func New(buf *logbuf.Buffer) *Module { return &Module{buf: buf} }

// Name returns the module's identifier.
func (m *Module) Name() string { return "logs" }

// Init registers no handler of its own: the group is reached through /admin.
func (m *Module) Init(_ bot.Deps) error {
	slog.Info("logs: initialized", "capacity", m.Capacity(), "available", m.buf != nil)
	return nil
}

// Background returns no background tasks for this module.
func (m *Module) Background() []bot.BackgroundTask { return nil }

// Shutdown is a no-op: the module owns no resources.
func (m *Module) Shutdown() error { return nil }

// Capacity is how many lines the buffer holds, or zero without one.
func (m *Module) Capacity() int {
	if m.buf == nil {
		return 0
	}
	return m.buf.Capacity()
}

// AdminSubcommandGroup returns the /admin logs subcommand group definition.
func (m *Module) AdminSubcommandGroup() *discordgo.ApplicationCommandOption {
	choices := make([]*discordgo.ApplicationCommandOptionChoice, 0, len(levelOptions))
	for _, o := range levelOptions {
		choices = append(choices, &discordgo.ApplicationCommandOptionChoice{
			Name:  o.Label,
			Value: o.Value,
		})
	}
	minCount := float64(1)
	return &discordgo.ApplicationCommandOption{
		Type:        discordgo.ApplicationCommandOptionSubCommandGroup,
		Name:        groupName,
		Description: "Read the bot's own recent log lines",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "show",
				Description: "Show the newest matching log lines from the in-memory window",
				Options: []*discordgo.ApplicationCommandOption{
					{
						Type:        discordgo.ApplicationCommandOptionString,
						Name:        "level",
						Description: "Lowest level to show (default: all levels)",
						Required:    false,
						Choices:     choices,
					},
					{
						Type:        discordgo.ApplicationCommandOptionInteger,
						Name:        "count",
						Description: fmt.Sprintf("How many lines to show, newest first (default %d)", DefaultShowLines),
						Required:    false,
						MinValue:    &minCount,
						MaxValue:    MaxShowLines,
					},
					{
						Type:        discordgo.ApplicationCommandOptionString,
						Name:        "filter",
						Description: "Only lines containing this text, case-insensitive",
						Required:    false,
						MaxLength:   MaxFilterRunes,
					},
				},
			},
		},
	}
}

// HandleAdminSubcommand handles /admin logs show. The interaction is already
// deferred ephemerally by the admin module, so the reply edits it.
func (m *Module) HandleAdminSubcommand(s *discordgo.Session, i *discordgo.InteractionCreate, sub *discordgo.ApplicationCommandInteractionDataOption) {
	if sub.Name != "show" {
		return
	}
	q := Query{MinLevel: AllLevels, Count: DefaultShowLines}
	if level := optionString(sub.Options, "level"); level != "" {
		if parsed, err := ParseLevel(level); err == nil {
			q.MinLevel = parsed.Level
		}
	}
	q.Filter = optionString(sub.Options, "filter")
	if count := optionInt(sub.Options, "count"); count > 0 {
		q.Count = int(count)
	}
	reply(s, i, m.DiscordContent(q))
}

// Window returns the newest matching lines with the context around them. The
// panel renders this directly; DiscordContent trims it to one message.
func (m *Module) Window(q Query) Window {
	q = q.normalize()
	if m.buf == nil {
		return Window{Query: q, Unavailable: true}
	}

	held := m.buf.Snapshot()
	matched := make([]logbuf.Line, 0, len(held))
	for _, line := range held {
		if line.Level < q.MinLevel {
			continue
		}
		if !matchesFilter(line, q.Filter) {
			continue
		}
		matched = append(matched, line)
	}
	// The newest lines are the ones an operator is after: a full window keeps
	// the tail, never the head.
	if len(matched) > q.Count {
		matched = matched[len(matched)-q.Count:]
	}

	return Window{
		Query:   q,
		Lines:   matched,
		Held:    len(held),
		Evicted: m.buf.Evicted(),
	}
}

// DiscordContent renders the window as one fenced message. The oldest matching
// lines are dropped until the content fits, so a wide window never becomes a
// trail of follow-up messages; the header then reports how many were left out.
func (m *Module) DiscordContent(q Query) string {
	w := m.Window(q)
	if w.Unavailable {
		return UnavailableMessage
	}
	content := fence(w)
	// logbuf caps a rendered line well below the content limit, so the newest
	// line alone always fits and the loop cannot empty the window.
	for len(w.Lines) > 1 && utf8.RuneCountInString(content) > discordContentRunes {
		w.Lines = w.Lines[1:]
		w.Dropped++
		content = fence(w)
	}
	return content
}

// fence wraps a window's header and lines in one code block and defuses it, so a
// log line cannot turn into Discord markup. Backticks are replaced rather than
// escaped: a stray one would close the block and leave the rest of the log
// outside it. The caller suppresses mentions through allowed_mentions.
func fence(w Window) string {
	return "```\n" + defuse(w.Header()) + "\n" + defuse(w.Text()) + "\n```"
}

// defuse replaces backticks with a lookalike that has no meaning in Discord
// markup.
func defuse(s string) string {
	return strings.ReplaceAll(s, "`", "\u02cb")
}

// matchesFilter reports whether a line contains the filter text, ignoring case.
// An empty filter matches every line, and the whole rendered line is searched —
// message, attributes and source — so a filter can name a channel, a module or
// an error.
func matchesFilter(line logbuf.Line, filter string) bool {
	if filter == "" {
		return true
	}
	return strings.Contains(strings.ToLower(line.String()), strings.ToLower(filter))
}

// truncateFilter bounds the filter at MaxFilterRunes, cutting on a rune
// boundary so a multi-byte value cannot be split in half.
func truncateFilter(filter string) string {
	if utf8.RuneCountInString(filter) <= MaxFilterRunes {
		return filter
	}
	return string([]rune(filter)[:MaxFilterRunes])
}

// reply edits the already-deferred ephemeral response with the window.
func reply(s *discordgo.Session, i *discordgo.InteractionCreate, content string) {
	if _, err := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Content:         &content,
		AllowedMentions: util.NoMentions(),
	}); err != nil {
		slog.Error("logs: failed to edit interaction response", "error", err)
	}
}

// optionString returns the string value of a named subcommand option.
func optionString(options []*discordgo.ApplicationCommandInteractionDataOption, name string) string {
	for _, o := range options {
		if o.Name == name {
			return o.StringValue()
		}
	}
	return ""
}

// optionInt returns the integer value of a named subcommand option.
func optionInt(options []*discordgo.ApplicationCommandInteractionDataOption, name string) int64 {
	for _, o := range options {
		if o.Name == name {
			return o.IntValue()
		}
	}
	return 0
}
