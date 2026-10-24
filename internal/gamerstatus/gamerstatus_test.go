package gamerstatus

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/toksikk/gidbig/internal/bot"
	"github.com/toksikk/gidbig/internal/util"
)

func TestNew(t *testing.T) {
	m := New()
	if m == nil {
		t.Fatal("New() returned nil")
	}
	if m.Name() != "gamerstatus" {
		t.Fatalf("Name() = %q, want %q", m.Name(), "gamerstatus")
	}
	if m.season == nil {
		t.Fatal("New() should initialize the season hook")
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

func TestInitRegistersNothing(t *testing.T) {
	m := New()
	r := bot.New(bot.Deps{}).Router
	if err := m.Init(bot.Deps{Router: r}); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}

	if len(r.Commands()) != 0 {
		t.Error("gamerstatus should register no commands")
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

// fakeStatus is a statusUpdater whose UpdateGameStatus returns gameErr.
type fakeStatus struct {
	gameErr error
}

func (f *fakeStatus) UpdateStreamingStatus(int, string, string) error { return nil }
func (f *fakeStatus) UpdateGameStatus(int, string) error              { return f.gameErr }

// runOneIteration runs the status loop until its first iteration, including
// UpdateGameStatus, has completed, then stops it.
func runOneIteration(t *testing.T, m *Module) {
	t.Helper()
	m.initialDelay = 0
	m.rotationMin = time.Millisecond
	m.rotationMax = 2 * time.Millisecond

	// The second season() call starts iteration two, so iteration one has
	// completed.
	secondIteration := make(chan struct{})
	calls := 0
	m.season = func() util.Season {
		calls++
		if calls == 2 {
			close(secondIteration)
		}
		return util.SeasonNone
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		m.runStatusLoop(ctx)
		close(done)
	}()

	select {
	case <-secondIteration:
	case <-time.After(5 * time.Second):
		t.Fatal("runStatusLoop did not complete an iteration within deadline")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("runStatusLoop did not exit after ctx cancel")
	}
}

func TestRunStatusLoopFailedUpdateNotRecorded(t *testing.T) {
	m := New()
	m.session = &fakeStatus{gameErr: errors.New("gateway down")}
	runOneIteration(t, m)

	if game, _ := m.currentStatus(); game != "" {
		t.Fatalf("currentStatus() = %q after failed update, want empty", game)
	}
}

func TestRunStatusLoopSuccessfulUpdateRecorded(t *testing.T) {
	m := New()
	m.session = &fakeStatus{}
	runOneIteration(t, m)

	game, since := m.currentStatus()
	if !slices.Contains(games, game) {
		t.Fatalf("currentStatus() = %q, want a game from the list", game)
	}
	if since.IsZero() {
		t.Fatal("currentStatus() since is zero after successful update")
	}
}

func TestShutdown(t *testing.T) {
	m := New()
	if err := m.Shutdown(); err != nil {
		t.Errorf("Shutdown() = %v, want nil", err)
	}
}

func TestCurrentGameSeasons(t *testing.T) {
	cases := []struct {
		name   string
		season util.Season
		want   string
	}{
		{"halloween", util.SeasonHalloween, "🎃 Spooky Season 👻"},
		{"aprilfools", util.SeasonAprilFools, string(util.Cl)},
		{"newyear", util.SeasonNewYear, "🥂 Prost, frohes neues Jahr!"},
		{"easter", util.SeasonEaster, "🐣 Jagd nach Ostereiern"},
		{"christmas", util.SeasonChristmas, "🎅 Frohe Weihnachten"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := New()
			m.season = func() util.Season { return tc.season }

			if got := m.currentGame(); got != tc.want {
				t.Fatalf("currentGame() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCurrentGameDefault(t *testing.T) {
	m := New()
	m.season = func() util.Season { return util.SeasonNone }

	got := m.currentGame()
	found := false
	for _, game := range games {
		if game == got {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("currentGame() = %q, want a random game from the list", got)
	}
}
