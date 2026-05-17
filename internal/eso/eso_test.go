package eso

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
	if m.responder == nil {
		t.Fatal("Init did not create responder")
	}
}

func TestModule_Commands_HasEso(t *testing.T) {
	cmds := New().Commands()
	if len(cmds) != 1 {
		t.Fatalf("expected 1 command, got %d", len(cmds))
	}
	if cmds[0].Name != "eso" {
		t.Fatalf("expected command name 'eso', got %q", cmds[0].Name)
	}
}

func TestModule_Commands_HasThemaOption(t *testing.T) {
	cmds := New().Commands()
	if len(cmds[0].Options) != 1 {
		t.Fatalf("expected 1 option, got %d", len(cmds[0].Options))
	}
	opt := cmds[0].Options[0]
	if opt.Name != "thema" {
		t.Fatalf("expected option name 'thema', got %q", opt.Name)
	}
	if opt.Type != discordgo.ApplicationCommandOptionString {
		t.Fatalf("expected string option type, got %v", opt.Type)
	}
	if opt.Required {
		t.Fatal("thema option must not be required")
	}
	if opt.MaxLength != 200 {
		t.Fatalf("expected MaxLength 200, got %d", opt.MaxLength)
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

func TestModule_OnInteractionCreate_EsoCommand(t *testing.T) {
	m := New()
	s, _ := discordgo.New("Bot fake-token")
	if err := m.Init(bot.Deps{Session: s}); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	i := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			Type: discordgo.InteractionApplicationCommand,
			Data: discordgo.ApplicationCommandInteractionData{Name: "eso"},
		},
	}
	// InteractionRespond fails (no real connection) → handler returns early, no panic.
	m.onInteractionCreate(s, i)
}

func TestModule_Responder_ResolvedSubjectReachesLLM(t *testing.T) {
	// Verifies that a pre-resolved subject (mention already replaced by name)
	// reaches the LLM user prompt unchanged — i.e. no raw <@ID> tokens.
	m := New()
	s, _ := discordgo.New("Bot fake-token")
	if err := m.Init(bot.Deps{Session: s}); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	var capturedUser string
	m.responder.GenerateFn = func(_ context.Context, _, user string) (string, error) {
		capturedUser = user
		return "output", nil
	}
	resolved := "Alice" // what util.ResolveMentions would return for a known user
	m.responder.GenerateWithPrompt(context.Background(), "Generiere esoterischen Unsinn über das Thema: "+resolved)
	if !strings.Contains(capturedUser, "Alice") {
		t.Fatalf("resolved name not in LLM prompt: %q", capturedUser)
	}
	if strings.Contains(capturedUser, "<@") {
		t.Fatalf("raw mention token leaked into LLM prompt: %q", capturedUser)
	}
}

func TestModule_Responder_WithSubject(t *testing.T) {
	m := New()
	s, _ := discordgo.New("Bot fake-token")
	if err := m.Init(bot.Deps{Session: s}); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	var capturedUser string
	m.responder.GenerateFn = func(_ context.Context, _, user string) (string, error) {
		capturedUser = user
		return "Kristallenergie fließt durch dein Auto.", nil
	}
	got := m.responder.GenerateWithPrompt(context.Background(), "Generiere esoterischen Unsinn über das Thema: Auto")
	if got != "Kristallenergie fließt durch dein Auto." {
		t.Fatalf("unexpected result: %q", got)
	}
	if !strings.Contains(capturedUser, "Auto") {
		t.Fatalf("subject not in user prompt: %q", capturedUser)
	}
}

func TestModule_Responder_AIPath(t *testing.T) {
	m := New()
	s, _ := discordgo.New("Bot fake-token")
	if err := m.Init(bot.Deps{Session: s}); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	m.responder.GenerateFn = func(_ context.Context, _, _ string) (string, error) {
		return "AI generated eso text", nil
	}
	got := m.responder.Generate(context.Background())
	if got != "AI generated eso text" {
		t.Fatalf("expected AI text, got %q", got)
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
	hasOhai := false
	for _, o := range ohai {
		if strings.HasPrefix(got, o) {
			hasOhai = true
			break
		}
	}
	if !hasOhai {
		t.Fatalf("fallback output not a valid eso message: %q", got)
	}
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
