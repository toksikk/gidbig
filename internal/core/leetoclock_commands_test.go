package gidbig

import (
	"errors"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/toksikk/gidbig/internal/bot"
	"github.com/toksikk/gidbig/internal/leetoclock"
)

// stubModule lets tests drive Bot.RegisterModule without a real session. A
// healthy stub registers one command through the injected router; a stub with
// an Init error must leave the router untouched.
type stubModule struct {
	name    string
	initErr error
	command *discordgo.ApplicationCommand
}

func (s stubModule) Name() string { return s.name }
func (s stubModule) Init(d bot.Deps) error {
	if s.initErr != nil {
		return s.initErr
	}
	if s.command != nil {
		d.Router.AddCommand(s.command, func(*discordgo.Session, *discordgo.InteractionCreate) {})
	}
	return nil
}
func (s stubModule) Background() []bot.BackgroundTask { return nil }
func (s stubModule) Shutdown() error                  { return nil }

func TestRegisterModuleSkipsCommandsWhenInitFails(t *testing.T) {
	b := bot.New(bot.Deps{Stats: bot.NewStatsRegistry()})

	cmd := leetoclock.New().Commands()[0]
	if err := b.RegisterModule(stubModule{name: "leetoclock", command: cmd}); err != nil {
		t.Fatalf("registering a healthy module failed: %v", err)
	}
	if err := b.RegisterModule(stubModule{name: "broken", initErr: errors.New("boom"), command: cmd}); err == nil {
		t.Fatal("expected RegisterModule to fail for a module whose Init fails")
	}

	cmds := b.Router.Commands()
	if len(cmds) != 1 || cmds[0].Name != "leetoclock" {
		t.Fatalf("commands = %#v, want only the healthy module's commands", cmds)
	}
}
