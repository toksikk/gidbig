package bot

import (
	"context"
	"log/slog"
)

// Bot wires together a Router, BackgroundSupervisor, and registered Modules.
type Bot struct {
	deps       Deps
	Router     *Router
	background *BackgroundSupervisor
	cancel     context.CancelFunc
	modules    []Module
}

// New creates a Bot from the given Deps.
func New(d Deps) *Bot {
	return &Bot{
		deps:       d,
		Router:     newRouter(d),
		background: newBackgroundSupervisor(),
	}
}

// RegisterModule wires a Module's commands, listeners, and background tasks into the Bot.
func (b *Bot) RegisterModule(m Module) error {
	if err := b.Router.register(m, b.deps); err != nil {
		return err
	}
	for _, comp := range m.Components() {
		b.Router.AddComponent(comp.Prefix, comp.Handler)
	}
	for _, cmd := range m.Commands() {
		b.Router.AddCommand(cmd.Name, nil)
	}
	b.modules = append(b.modules, m)
	return nil
}

// Run registers the router on the Discord session and blocks until ctx is cancelled.
func (b *Bot) Run(ctx context.Context) error {
	ctx, b.cancel = context.WithCancel(ctx)

	b.deps.Session.AddHandler(b.Router.onInteractionCreate)
	b.deps.Session.AddHandler(b.Router.onMessageCreate)

	var tasks []BackgroundTask
	for _, m := range b.modules {
		tasks = append(tasks, m.Background()...)
	}
	b.background.Start(ctx, tasks...)

	slog.Info("bot: running")
	<-ctx.Done()
	return nil
}

// Shutdown cancels the context, waits for background tasks, and shuts down modules.
func (b *Bot) Shutdown() {
	if b.cancel != nil {
		b.cancel()
	}
	b.background.Wait()
	for _, m := range b.modules {
		if err := m.Shutdown(); err != nil {
			slog.Error("bot: module shutdown error", "module", m.Name(), "error", err)
		}
	}
	slog.Info("bot: shutdown complete")
}
