package gidbig

import (
	"errors"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/toksikk/gidbig/internal/bot"
	"github.com/toksikk/gidbig/internal/leetoclock"
)

// stubModule lets tests drive Bot.RegisterModule without a real session.
type stubModule struct {
	name    string
	initErr error
	cmds    []*discordgo.ApplicationCommand
}

func (s stubModule) Name() string                              { return s.name }
func (s stubModule) Init(bot.Deps) error                       { return s.initErr }
func (s stubModule) Commands() []*discordgo.ApplicationCommand { return s.cmds }
func (s stubModule) Listeners() []bot.EventListener            { return nil }
func (s stubModule) Components() []bot.ComponentHandler        { return nil }
func (s stubModule) Background() []bot.BackgroundTask          { return nil }
func (s stubModule) Shutdown() error                           { return nil }

func TestRegisterModuleSkipsCommandsWhenInitFails(t *testing.T) {
	b := bot.New(bot.Deps{Stats: bot.NewStatsRegistry()})

	if err := b.RegisterModule(stubModule{name: "leetoclock", cmds: leetoclock.New().Commands()}); err != nil {
		t.Fatalf("registering a healthy module failed: %v", err)
	}
	if err := b.RegisterModule(stubModule{name: "broken", initErr: errors.New("boom"), cmds: leetoclock.New().Commands()}); err == nil {
		t.Fatal("expected RegisterModule to fail for a module whose Init fails")
	}

	cmds := b.Commands()
	if len(cmds) != 1 || cmds[0].Name != "leetoclock" {
		t.Fatalf("commands = %#v, want only the healthy module's commands", cmds)
	}
}
