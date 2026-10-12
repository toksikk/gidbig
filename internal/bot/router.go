package bot

import (
	"log/slog"
	"strings"
	"sync"

	"github.com/bwmarrin/discordgo"
)

// commandEntry is one slash command: its definition, handler and middleware.
type commandEntry struct {
	command    *discordgo.ApplicationCommand
	handler    HandlerFunc
	middleware []Middleware
}

// Router is the single dispatch point for every module's Discord handlers. A
// module registers its slash commands, message components, message handler and
// gateway events through the Router during Init; the Router installs exactly
// one MessageCreate and one InteractionCreate session handler and fans out from
// there. Every handler runs behind Recover, so a panicking handler is logged
// instead of taking the process down.
//
// Registration happens while the gateway may already be delivering events, so
// the command/component/message tables are guarded by a mutex.
type Router struct {
	deps Deps

	mu              sync.RWMutex
	commands        []commandEntry
	components      map[string]func(*discordgo.Session, *discordgo.InteractionCreate)
	messageHandlers []func(*discordgo.Session, *discordgo.MessageCreate)
	sessionCleanups []func()
}

func newRouter(d Deps) *Router {
	return &Router{
		deps:       d,
		components: make(map[string]func(*discordgo.Session, *discordgo.InteractionCreate)),
	}
}

// AddCommand registers a slash command and its handler. The optional middleware
// wraps the handler in the order given. Registering the definition and the
// handler together keeps a command and its behaviour in one place. A duplicate
// name is rejected loudly: ApplicationCommandBulkOverwrite fails the whole
// batch on one collision, so keeping duplicates out preserves every command.
func (r *Router) AddCommand(cmd *discordgo.ApplicationCommand, h HandlerFunc, mw ...Middleware) {
	if r == nil || cmd == nil || h == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range r.commands {
		if e.command.Name == cmd.Name {
			slog.Error("bot/router: duplicate command name ignored", "name", cmd.Name)
			return
		}
	}
	r.commands = append(r.commands, commandEntry{command: cmd, handler: h, middleware: mw})
	slog.Debug("bot/router: command registered", "name", cmd.Name)
}

// AddComponent registers a component handler matched by custom-ID prefix.
func (r *Router) AddComponent(prefix string, h func(*discordgo.Session, *discordgo.InteractionCreate)) {
	if r == nil || prefix == "" || h == nil {
		return
	}
	r.mu.Lock()
	r.components[prefix] = h
	r.mu.Unlock()
	slog.Debug("bot/router: component registered", "prefix", prefix)
}

// OnMessage registers a MessageCreate handler.
func (r *Router) OnMessage(h func(*discordgo.Session, *discordgo.MessageCreate)) {
	if r == nil || h == nil {
		return
	}
	r.mu.Lock()
	r.messageHandlers = append(r.messageHandlers, h)
	r.mu.Unlock()
}

// The On* methods register typed gateway-event handlers. Unlike discordgo's
// untyped AddHandler — which accepts a mistyped listener and then never calls
// it — an explicit signature makes a wrong listener a compile error.

// OnReady registers a Ready handler.
func (r *Router) OnReady(h func(*discordgo.Session, *discordgo.Ready)) { r.addHandler(h) }

// OnConnect registers a Connect handler.
func (r *Router) OnConnect(h func(*discordgo.Session, *discordgo.Connect)) { r.addHandler(h) }

// OnDisconnect registers a Disconnect handler.
func (r *Router) OnDisconnect(h func(*discordgo.Session, *discordgo.Disconnect)) { r.addHandler(h) }

// OnResumed registers a Resumed handler.
func (r *Router) OnResumed(h func(*discordgo.Session, *discordgo.Resumed)) { r.addHandler(h) }

// OnMessageUpdate registers a MessageUpdate handler.
func (r *Router) OnMessageUpdate(h func(*discordgo.Session, *discordgo.MessageUpdate)) {
	r.addHandler(h)
}

func (r *Router) addHandler(h any) {
	if r == nil || h == nil || r.deps.Session == nil {
		return
	}
	r.mu.Lock()
	remove := r.deps.Session.AddHandler(h)
	r.sessionCleanups = append(r.sessionCleanups, remove)
	r.mu.Unlock()
}

// Commands returns the registered command definitions in registration order,
// for the bulk overwrite at startup.
func (r *Router) Commands() []*discordgo.ApplicationCommand {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*discordgo.ApplicationCommand, 0, len(r.commands))
	for _, e := range r.commands {
		out = append(out, e.command)
	}
	return out
}

// routerState is a snapshot of the registrations taken before a module's Init,
// so a failed Init can be rolled back completely.
type routerState struct {
	commands        []commandEntry
	components      map[string]func(*discordgo.Session, *discordgo.InteractionCreate)
	messageHandlers []func(*discordgo.Session, *discordgo.MessageCreate)
	sessionCleanups int
}

func (r *Router) snapshot() routerState {
	r.mu.RLock()
	defer r.mu.RUnlock()
	st := routerState{
		commands:        append([]commandEntry(nil), r.commands...),
		messageHandlers: append([]func(*discordgo.Session, *discordgo.MessageCreate){}, r.messageHandlers...),
		components:      make(map[string]func(*discordgo.Session, *discordgo.InteractionCreate), len(r.components)),
		sessionCleanups: len(r.sessionCleanups),
	}
	for k, v := range r.components {
		st.components[k] = v
	}
	return st
}

// restore drops everything registered since the snapshot was taken and removes
// the gateway handlers added in that window from the session.
func (r *Router) restore(st routerState) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.commands = st.commands
	r.messageHandlers = st.messageHandlers
	r.components = st.components
	for _, remove := range r.sessionCleanups[st.sessionCleanups:] {
		if remove != nil {
			remove()
		}
	}
	r.sessionCleanups = r.sessionCleanups[:st.sessionCleanups]
}

// Attach registers the router's single MessageCreate and InteractionCreate
// handlers on the session. All AddHandler calls for those two events live here,
// so the session never carries more than one handler per event type.
func (r *Router) Attach(s *discordgo.Session) {
	if s == nil {
		return
	}
	s.AddHandler(r.onInteractionCreate)
	s.AddHandler(r.onMessageCreate)
}

func (r *Router) onInteractionCreate(s *discordgo.Session, i *discordgo.InteractionCreate) {
	switch i.Type {
	case discordgo.InteractionApplicationCommand, discordgo.InteractionApplicationCommandAutocomplete:
		name := i.ApplicationCommandData().Name
		r.mu.RLock()
		var entry commandEntry
		found := false
		for _, e := range r.commands {
			if e.command.Name == name {
				entry, found = e, true
				break
			}
		}
		r.mu.RUnlock()
		if !found || entry.handler == nil {
			return
		}
		chain := entry.handler
		for j := len(entry.middleware) - 1; j >= 0; j-- {
			chain = entry.middleware[j](chain)
		}
		Recover()(chain)(s, i)

	case discordgo.InteractionMessageComponent:
		customID := i.MessageComponentData().CustomID
		r.mu.RLock()
		var handler func(*discordgo.Session, *discordgo.InteractionCreate)
		for prefix, h := range r.components {
			if strings.HasPrefix(customID, prefix) {
				handler = h
				break
			}
		}
		r.mu.RUnlock()
		if handler != nil {
			Recover()(handler)(s, i)
		}
	}
}

func (r *Router) onMessageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	r.mu.RLock()
	handlers := append([]func(*discordgo.Session, *discordgo.MessageCreate){}, r.messageHandlers...)
	r.mu.RUnlock()

	// One goroutine per handler, matching discordgo's default dispatch. A
	// blocking handler (for example gippity's LLM call) must not stall the
	// others, and Recover keeps a panic from taking the process down from this
	// goroutine.
	for _, h := range handlers {
		go func(h func(*discordgo.Session, *discordgo.MessageCreate)) {
			defer func() {
				if rec := recover(); rec != nil {
					slog.Error("bot/router: recovered from panic", "event", "MessageCreate", "panic", rec)
				}
			}()
			h(s, m)
		}(h)
	}
}
