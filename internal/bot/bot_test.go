package bot

import (
	"errors"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
)

// stubModule is a minimal Module implementation for testing. Its Init
// optionally registers one command through the injected router.
type stubModule struct {
	name       string
	initErr    error
	register   bool
	registered bool
}

func (s *stubModule) Name() string { return s.name }
func (s *stubModule) Init(d Deps) error {
	if s.initErr != nil {
		return s.initErr
	}
	if s.register {
		d.Router.AddCommand(&discordgo.ApplicationCommand{Name: s.name}, func(_ *discordgo.Session, _ *discordgo.InteractionCreate) {})
		s.registered = true
	}
	return nil
}
func (s *stubModule) Background() []BackgroundTask { return nil }
func (s *stubModule) Shutdown() error              { return nil }

func TestBot_RegisterModule_Success(t *testing.T) {
	b := New(Deps{})
	if err := b.RegisterModule(&stubModule{name: "ok"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBot_RegisterModule_InitError(t *testing.T) {
	b := New(Deps{})
	want := errors.New("init failed")
	if err := b.RegisterModule(&stubModule{name: "bad", initErr: want}); !errors.Is(err, want) {
		t.Fatalf("want %v, got %v", want, err)
	}
}

func TestBot_RegisterModule_InitErrorDoesNotAddModule(t *testing.T) {
	b := New(Deps{})
	_ = b.RegisterModule(&stubModule{name: "bad", initErr: errors.New("boom")})
	if len(b.modules) != 0 {
		t.Fatalf("failed module should not be appended, got %d modules", len(b.modules))
	}
}

func TestBot_RegisterModule_RouterCollectsCommands(t *testing.T) {
	b := New(Deps{})
	if err := b.RegisterModule(&stubModule{name: "leetoclock", register: true}); err != nil {
		t.Fatalf("register: %v", err)
	}
	cmds := b.Router.Commands()
	if len(cmds) != 1 || cmds[0].Name != "leetoclock" {
		t.Fatalf("router commands = %#v, want the module's command", cmds)
	}
}

// registerThenFailModule registers a command and then fails Init, simulating a
// module that starts wiring handlers before hitting a fallible step.
type registerThenFailModule struct{}

func (registerThenFailModule) Name() string { return "half" }
func (registerThenFailModule) Init(d Deps) error {
	d.Router.AddCommand(&discordgo.ApplicationCommand{Name: "ghost"}, func(_ *discordgo.Session, _ *discordgo.InteractionCreate) {})
	return errors.New("init failed after registering")
}
func (registerThenFailModule) Background() []BackgroundTask { return nil }
func (registerThenFailModule) Shutdown() error              { return nil }

func TestBot_RegisterModule_RollsBackOnInitError(t *testing.T) {
	b := New(Deps{})

	// A healthy module first, so rollback must keep its command.
	if err := b.RegisterModule(&stubModule{name: "good", register: true}); err != nil {
		t.Fatalf("register good: %v", err)
	}
	if err := b.RegisterModule(registerThenFailModule{}); err == nil {
		t.Fatal("expected RegisterModule to fail")
	}

	cmds := b.Router.Commands()
	if len(cmds) != 1 || cmds[0].Name != "good" {
		t.Fatalf("router commands = %#v, want only the healthy module's command", cmds)
	}
}

// duplicateModule registers two commands with the same name.
type duplicateModule struct{}

func (duplicateModule) Name() string { return "dupes" }
func (duplicateModule) Init(d Deps) error {
	h := func(_ *discordgo.Session, _ *discordgo.InteractionCreate) {}
	d.Router.AddCommand(&discordgo.ApplicationCommand{Name: "same"}, h)
	d.Router.AddCommand(&discordgo.ApplicationCommand{Name: "same"}, h)
	return nil
}
func (duplicateModule) Background() []BackgroundTask { return nil }
func (duplicateModule) Shutdown() error              { return nil }

func TestBot_RegisterModule_FailsOnDuplicateCommand(t *testing.T) {
	b := New(Deps{})
	err := b.RegisterModule(duplicateModule{})
	if err == nil {
		t.Fatal("expected RegisterModule to fail on a duplicate command name")
	}
	if !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("error = %v, want it to mention the duplicate", err)
	}
	if cmds := b.Router.Commands(); len(cmds) != 0 {
		t.Fatalf("commands = %#v, want the duplicate module rolled back", cmds)
	}
}
