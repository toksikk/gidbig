package logscmd

import (
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/bwmarrin/discordgo"
	"github.com/toksikk/gidbig/internal/bot"
)

var _ bot.AdminProvider = (*Module)(nil)

func TestAdminSubcommandGroup(t *testing.T) {
	g := New(nil).AdminSubcommandGroup()
	if g.Name != "logs" || g.Type != discordgo.ApplicationCommandOptionSubCommandGroup {
		t.Fatalf("group = %q type %v", g.Name, g.Type)
	}
	if len(g.Options) != 1 || g.Options[0].Name != "show" {
		t.Fatalf("subcommands = %+v", g.Options)
	}
	opts := map[string]*discordgo.ApplicationCommandOption{}
	for _, o := range g.Options[0].Options {
		opts[o.Name] = o
	}
	if o := opts["level"]; o == nil || o.Type != discordgo.ApplicationCommandOptionString || len(o.Choices) != 4 {
		t.Errorf("level option = %+v", o)
	}
	if o := opts["filter"]; o == nil || o.Type != discordgo.ApplicationCommandOptionString {
		t.Errorf("filter option = %+v", o)
	}
	o := opts["count"]
	if o == nil || o.Type != discordgo.ApplicationCommandOptionInteger || o.MinValue == nil || *o.MinValue != 1 || o.MaxValue != 200 {
		t.Errorf("count option = %+v", o)
	}
}

func opt(name string, t discordgo.ApplicationCommandOptionType, v any) *discordgo.ApplicationCommandInteractionDataOption {
	return &discordgo.ApplicationCommandInteractionDataOption{Name: name, Type: t, Value: v}
}

func TestQueryFromOptions(t *testing.T) {
	q := QueryFromOptions(nil)
	if q.MinLevel != slog.LevelInfo || q.Count != DefaultCount || q.Filter != "" {
		t.Errorf("defaults = %+v", q)
	}
	q = QueryFromOptions([]*discordgo.ApplicationCommandInteractionDataOption{
		opt("level", discordgo.ApplicationCommandOptionString, "warn"),
		opt("filter", discordgo.ApplicationCommandOptionString, "voice"),
		opt("count", discordgo.ApplicationCommandOptionInteger, float64(7)),
	})
	if q.MinLevel != slog.LevelWarn || q.Count != 7 || q.Filter != "voice" {
		t.Errorf("query = %+v", q)
	}
}

func TestRenderEmpty(t *testing.T) {
	got := Render(nil, MaxMessages, maxMessageRunes)
	if len(got) != 1 || got[0] != EmptyMessage {
		t.Errorf("Render(nil) = %v", got)
	}
}

func TestRenderFitsInOneMessage(t *testing.T) {
	lines := []string{"a", "b", "c"}
	got := Render(lines, MaxMessages, maxMessageRunes)
	if len(got) != 1 || got[0] != "```\na\nb\nc\n```" {
		t.Errorf("Render = %q", got)
	}
}

func TestRenderTrimsToTwoMessagesWithNote(t *testing.T) {
	var lines []string
	for i := range 200 {
		lines = append(lines, fmt.Sprintf("2026-01-01T00:00:00Z INFO line %03d %s", i, strings.Repeat("x", 40)))
	}
	got := Render(lines, MaxMessages, maxMessageRunes)
	if len(got) != 2 {
		t.Fatalf("messages = %d, want 2", len(got))
	}
	shown := 0
	for _, m := range got {
		if n := utf8.RuneCountInString(m); n > maxMessageRunes {
			t.Errorf("message has %d runes, limit %d", n, maxMessageRunes)
		}
		shown += strings.Count(m, " INFO line ")
	}
	want := fmt.Sprintf("… %d older lines omitted — see /admin/logs\n```\n", 200-shown)
	if !strings.HasPrefix(got[0], want) {
		t.Errorf("first message starts %q, want %q", got[0][:80], want)
	}
	if !strings.Contains(got[1], "line 199") {
		t.Error("newest line missing from the last message")
	}
	// Chronological across messages: the last line of message 1 precedes the
	// first line of message 2.
	first := strings.Split(strings.TrimSuffix(got[0], "\n```"), "\n")
	second := strings.Split(strings.TrimPrefix(got[1], "```\n"), "\n")
	if first[len(first)-1] >= second[0] {
		t.Errorf("order broken: %q then %q", first[len(first)-1], second[0])
	}
}

func TestRenderTruncatesAnOverlongLine(t *testing.T) {
	got := Render([]string{strings.Repeat("é", 5000)}, MaxMessages, maxMessageRunes)
	if len(got) != 1 || utf8.RuneCountInString(got[0]) > maxMessageRunes {
		t.Errorf("Render overlong = %d messages", len(got))
	}
}

func TestRenderNeutralisesFences(t *testing.T) {
	got := Render([]string{"evil ``` break"}, MaxMessages, maxMessageRunes)
	if strings.Count(got[0], "```") != 2 {
		t.Errorf("fence escaped: %q", got[0])
	}
}
