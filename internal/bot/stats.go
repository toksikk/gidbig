package bot

import "context"

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
