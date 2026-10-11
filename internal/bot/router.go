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

// Router dispatches Discord interactions and messages to registered Module
// handlers. Every module listener is funnelled through the router so the
// session carries exactly one MessageCreate and one InteractionCreate handler.
type Router struct {
	deps       Deps
	commands   map[string]commandEntry
	components map[string]func(*discordgo.Session, *discordgo.InteractionCreate)

	messageHandlers     []func(*discordgo.Session, *discordgo.MessageCreate)
	interactionHandlers []func(*discordgo.Session, *discordgo.InteractionCreate)
}

func newRouter(d Deps) *Router {
	return &Router{
		deps:       d,
		commands:   make(map[string]commandEntry),
		components: make(map[string]func(*discordgo.Session, *discordgo.InteractionCreate)),
	}
}

// register initialises a Module and sorts its listeners into the router's
// typed buckets. Message and interaction listeners are dispatched by the single
// session handlers; any other event type is registered directly.
func (r *Router) register(m Module, d Deps) error {
	if err := m.Init(d); err != nil {
		return err
	}
	for _, l := range m.Listeners() {
		switch h := l.(type) {
		case func(*discordgo.Session, *discordgo.MessageCreate):
			r.messageHandlers = append(r.messageHandlers, h)
		case func(*discordgo.Session, *discordgo.InteractionCreate):
			r.interactionHandlers = append(r.interactionHandlers, h)
		default:
			// Non-message, non-interaction events (Ready, Connect,
			// MessageUpdate, …) are registered directly. This is the only
			// AddHandler outside the router's own session handlers.
			d.Session.AddHandler(l)
		}
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

// Attach registers the router's single MessageCreate and InteractionCreate
// handlers on the session. All AddHandler calls live in this file so the
// session never carries more than one handler per event type.
func (r *Router) Attach(s *discordgo.Session) {
	if s == nil {
		return
	}
	s.AddHandler(r.onInteractionCreate)
	s.AddHandler(r.onMessageCreate)
}

func (r *Router) onInteractionCreate(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// discordgo runs every registered handler in its own goroutine by default
	// (SyncEvents is false); dispatching concurrently preserves that so one
	// module's handler cannot block another's.
	for _, h := range r.interactionHandlers {
		go h(s, i)
	}

	switch i.Type {
	case discordgo.InteractionApplicationCommand:
		name := i.ApplicationCommandData().Name
		entry, ok := r.commands[name]
		if !ok || entry.handler == nil {
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

func (r *Router) onMessageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	// One goroutine per handler, matching discordgo's default dispatch. A
	// blocking handler (for example gippity's LLM call) must not stall the
	// others.
	for _, h := range r.messageHandlers {
		go h(s, m)
	}
}
