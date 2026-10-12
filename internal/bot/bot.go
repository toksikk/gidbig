package bot

import (
	"context"
	"fmt"
	"log/slog"
)

// Bot wires together a Router, BackgroundSupervisor, and registered Modules.
type Bot struct {
	deps              Deps
	Router            *Router
	background        *BackgroundSupervisor
	cancel            context.CancelFunc
	modules           []Module
	handlersInstalled bool
}

// New creates a Bot from the given Deps. When a session is present the router's
// single MessageCreate and InteractionCreate handlers are installed right away,
// so they are active before the gateway is opened. The router is exposed to
// modules through Deps.Router, which is set here before any module registers.
func New(d Deps) *Bot {
	b := &Bot{
		deps:       d,
		background: newBackgroundSupervisor(),
	}
	b.Router = newRouter(b.deps)
	b.deps.Router = b.Router
	b.installSessionHandlers()
	return b
}

// installSessionHandlers registers the router's message and interaction
// handlers on the session exactly once.
func (b *Bot) installSessionHandlers() {
	if b.handlersInstalled || b.deps.Session == nil {
		return
	}
	b.Router.Attach(b.deps.Session)
	b.handlersInstalled = true
}

// RegisterModule initialises a Module and records its background tasks and
// stats. A module registers its handlers with the router during Init; if Init
// fails, or it registered a conflicting command, everything it registered is
// rolled back and the error is returned so the module is dropped loudly.
func (b *Bot) RegisterModule(m Module) error {
	snapshot := b.Router.snapshot()
	if err := m.Init(b.deps); err != nil {
		b.Router.restore(snapshot)
		return err
	}
	if err := b.Router.takeRegistrationError(); err != nil {
		b.Router.restore(snapshot)
		return fmt.Errorf("module %s: %w", m.Name(), err)
	}
	if sp, ok := m.(StatsProvider); ok {
		b.deps.Stats.Register(sp)
	}
	b.modules = append(b.modules, m)
	slog.Info("bot: module registered", "module", m.Name())
	return nil
}

// Run registers the router's slash commands, starts background tasks, and
// blocks until ctx is cancelled.
func (b *Bot) Run(ctx context.Context) error {
	ctx, b.cancel = context.WithCancel(ctx)

	b.installSessionHandlers()

	var tasks []BackgroundTask
	for _, m := range b.modules {
		tasks = append(tasks, m.Background()...)
	}
	b.background.Start(ctx, tasks...)

	if user := b.deps.Session.State.User; user != nil {
		if _, err := b.deps.Session.ApplicationCommandBulkOverwrite(user.ID, "", b.Router.Commands()); err != nil {
			slog.Error("bot: failed to register slash commands", "error", err)
		}
	} else {
		slog.Warn("bot: session user unknown; skipped slash-command registration")
	}

	slog.Info("bot: running")
	<-ctx.Done()
	return nil
}

// Shutdown cancels the context, waits for background tasks, shuts down modules,
// and closes the Discord session.
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
	if err := b.deps.Session.Close(); err != nil {
		slog.Error("bot: error closing discord session", "error", err)
	}
	slog.Info("bot: shutdown complete")
}
