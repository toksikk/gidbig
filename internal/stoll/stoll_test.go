package stoll

import (
	"context"
	"errors"
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
	if m.responder == nil {
		t.Fatal("Init did not create responder")
	}
}

func TestModule_Commands_HasStoll(t *testing.T) {
	cmds := New().Commands()
	if len(cmds) != 1 {
		t.Fatalf("expected 1 command, got %d", len(cmds))
	}
	if cmds[0].Name != "stoll" {
		t.Fatalf("expected command name 'stoll', got %q", cmds[0].Name)
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

func TestModule_OnInteractionCreate_IgnoresNonApplicationCommand(t *testing.T) {
	m := New()
	s, _ := discordgo.New("Bot fake-token")
	if err := m.Init(bot.Deps{Session: s}); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	i := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			Type: discordgo.InteractionMessageComponent,
		},
	}
	m.onInteractionCreate(s, i)
}

func TestModule_OnInteractionCreate_IgnoresOtherCommand(t *testing.T) {
	m := New()
	s, _ := discordgo.New("Bot fake-token")
	if err := m.Init(bot.Deps{Session: s}); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	i := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			Type: discordgo.InteractionApplicationCommand,
			Data: discordgo.ApplicationCommandInteractionData{Name: "other"},
		},
	}
	m.onInteractionCreate(s, i)
}

func TestModule_OnInteractionCreate_StollCommand(t *testing.T) {
	m := New()
	s, _ := discordgo.New("Bot fake-token")
	if err := m.Init(bot.Deps{Session: s}); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	i := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			Type: discordgo.InteractionApplicationCommand,
			Data: discordgo.ApplicationCommandInteractionData{Name: "stoll"},
		},
	}
	// InteractionRespond fails (no real connection) → handler returns early, no panic.
	m.onInteractionCreate(s, i)
}

func TestModule_Responder_AIPath(t *testing.T) {
	m := New()
	s, _ := discordgo.New("Bot fake-token")
	if err := m.Init(bot.Deps{Session: s}); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	m.responder.GenerateFn = func(_ context.Context, _, _ string) (string, error) {
		return "> Die Sonne ist kalt!\n - Dr. Axel Stoll, promovierter Naturwissenschaftler", nil
	}
	got := m.responder.Generate(context.Background())
	if !strings.HasPrefix(got, "> ") {
		t.Fatalf("expected AI quote to start with '> ', got %q", got)
	}
}

func TestModule_Responder_FallbackOnError(t *testing.T) {
	m := New()
	s, _ := discordgo.New("Bot fake-token")
	if err := m.Init(bot.Deps{Session: s}); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	m.responder.GenerateFn = func(_ context.Context, _, _ string) (string, error) {
		return "", errors.New("simulated LLM failure")
	}
	got := m.responder.Generate(context.Background())
	if got == "" {
		t.Fatal("fallback returned empty string")
	}
	if !strings.HasPrefix(got, "> ") {
		t.Fatalf("fallback output not a valid stoll quote: %q", got)
	}
	if !strings.HasSuffix(got, "Dr. Axel Stoll, promovierter Naturwissenschaftler") {
		t.Fatalf("fallback output missing attribution suffix: %q", got)
	}
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
	for range 100 {
		q := buildQuote()
		if q == "" {
			t.Fatal("buildQuote returned empty string")
		}
	}
}
