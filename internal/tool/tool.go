// Package tool defines a small, provider-agnostic registry of LLM tools.
//
// A tool lets the bot consult internal data while composing an answer. The
// package deliberately imports only the standard library so modules can
// register tools without pulling in the bot, gippity or LLM packages and
// without creating an import cycle.
package tool

import (
	"context"
	"encoding/json"
	"sync"
)

// Env carries the caller and channel scope of one answer loop. Handlers use it
// to clamp every lookup to what the originating user and channel may see; it is
// never taken from model-provided arguments.
type Env struct {
	GuildID   string
	ChannelID string
	UserID    string
}

// Def describes one tool and its handler.
//
// Parameters is a JSON Schema object describing the handler's arguments.
// Handler receives the raw JSON arguments produced by the model and returns a
// single, already-formatted string. The caller truncates the result before it
// is sent back to the model, so handlers need not bound their own output.
type Def struct {
	Name        string
	Description string
	Parameters  json.RawMessage
	Handler     func(ctx context.Context, env Env, args json.RawMessage) (string, error)
}

// Registry is a shared, mutable collection of tool definitions. Modules
// register during Init and the answer loop reads All at request time, so
// registration order does not matter. It is safe for concurrent use.
type Registry struct {
	mu    sync.RWMutex
	defs  map[string]Def
	order []string
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{defs: make(map[string]Def)}
}

// Register adds a definition. A definition without a name or handler is
// ignored. Re-registering an existing name replaces it in place so repeated
// wiring stays idempotent.
func (r *Registry) Register(d Def) {
	if r == nil || d.Name == "" || d.Handler == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.defs[d.Name]; !exists {
		r.order = append(r.order, d.Name)
	}
	r.defs[d.Name] = d
}

// All returns the registered definitions in registration order.
func (r *Registry) All() []Def {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Def, 0, len(r.order))
	for _, name := range r.order {
		out = append(out, r.defs[name])
	}
	return out
}

// Get returns the definition registered under name.
func (r *Registry) Get(name string) (Def, bool) {
	if r == nil {
		return Def{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	d, ok := r.defs[name]
	return d, ok
}
