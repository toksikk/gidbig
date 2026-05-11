package bot

import (
	"context"
	"log/slog"
	"sync"
)

// BackgroundSupervisor manages supervised context-aware goroutines.
type BackgroundSupervisor struct {
	wg sync.WaitGroup
}

func newBackgroundSupervisor() *BackgroundSupervisor {
	return &BackgroundSupervisor{}
}

// Start launches each task in its own goroutine under ctx.
// Each task is supervised: panics are logged and do not crash the process.
func (bs *BackgroundSupervisor) Start(ctx context.Context, tasks ...BackgroundTask) {
	for _, t := range tasks {
		bs.wg.Go(func() {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("bot/background: task panicked", "task", t.Name, "panic", r)
				}
			}()
			slog.Info("bot/background: task started", "task", t.Name)
			t.Run(ctx)
			slog.Info("bot/background: task stopped", "task", t.Name)
		})
	}
}

// Wait blocks until all supervised tasks have returned.
func (bs *BackgroundSupervisor) Wait() {
	bs.wg.Wait()
}
