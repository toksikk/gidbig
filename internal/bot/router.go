package bot

import (
	"log/slog"
	"strings"

	"github.com/bwmarrin/discordgo"
)

// commandEntry maps a slash-command name to its handler and middleware chain.
type commandEntry struct {
	handler    HandlerFunc
	middleware []Middleware
}

// Router dispatches Discord interactions and messages to registered Module handlers.
// It is a no-op until modules migrate.
type Router struct {
	deps       Deps
	commands   map[string]commandEntry
	components map[string]func(*discordgo.Session, *discordgo.InteractionCreate)
}

func newRouter(d Deps) *Router {
	return &Router{
		deps:       d,
		commands:   make(map[string]commandEntry),
		components: make(map[string]func(*discordgo.Session, *discordgo.InteractionCreate)),
	}
}

// register wires a Module's listeners into the session.
// Command and component dispatch wiring happens via AddCommand / AddComponent.
func (r *Router) register(m Module, d Deps) error {
	if err := m.Init(d); err != nil {
		return err
	}
	for _, l := range m.Listeners() {
		d.Session.AddHandler(l)
	}
	slog.Info("bot/router: module registered", "module", m.Name())
	return nil
}

// AddCommand registers a slash-command handler with optional middleware.
func (r *Router) AddCommand(name string, h HandlerFunc, mw ...Middleware) {
	r.commands[name] = commandEntry{handler: h, middleware: mw}
	slog.Debug("bot/router: command registered", "name", name)
}

// AddComponent registers a component handler matched by custom-ID prefix.
func (r *Router) AddComponent(prefix string, h func(*discordgo.Session, *discordgo.InteractionCreate)) {
	r.components[prefix] = h
	slog.Debug("bot/router: component registered", "prefix", prefix)
}

func (r *Router) onInteractionCreate(s *discordgo.Session, i *discordgo.InteractionCreate) {
	switch i.Type {
	case discordgo.InteractionApplicationCommand:
		name := i.ApplicationCommandData().Name
		entry, ok := r.commands[name]
		if !ok {
			return
		}
		chain := entry.handler
		for j := len(entry.middleware) - 1; j >= 0; j-- {
			chain = entry.middleware[j](chain)
		}
		chain(s, i)

	case discordgo.InteractionMessageComponent:
		customID := i.MessageComponentData().CustomID
		for prefix, handler := range r.components {
			if strings.HasPrefix(customID, prefix) {
				handler(s, i)
				return
			}
		}
	}
}

func (r *Router) onMessageCreate(_ *discordgo.Session, _ *discordgo.MessageCreate) {
	// No-op until legacy message handler migration.
}
