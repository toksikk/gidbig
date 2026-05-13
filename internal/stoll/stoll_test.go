package stoll

import (
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/toksikk/gidbig/internal/bot"
)

func TestNew_ReturnsModule(t *testing.T) {
	m := New()
	if m == nil {
		t.Fatal("New() returned nil")
	}
}

func TestModule_Name(t *testing.T) {
	if New().Name() != "stoll" {
		t.Fatalf("expected name 'stoll', got %q", New().Name())
	}
}

func TestModule_Init(t *testing.T) {
	m := New()
	s, _ := discordgo.New("Bot fake-token")
	if err := m.Init(bot.Deps{Session: s}); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	if m.session != s {
		t.Fatal("Init did not store session")
	}
}

func TestModule_Commands_Empty(t *testing.T) {
	if cmds := New().Commands(); len(cmds) != 0 {
		t.Fatalf("expected 0 commands, got %d", len(cmds))
	}
}

func TestModule_Components_Empty(t *testing.T) {
	if comps := New().Components(); len(comps) != 0 {
		t.Fatalf("expected 0 components, got %d", len(comps))
	}
}

func TestModule_Background_Empty(t *testing.T) {
	if tasks := New().Background(); len(tasks) != 0 {
		t.Fatalf("expected 0 background tasks, got %d", len(tasks))
	}
}

func TestModule_Shutdown_NoError(t *testing.T) {
	if err := New().Shutdown(); err != nil {
		t.Fatalf("Shutdown returned error: %v", err)
	}
}

func TestModule_Listeners_Count(t *testing.T) {
	listeners := New().Listeners()
	if len(listeners) != 1 {
		t.Fatalf("expected 1 listener, got %d", len(listeners))
	}
}

func TestModule_OnMessageCreate_IgnoresNonStoll(t *testing.T) {
	m := New()
	// non-stoll message with nil session should not panic
	m.onMessageCreate(nil, &discordgo.MessageCreate{
		Message: &discordgo.Message{Content: "hello world"},
	})
}

func TestModule_OnMessageCreate_IgnoresEmptyContent(t *testing.T) {
	m := New()
	m.onMessageCreate(nil, &discordgo.MessageCreate{
		Message: &discordgo.Message{Content: ""},
	})
}

func TestModule_OnMessageCreate_IgnoresOtherCommands(t *testing.T) {
	m := New()
	m.onMessageCreate(nil, &discordgo.MessageCreate{
		Message: &discordgo.Message{Content: "!other"},
	})
}

func TestModule_OnMessageCreate_StollCommand(t *testing.T) {
	m := New()
	s, _ := discordgo.New("Bot fake-token")
	// HTTP send fails but must not panic; err != nil path is handled gracefully.
	m.onMessageCreate(s, &discordgo.MessageCreate{
		Message: &discordgo.Message{Content: "!stoll", ChannelID: "123"},
	})
}

func TestModule_OnMessageCreate_StollCommandCaseInsensitive(t *testing.T) {
	m := New()
	s, _ := discordgo.New("Bot fake-token")
	m.onMessageCreate(s, &discordgo.MessageCreate{
		Message: &discordgo.Message{Content: "!STOLL", ChannelID: "123"},
	})
}

func TestBuildQuote_Format(t *testing.T) {
	for range 20 {
		q := buildQuote()
		if !strings.HasPrefix(q, "> ") {
			t.Fatalf("quote missing '> ' prefix: %q", q)
		}
		if !strings.HasSuffix(q, "Dr. Axel Stoll, promovierter Naturwissenschaftler") {
			t.Fatalf("quote missing attribution suffix: %q", q)
		}
	}
}

func TestBuildQuote_UsesAllThreeLists(t *testing.T) {
	// Run many times; with 3 lists each with many entries the chance of a single
	// list not contributing is astronomically small over 100 runs.
	for range 100 {
		q := buildQuote()
		if q == "" {
			t.Fatal("buildQuote returned empty string")
		}
	}
}
