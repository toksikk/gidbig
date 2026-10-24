package gamerstatus

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/toksikk/gidbig/internal/bot"
	"github.com/toksikk/gidbig/internal/tool"
)

func TestInitRegistersTool(t *testing.T) {
	m := New()
	reg := tool.NewRegistry()
	if err := m.Init(bot.Deps{Tools: reg}); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	if _, ok := reg.Get("current_game_status"); !ok {
		t.Fatal("current_game_status not registered")
	}
}

func TestInitNilToolsRegistry(t *testing.T) {
	m := New()
	if err := m.Init(bot.Deps{}); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
}

func TestStatusHandlerEmpty(t *testing.T) {
	m := New()
	got, err := m.statusHandler(context.Background(), tool.Env{}, nil)
	if err != nil {
		t.Fatalf("statusHandler error: %v", err)
	}
	if got != "No game status is currently set." {
		t.Fatalf("got %q", got)
	}
}

func TestStatusHandlerAfterSet(t *testing.T) {
	m := New()
	clock := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	m.now = func() time.Time { return clock }
	m.setCurrent("Doom")

	got, _ := m.statusHandler(context.Background(), tool.Env{}, nil)
	if want := `Currently shown status: Playing "Doom" (set less than a minute ago).`; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}

	clock = clock.Add(7*time.Minute + 30*time.Second)
	got, _ = m.statusHandler(context.Background(), tool.Env{}, nil)
	if want := `Currently shown status: Playing "Doom" (set 7 minutes ago).`; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestStatusHandlerConcurrent(t *testing.T) {
	m := New()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			m.setCurrent("Tetris")
		}()
		go func() {
			defer wg.Done()
			got, err := m.statusHandler(context.Background(), tool.Env{}, nil)
			if err != nil || (got != "No game status is currently set." && !strings.Contains(got, "Tetris")) {
				t.Errorf("statusHandler = %q, %v", got, err)
			}
		}()
	}
	wg.Wait()
	if got, _ := m.statusHandler(context.Background(), tool.Env{}, nil); !strings.Contains(got, "Tetris") {
		t.Fatalf("got %q", got)
	}
}
