package bot

import (
	"log/slog"
	"strings"

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
// there.
type Router struct {
	deps Deps

	commands        []commandEntry
	components      map[string]func(*discordgo.Session, *discordgo.InteractionCreate)
	messageHandlers []func(*discordgo.Session, *discordgo.MessageCreate)
}

func newRouter(d Deps) *Router {
	return &Router{
		deps:       d,
		components: make(map[string]func(*discordgo.Session, *discordgo.InteractionCreate)),
	}
}

// AddCommand registers a slash command and its handler. The optional middleware
// wraps the handler in the order given. Registering the definition and the
// handler together keeps a command and its behaviour in one place.
func (r *Router) AddCommand(cmd *discordgo.ApplicationCommand, h HandlerFunc, mw ...Middleware) {
	if r == nil || cmd == nil || h == nil {
		return
	}
	r.commands = append(r.commands, commandEntry{command: cmd, handler: h, middleware: mw})
	slog.Debug("bot/router: command registered", "name", cmd.Name)
}

// AddComponent registers a component handler matched by custom-ID prefix.
func (r *Router) AddComponent(prefix string, h func(*discordgo.Session, *discordgo.InteractionCreate)) {
	if r == nil || prefix == "" || h == nil {
		return
	}
	r.components[prefix] = h
	slog.Debug("bot/router: component registered", "prefix", prefix)
}

// OnMessage registers a MessageCreate handler.
func (r *Router) OnMessage(h func(*discordgo.Session, *discordgo.MessageCreate)) {
	if r == nil || h == nil {
		return
	}
	r.messageHandlers = append(r.messageHandlers, h)
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
	r.deps.Session.AddHandler(h)
}

// Commands returns the registered command definitions in registration order,
// for the bulk overwrite at startup.
func (r *Router) Commands() []*discordgo.ApplicationCommand {
	if r == nil {
		return nil
	}
	out := make([]*discordgo.ApplicationCommand, 0, len(r.commands))
	for _, e := range r.commands {
		out = append(out, e.command)
	}
	return out
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
		for _, e := range r.commands {
			if e.command.Name != name {
				continue
			}
			chain := Recover()(e.handler)
			for j := len(e.middleware) - 1; j >= 0; j-- {
				chain = e.middleware[j](chain)
			}
			chain(s, i)
			return
		}
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
