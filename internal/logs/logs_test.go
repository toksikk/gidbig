package logs

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/toksikk/gidbig/internal/bot"
	"github.com/toksikk/gidbig/internal/logbuf"
)

var _ bot.Module = (*Module)(nil)
var _ bot.AdminProvider = (*Module)(nil)

// bufferWith fills a buffer with one line per message, oldest first.
func bufferWith(t *testing.T, levels []slog.Level, messages ...string) *logbuf.Buffer {
	t.Helper()
	buf := logbuf.New(len(messages) + 1)
	for i, msg := range messages {
		level := slog.LevelInfo
		if len(levels) > i {
			level = levels[i]
		}
		buf.Add(logbuf.Line{
			Time:    time.Date(2026, 10, 16, 9, 0, i, 0, time.UTC),
			Level:   level,
			Message: msg,
			Attrs:   "component=test",
			Source:  "x.go:1",
		})
	}
	return buf
}

func TestModule_Lifecycle(t *testing.T) {
	m := New(logbuf.New(10))
	if m.Name() != "logs" {
		t.Errorf("Name() = %q, want %q", m.Name(), "logs")
	}
	if err := m.Init(bot.Deps{}); err != nil {
		t.Errorf("Init() = %v, want nil", err)
	}
	if m.Background() != nil {
		t.Error("Background() should be nil")
	}
	if err := m.Shutdown(); err != nil {
		t.Errorf("Shutdown() = %v, want nil", err)
	}
	if m.Capacity() != 10 {
		t.Errorf("Capacity() = %d, want 10", m.Capacity())
	}
	if got := New(nil).Capacity(); got != 0 {
		t.Errorf("Capacity() without a buffer = %d, want 0", got)
	}
}

// TestParseLevel pins the level vocabulary both surfaces offer, including the
// case-insensitive spelling a hand-typed query parameter may use.
func TestParseLevel(t *testing.T) {
	for _, value := range []string{"", "all", "ALL"} {
		got, err := ParseLevel(value)
		if err != nil {
			t.Fatalf("ParseLevel(%q) = %v", value, err)
		}
		if got.Value != "all" || got.Level != AllLevels {
			t.Errorf("ParseLevel(%q) = %+v, want the all-levels option", value, got)
		}
	}

	want := map[string]slog.Level{
		"debug": slog.LevelDebug,
		"info":  slog.LevelInfo,
		"warn":  slog.LevelWarn,
		"error": slog.LevelError,
	}
	for value, level := range want {
		got, err := ParseLevel(" " + strings.ToUpper(value) + " ")
		if err != nil {
			t.Fatalf("ParseLevel(%q) = %v", value, err)
		}
		if got.Level != level || got.Value != value {
			t.Errorf("ParseLevel(%q) = %+v, want level %v", value, got, level)
		}
	}

	if _, err := ParseLevel("trace"); err == nil {
		t.Error("ParseLevel(trace) = nil error, want a rejection")
	}
}

func TestLevelOptions_AreOrderedFromWidestToNarrowest(t *testing.T) {
	options := LevelOptions()
	if len(options) != 5 {
		t.Fatalf("got %d level options, want 5", len(options))
	}
	for i := 1; i < len(options); i++ {
		if options[i].Level < options[i-1].Level {
			t.Errorf("option %d (%v) is narrower than %d (%v)", i, options[i].Level, i-1, options[i-1].Level)
		}
		if options[i].Label == "" || options[i].Value == "" {
			t.Errorf("option %d is incomplete: %+v", i, options[i])
		}
	}
	if options[0].Level != AllLevels {
		t.Errorf("the widest option is %v, want AllLevels", options[0].Level)
	}
}

// TestAdminSubcommandGroup_Shape ties the command to the level vocabulary: the
// choices, the count bounds and the filter cap all have to match the module's
// own limits, or Discord accepts a window the module then silently changes.
func TestAdminSubcommandGroup_Shape(t *testing.T) {
	group := New(nil).AdminSubcommandGroup()
	if group.Name != groupName || group.Type != discordgo.ApplicationCommandOptionSubCommandGroup {
		t.Fatalf("group = %+v", group)
	}
	if len(group.Options) != 1 || group.Options[0].Name != "show" {
		t.Fatalf("group options = %+v, want a single show subcommand", group.Options)
	}

	options := map[string]*discordgo.ApplicationCommandOption{}
	for _, o := range group.Options[0].Options {
		options[o.Name] = o
	}

	level, ok := options["level"]
	if !ok || level.Type != discordgo.ApplicationCommandOptionString {
		t.Fatalf("no string level option: %+v", options)
	}
	if len(level.Choices) != len(LevelOptions()) {
		t.Errorf("level has %d choices, want %d", len(level.Choices), len(LevelOptions()))
	}
	for i, choice := range level.Choices {
		if choice.Value != LevelOptions()[i].Value || choice.Name != LevelOptions()[i].Label {
			t.Errorf("choice %d = %v/%v, want %v/%v", i, choice.Value, choice.Name, LevelOptions()[i].Value, LevelOptions()[i].Label)
		}
	}
	if level.Required {
		t.Error("the level option is required, but omitting it must mean every level")
	}

	count, ok := options["count"]
	if !ok || count.Type != discordgo.ApplicationCommandOptionInteger {
		t.Fatalf("no integer count option: %+v", options)
	}
	if count.MinValue == nil || *count.MinValue != 1 {
		t.Errorf("count MinValue = %v, want 1", count.MinValue)
	}
	if count.MaxValue != MaxShowLines {
		t.Errorf("count MaxValue = %v, want %d", count.MaxValue, MaxShowLines)
	}

	filter, ok := options["filter"]
	if !ok || filter.Type != discordgo.ApplicationCommandOptionString {
		t.Fatalf("no string filter option: %+v", options)
	}
	if filter.MaxLength != MaxFilterRunes {
		t.Errorf("filter MaxLength = %d, want %d", filter.MaxLength, MaxFilterRunes)
	}
}

// TestWindow_SelectsTheNewestMatches is the core behaviour both surfaces share:
// a level floor, a case-insensitive substring filter and the newest lines only.
func TestWindow_SelectsTheNewestMatches(t *testing.T) {
	buf := bufferWith(t,
		[]slog.Level{slog.LevelDebug, slog.LevelInfo, slog.LevelInfo, slog.LevelWarn, slog.LevelError},
		"debug noise", "Timeout waiting for gateway", "another info", "WARN timeout again", "fatal timeout",
	)
	m := New(buf)

	got := m.Window(Query{MinLevel: slog.LevelInfo, Filter: "timeout", Count: 2})
	if len(got.Lines) != 2 {
		t.Fatalf("window has %d lines, want the newest 2: %+v", len(got.Lines), got.Lines)
	}
	if got.Lines[0].Message != "WARN timeout again" || got.Lines[1].Message != "fatal timeout" {
		t.Errorf("window lines = %q/%q, want the two newest matches", got.Lines[0].Message, got.Lines[1].Message)
	}
	if got.Held != 5 {
		t.Errorf("Held = %d, want 5", got.Held)
	}

	// The debug line is filtered by the level floor, not by the filter: with the
	// widest floor and no filter it is the newest line of the whole buffer.
	all := m.Window(Query{MinLevel: AllLevels, Count: MaxShowLines})
	if len(all.Lines) != 5 {
		t.Errorf("unfiltered window has %d lines, want all 5", len(all.Lines))
	}
	if all.Lines[0].Message != "debug noise" {
		t.Errorf("oldest line = %q, want the debug line", all.Lines[0].Message)
	}
}

// TestWindow_FilterSearchesTheWholeLine makes a filter usable for attributes and
// source as well as the message.
func TestWindow_FilterSearchesTheWholeLine(t *testing.T) {
	m := New(bufferWith(t, nil, "one", "two"))
	cases := map[string]int{"component=test": 2, "x.go:1": 2, "TWO": 1, "two": 1}
	for filter, want := range cases {
		if got := m.Window(Query{MinLevel: AllLevels, Filter: filter}); len(got.Lines) != want {
			t.Errorf("filter %q matched %d lines, want %d", filter, len(got.Lines), want)
		}
	}
	if got := m.Window(Query{MinLevel: AllLevels, Filter: "nothing here"}); len(got.Lines) != 0 {
		t.Errorf("filter matched %d lines, want none", len(got.Lines))
	}
}

// TestWindow_NormalisesTheQuery pins the bounds: a caller cannot ask for an
// unbounded window or an unbounded filter.
func TestWindow_NormalisesTheQuery(t *testing.T) {
	m := New(bufferWith(t, nil, "one"))
	long := strings.Repeat("f", MaxFilterRunes+20)

	cases := []struct {
		name      string
		query     Query
		wantCount int
		wantRunes int
	}{
		{name: "zero count", query: Query{Count: 0}, wantCount: DefaultShowLines},
		{name: "negative count", query: Query{Count: -3}, wantCount: DefaultShowLines},
		{name: "count above the cap", query: Query{Count: MaxShowLines + 50}, wantCount: MaxShowLines},
		{name: "long filter", query: Query{Count: 5, Filter: long}, wantCount: 5, wantRunes: MaxFilterRunes},
	}

	for _, tc := range cases {
		got := m.Window(tc.query)
		if got.Query.Count != tc.wantCount {
			t.Errorf("%s: count = %d, want %d", tc.name, got.Query.Count, tc.wantCount)
		}
		if tc.wantRunes != 0 {
			if runes := len([]rune(got.Query.Filter)); runes != tc.wantRunes {
				t.Errorf("%s: filter is %d runes, want %d", tc.name, runes, tc.wantRunes)
			}
		}
	}
}

// TestWindow_WithoutABuffer reports the missing buffer instead of panicking, and
// says so on both surfaces.
func TestWindow_WithoutABuffer(t *testing.T) {
	m := New(nil)
	got := m.Window(Query{MinLevel: AllLevels, Count: 5})
	if got.Held != 0 || len(got.Lines) != 0 || !got.Unavailable {
		t.Errorf("window without a buffer = %+v, want an unavailable window", got)
	}
	if got.Header() != UnavailableMessage || got.Text() != UnavailableMessage {
		t.Errorf("an unavailable window renders as %q / %q", got.Header(), got.Text())
	}
	if content := m.DiscordContent(Query{}); content != UnavailableMessage {
		t.Errorf("DiscordContent() = %q, want %q", content, UnavailableMessage)
	}
}

func TestWindow_HeaderDescribesTheWindow(t *testing.T) {
	m := New(bufferWith(t,
		[]slog.Level{slog.LevelDebug, slog.LevelWarn, slog.LevelWarn},
		"one", "timeout", "timeout"),
	)

	cases := []struct {
		name  string
		query Query
		want  string
	}{
		{
			name:  "wide window",
			query: Query{MinLevel: AllLevels, Count: 10},
			want:  "3 of 3 held lines",
		},
		{
			name:  "narrow window with a filter",
			query: Query{MinLevel: slog.LevelWarn, Filter: "timeout", Count: 1},
			want:  `1 of 3 held lines · from WARN up · filter "timeout"`,
		},
		{
			name:  "window nothing matched",
			query: Query{MinLevel: slog.LevelError, Count: 10},
			want:  "0 of 3 held lines · from ERROR up",
		},
	}

	for _, tc := range cases {
		got := m.Window(tc.query).Header()
		if got != tc.want {
			t.Errorf("%s: Header() = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestWindow_HeaderReportsEvictions keeps the header honest about lines the ring
// dropped, which is the difference between "nothing was logged" and "the window
// no longer reaches that far back".
func TestWindow_HeaderReportsEvictions(t *testing.T) {
	buf := logbuf.New(2)
	for _, msg := range []string{"one", "two", "three"} {
		buf.Add(logbuf.Line{Time: time.Now(), Level: slog.LevelInfo, Message: msg})
	}

	got := New(buf).Window(Query{MinLevel: AllLevels, Count: 10}).Header()
	if want := "2 of 2 held lines · 1 older lines evicted"; got != want {
		t.Errorf("Header() = %q, want %q", got, want)
	}

	empty := New(logbuf.New(2)).Window(Query{})
	if empty.Header() != emptyBufferMessage {
		t.Errorf("Header() of an empty buffer = %q, want %q", empty.Header(), emptyBufferMessage)
	}
}

func TestWindow_Text(t *testing.T) {
	m := New(bufferWith(t, nil, "first", "second"))

	got := m.Window(Query{MinLevel: AllLevels, Count: 10}).Text()
	lines := strings.Split(got, "\n")
	if len(lines) != 2 {
		t.Fatalf("Text() has %d lines, want 2: %q", len(lines), got)
	}
	if !strings.HasSuffix(lines[0], "INFO first component=test (x.go:1)") {
		t.Errorf("first line = %q", lines[0])
	}
	if !strings.HasSuffix(lines[1], "INFO second component=test (x.go:1)") {
		t.Errorf("second line = %q", lines[1])
	}

	if got := m.Window(Query{MinLevel: slog.LevelError}).Text(); got != noLinesMessage {
		t.Errorf("Text() of an empty window = %q, want %q", got, noLinesMessage)
	}
}

// TestDiscordContent_FitsOneMessage drives a maximal window through the renderer:
// the reply has to stay inside one content field, keep the newest line and say
// how much was left out.
func TestDiscordContent_FitsOneMessage(t *testing.T) {
	buf := logbuf.New(MaxShowLines)
	long := strings.Repeat("x", logbuf.MaxLineRunes)
	for i := 0; i < MaxShowLines; i++ {
		buf.Add(logbuf.Line{
			Time:    time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC),
			Level:   slog.LevelInfo,
			Message: long,
			Attrs:   "body=" + long,
			Source:  "handler.go:1234",
		})
	}

	content := New(buf).DiscordContent(Query{MinLevel: AllLevels, Count: MaxShowLines})
	if runes := len([]rune(content)); runes > discordContentRunes {
		t.Errorf("content is %d runes, want at most %d", runes, discordContentRunes)
	}
	if !strings.HasPrefix(content, "```\n") || !strings.HasSuffix(content, "\n```") {
		t.Errorf("content is not one fenced block: %q", content[:40])
	}
	if !strings.Contains(content, "newest kept") {
		t.Errorf("content does not report the lines it left out: %q", content[:200])
	}

	// A single maximal line still has to fit: the trim loop may not empty the
	// window to satisfy the limit.
	one := New(buf).DiscordContent(Query{MinLevel: AllLevels, Count: 1})
	if runes := len([]rune(one)); runes > discordContentRunes {
		t.Errorf("single-line content is %d runes, want at most %d", runes, discordContentRunes)
	}
	if !strings.Contains(one, long[:50]) {
		t.Error("the single-line window lost its line")
	}
}

// TestDiscordContent_DefusesBackticks keeps a log line from ending the fenced
// block and leaking the rest of the log into message markup.
func TestDiscordContent_DefusesBackticks(t *testing.T) {
	m := New(bufferWith(t, nil, "``` evil ``` and a `span`"))
	content := m.DiscordContent(Query{MinLevel: AllLevels, Count: 5})

	if got := strings.Count(content, "```"); got != 2 {
		t.Errorf("content has %d fences, want exactly the pair: %q", got, content)
	}
	if strings.Contains(content, "`span`") {
		t.Errorf("a backtick survived inside the block: %q", content)
	}
}

// TestDiscordContent_ReportsTheHeader pins that the reply carries the same
// context the panel shows.
func TestDiscordContent_ReportsTheHeader(t *testing.T) {
	m := New(bufferWith(t, []slog.Level{slog.LevelDebug, slog.LevelWarn}, "noise", "boom"))
	content := m.DiscordContent(Query{MinLevel: slog.LevelWarn, Count: 5})

	for _, want := range []string{"1 of 2 held lines", "from WARN up"} {
		if !strings.Contains(content, want) {
			t.Errorf("content %q is missing %q", content, want)
		}
	}
	if strings.Contains(content, "noise") {
		t.Errorf("content %q contains a line below the floor", content)
	}
}

// TestHandleAdminSubcommand_PostsTheFencedWindow runs the interaction path
// end to end against a stand-in Discord API, so the option parsing, the reply
// edit and the mention suppression are covered together.
func TestHandleAdminSubcommand_PostsTheFencedWindow(t *testing.T) {
	body, payload := handleAdminShow(t, New(bufferWith(t,
		[]slog.Level{slog.LevelDebug, slog.LevelWarn},
		"noise", "boom: something failed",
	)), subcommand("show",
		choiceOption("level", "warn"),
		intOption("count", 5),
		stringOption("filter", "failed"),
	))

	if !strings.Contains(body, "boom: something failed") {
		t.Errorf("reply %q is missing the matching line", body)
	}
	if strings.Contains(body, "noise") {
		t.Errorf("reply %q contains a line the filters excluded", body)
	}
	if got := rawAllowedMentions(t, payload); !strings.Contains(got, `"parse":[]`) {
		t.Errorf("reply does not suppress mentions: %s", got)
	}
}

// TestHandleAdminSubcommand_UnknownSubcommandIsIgnored keeps a group whose
// subcommand this module does not own from producing a reply at all.
func TestHandleAdminSubcommand_UnknownSubcommandIsIgnored(t *testing.T) {
	requests := make(chan []byte, 1)
	session, cleanup := stubDiscordSession(t, requests)
	defer cleanup()

	New(bufferWith(t, nil, "one")).HandleAdminSubcommand(session, interaction(subcommand("other")), subcommand("other"))

	select {
	case body := <-requests:
		t.Fatalf("an unknown subcommand produced a reply: %s", body)
	case <-time.After(50 * time.Millisecond):
	}
}

// --- helpers -------------------------------------------------------------

// handleAdminShow runs one /admin logs show and returns the raw reply body plus
// the decoded payload. It fails the test when no request arrives.
func handleAdminShow(t *testing.T, m *Module, sub *discordgo.ApplicationCommandInteractionDataOption) (string, map[string]any) {
	t.Helper()
	requests := make(chan []byte, 1)
	session, cleanup := stubDiscordSession(t, requests)
	defer cleanup()

	m.HandleAdminSubcommand(session, interaction(sub), sub)

	select {
	case body := <-requests:
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("decode reply %s: %v", body, err)
		}
		content, _ := payload["content"].(string)
		return content, payload
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for a Discord request")
		return "", nil
	}
}

// stubDiscordSession points discordgo's REST endpoints at a recording server.
func stubDiscordSession(t *testing.T, requests chan<- []byte) (*discordgo.Session, func()) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		select {
		case requests <- body:
		default:
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))

	origAPI, origWebhooks := discordgo.EndpointAPI, discordgo.EndpointWebhooks
	discordgo.EndpointAPI = server.URL + "/"
	discordgo.EndpointWebhooks = server.URL + "/webhooks/"
	cleanup := func() {
		discordgo.EndpointAPI = origAPI
		discordgo.EndpointWebhooks = origWebhooks
		server.Close()
	}

	session, err := discordgo.New("Bot test")
	if err != nil {
		cleanup()
		t.Fatalf("discordgo.New: %v", err)
	}
	return session, cleanup
}

// rawAllowedMentions re-encodes the allowed_mentions object, because the parsed
// payload cannot tell an empty parse list from a missing one.
func rawAllowedMentions(t *testing.T, payload map[string]any) string {
	t.Helper()
	encoded, err := json.Marshal(payload["allowed_mentions"])
	if err != nil {
		t.Fatalf("encode allowed_mentions: %v", err)
	}
	return string(encoded)
}

func interaction(sub *discordgo.ApplicationCommandInteractionDataOption) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ID: "interaction", AppID: "app", Token: "token",
			Type: discordgo.InteractionApplicationCommand,
			Data: discordgo.ApplicationCommandInteractionData{
				Name: "admin",
				Options: []*discordgo.ApplicationCommandInteractionDataOption{
					{
						Name:    groupName,
						Type:    discordgo.ApplicationCommandOptionSubCommandGroup,
						Options: []*discordgo.ApplicationCommandInteractionDataOption{sub},
					},
				},
			},
			Member: &discordgo.Member{User: &discordgo.User{ID: "owner-1"}},
		},
	}
}

func subcommand(name string, options ...*discordgo.ApplicationCommandInteractionDataOption) *discordgo.ApplicationCommandInteractionDataOption {
	return &discordgo.ApplicationCommandInteractionDataOption{
		Name:    name,
		Type:    discordgo.ApplicationCommandOptionSubCommand,
		Options: options,
	}
}

func stringOption(name, value string) *discordgo.ApplicationCommandInteractionDataOption {
	return &discordgo.ApplicationCommandInteractionDataOption{
		Name:  name,
		Type:  discordgo.ApplicationCommandOptionString,
		Value: value,
	}
}

// choiceOption is a string option that arrived as a choice: the wire type is the
// same, and its value is the choice's value rather than its label.
func choiceOption(name, value string) *discordgo.ApplicationCommandInteractionDataOption {
	return stringOption(name, value)
}

func intOption(name string, value int64) *discordgo.ApplicationCommandInteractionDataOption {
	return &discordgo.ApplicationCommandInteractionDataOption{
		Name:  name,
		Type:  discordgo.ApplicationCommandOptionInteger,
		Value: float64(value),
	}
}
