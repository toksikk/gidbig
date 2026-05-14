package gamerstatus

import (
	"context"
	"testing"
	"time"

	"github.com/toksikk/gidbig/internal/bot"
)

func TestNew(t *testing.T) {
	m := New()
	if m == nil {
		t.Fatal("New() returned nil")
	}
	if m.Name() != "gamerstatus" {
		t.Fatalf("Name() = %q, want %q", m.Name(), "gamerstatus")
	}
}

func TestModuleInterface(t *testing.T) {
	var _ bot.Module = New()
}

func TestInitStoresSession(t *testing.T) {
	m := New()
	err := m.Init(bot.Deps{})
	if err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
}

func TestNoCommandsListenersComponents(t *testing.T) {
	m := New()
	_ = m.Init(bot.Deps{})

	if len(m.Commands()) != 0 {
		t.Error("Commands() should be empty")
	}
	if len(m.Listeners()) != 0 {
		t.Error("Listeners() should be empty")
	}
	if len(m.Components()) != 0 {
		t.Error("Components() should be empty")
	}
}

func TestBackgroundTaskRegistered(t *testing.T) {
	m := New()
	tasks := m.Background()
	if len(tasks) != 1 {
		t.Fatalf("Background() len = %d, want 1", len(tasks))
	}
	if tasks[0].Name != "gamerstatus/rotate" {
		t.Errorf("task name = %q, want %q", tasks[0].Name, "gamerstatus/rotate")
	}
}

func TestShutdownCancelsGoroutine(t *testing.T) {
	m := New()
	// large initial delay so the goroutine blocks in the initial select
	m.initialDelay = time.Hour
	m.rotationMin = time.Hour
	m.rotationMax = 2 * time.Hour
	_ = m.Init(bot.Deps{})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel: goroutine exits at the first select, never touching session

	done := make(chan struct{})
	go func() {
		m.runStatusLoop(ctx)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runStatusLoop did not exit within deadline after ctx cancel")
	}
}

func TestShutdown(t *testing.T) {
	m := New()
	if err := m.Shutdown(); err != nil {
		t.Errorf("Shutdown() = %v, want nil", err)
	}
}
