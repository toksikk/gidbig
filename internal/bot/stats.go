package bot

import (
	"context"
	"sync"
)

// Stat is a single labelled value reported in /status.
type Stat struct {
	Name  string
	Value string
}

// ModuleStats is a runtime snapshot reported by a module.
// Summary is shown on one line in the compact view; Detail is added in the detailed view.
type ModuleStats struct {
	Summary []Stat
	Detail  []Stat
}

// StatsProvider is implemented by modules that report runtime counters in /status.
// Stats must honour ctx cancellation; callers bound it with a short timeout.
type StatsProvider interface {
	Name() string
	Stats(ctx context.Context) (ModuleStats, error)
}

// StatsFunc adapts a function to StatsProvider.
type StatsFunc struct {
	ModuleName string
	Fn         func(ctx context.Context) (ModuleStats, error)
}

// Name returns the module name.
func (f StatsFunc) Name() string { return f.ModuleName }

// Stats calls Fn.
func (f StatsFunc) Stats(ctx context.Context) (ModuleStats, error) { return f.Fn(ctx) }

// StatsRegistry collects the StatsProviders of registered modules so the
// composition root and the core module can enumerate them without holding
// package-level state.
type StatsRegistry struct {
	mu        sync.Mutex
	providers []StatsProvider
}

// NewStatsRegistry returns an empty registry.
func NewStatsRegistry() *StatsRegistry { return &StatsRegistry{} }

// Register adds a provider. Duplicate registrations by module name replace the
// previous entry so repeated wiring stays idempotent.
func (r *StatsRegistry) Register(p StatsProvider) {
	if r == nil || p == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, existing := range r.providers {
		if existing.Name() == p.Name() {
			r.providers[i] = p
			return
		}
	}
	r.providers = append(r.providers, p)
}

// All returns the registered providers in registration order.
func (r *StatsRegistry) All() []StatsProvider {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]StatsProvider, len(r.providers))
	copy(out, r.providers)
	return out
}
