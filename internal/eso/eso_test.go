package eso

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
	if New().Name() != "eso" {
		t.Fatalf("expected name 'eso', got %q", New().Name())
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

func TestModule_OnMessageCreate_IgnoresNonEso(t *testing.T) {
	m := New()
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

func TestModule_OnMessageCreate_EsoCommand(t *testing.T) {
	m := New()
	s, _ := discordgo.New("Bot fake-token")
	// HTTP send fails but must not panic; err != nil path is handled gracefully.
	m.onMessageCreate(s, &discordgo.MessageCreate{
		Message: &discordgo.Message{Content: "!eso", ChannelID: "123"},
	})
}

func TestModule_OnMessageCreate_EsoCommandCaseInsensitive(t *testing.T) {
	m := New()
	s, _ := discordgo.New("Bot fake-token")
	m.onMessageCreate(s, &discordgo.MessageCreate{
		Message: &discordgo.Message{Content: "!ESO", ChannelID: "123"},
	})
}

func TestBuildMessage_NonEmpty(t *testing.T) {
	for range 20 {
		msg := buildMessage()
		if msg == "" {
			t.Fatal("buildMessage returned empty string")
		}
	}
}

func TestBuildMessage_ContainsAllParts(t *testing.T) {
	for range 100 {
		msg := buildMessage()
		// Must start with one of the ohai entries (all end with ", ")
		hasOhai := false
		for _, o := range ohai {
			if strings.HasPrefix(msg, o) {
				hasOhai = true
				break
			}
		}
		if !hasOhai {
			t.Fatalf("buildMessage output missing ohai prefix: %q", msg)
		}
		// Must end with one of the todotings entries
		hasTodo := false
		for _, td := range todotings {
			if strings.HasSuffix(msg, td) {
				hasTodo = true
				break
			}
		}
		if !hasTodo {
			t.Fatalf("buildMessage output missing todotings suffix: %q", msg)
		}
	}
}
