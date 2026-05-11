package bot

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestBackgroundSupervisor_RunsTask(t *testing.T) {
	bs := newBackgroundSupervisor()
	ctx, cancel := context.WithCancel(context.Background())

	var ran atomic.Bool
	bs.Start(ctx, BackgroundTask{
		Name: "test",
		Run: func(ctx context.Context) {
			ran.Store(true)
			<-ctx.Done()
		},
	})

	time.Sleep(10 * time.Millisecond)
	if !ran.Load() {
		t.Fatal("task did not start")
	}
	cancel()
	bs.Wait()
}

func TestBackgroundSupervisor_MultipleTasksAllRun(t *testing.T) {
	bs := newBackgroundSupervisor()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var count atomic.Int32
	tasks := make([]BackgroundTask, 5)
	for i := range tasks {
		tasks[i] = BackgroundTask{
			Name: "task",
			Run: func(ctx context.Context) {
				count.Add(1)
				<-ctx.Done()
			},
		}
	}
	bs.Start(ctx, tasks...)

	time.Sleep(20 * time.Millisecond)
	if got := count.Load(); got != 5 {
		t.Fatalf("want 5 tasks running, got %d", got)
	}
	cancel()
	bs.Wait()
}

func TestBackgroundSupervisor_PanicDoesNotCrash(t *testing.T) {
	bs := newBackgroundSupervisor()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bs.Start(ctx, BackgroundTask{
		Name: "panicky",
		Run:  func(_ context.Context) { panic("boom") },
	})
	bs.Wait()
}

func TestBackgroundSupervisor_StopsOnContextCancel(t *testing.T) {
	bs := newBackgroundSupervisor()
	ctx, cancel := context.WithCancel(context.Background())

	var stopped atomic.Bool
	bs.Start(ctx, BackgroundTask{
		Name: "stopper",
		Run: func(ctx context.Context) {
			<-ctx.Done()
			stopped.Store(true)
		},
	})

	cancel()
	bs.Wait()
	if !stopped.Load() {
		t.Fatal("task did not observe context cancellation")
	}
}

func TestBackgroundSupervisor_EmptyStartIsNoop(t *testing.T) {
	bs := newBackgroundSupervisor()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bs.Start(ctx)
	bs.Wait()
}
