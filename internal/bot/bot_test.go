package bot

import (
	"errors"
	"testing"

	"github.com/bwmarrin/discordgo"
)

// stubModule is a minimal Module implementation for testing.
type stubModule struct {
	name    string
	initErr error
}

func (s *stubModule) Name() string                               { return s.name }
func (s *stubModule) Init(_ Deps) error                         { return s.initErr }
func (s *stubModule) Commands() []*discordgo.ApplicationCommand { return nil }
func (s *stubModule) Listeners() []EventListener                { return nil }
func (s *stubModule) Components() []ComponentHandler            { return nil }
func (s *stubModule) Background() []BackgroundTask              { return nil }
func (s *stubModule) Shutdown() error                           { return nil }

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
