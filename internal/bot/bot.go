package bot

import (
	"context"
	"log/slog"

	"github.com/bwmarrin/discordgo"
)

// Bot wires together a Router, BackgroundSupervisor, and registered Modules.
type Bot struct {
	deps              Deps
	Router            *Router
	background        *BackgroundSupervisor
	cancel            context.CancelFunc
	modules           []Module
	commands          []*discordgo.ApplicationCommand
	handlersInstalled bool
}

// New creates a Bot from the given Deps. When a session is present the router's
// single MessageCreate and InteractionCreate handlers are installed right away,
// so they are active before the gateway is opened.
func New(d Deps) *Bot {
	b := &Bot{
		deps:       d,
		Router:     newRouter(d),
		background: newBackgroundSupervisor(),
	}
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

// RegisterModule wires a Module's commands, listeners, background tasks and
// stats into the Bot. A module whose Init fails is not registered.
func (b *Bot) RegisterModule(m Module) error {
	if err := b.Router.register(m, b.deps); err != nil {
		return err
	}
	for _, comp := range m.Components() {
		b.Router.AddComponent(comp.Prefix, comp.Handler)
	}
	b.commands = append(b.commands, m.Commands()...)
	if sp, ok := m.(StatsProvider); ok {
		b.deps.Stats.Register(sp)
	}
	b.modules = append(b.modules, m)
	return nil
}

// AddCommands appends extra slash commands (composition-root owned) to the set
// registered with Discord on Run.
func (b *Bot) AddCommands(cmds ...*discordgo.ApplicationCommand) {
	b.commands = append(b.commands, cmds...)
}

// Commands returns the slash commands collected from registered modules and
// AddCommands, in registration order.
func (b *Bot) Commands() []*discordgo.ApplicationCommand {
	out := make([]*discordgo.ApplicationCommand, len(b.commands))
	copy(out, b.commands)
	return out
}

// Run registers the router on the Discord session, starts background tasks,
// publishes slash commands, and blocks until ctx is cancelled.
func (b *Bot) Run(ctx context.Context) error {
	ctx, b.cancel = context.WithCancel(ctx)

	b.installSessionHandlers()

	var tasks []BackgroundTask
	for _, m := range b.modules {
		tasks = append(tasks, m.Background()...)
	}
	b.background.Start(ctx, tasks...)

	if user := b.deps.Session.State.User; user != nil {
		if _, err := b.deps.Session.ApplicationCommandBulkOverwrite(user.ID, "", b.commands); err != nil {
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
