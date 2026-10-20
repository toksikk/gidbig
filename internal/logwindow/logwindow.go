// Package logwindow keeps the most recent log records in memory so the owner
// can read them from /admin logs show and the /admin/logs web page. It is a
// slog.Handler tee: every record the wrapped handler accepts is copied into a
// fixed-capacity ring and then passed on unchanged.
//
// Both surfaces render through Format, so they cannot drift apart. Nothing is
// written to disk; the window is gone on restart.
package logwindow

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// DefaultCapacity is the window size used when none is configured.
const DefaultCapacity = 2000

// Entry is one copied log record.
type Entry struct {
	Time    time.Time
	Level   slog.Level
	Message string
	// Attrs are the flattened key=value pairs, group prefixes included.
	Attrs []Attr
}

// Attr is one flattened attribute.
type Attr struct {
	Key   string
	Value string
}

// Query selects lines from the window.
type Query struct {
	// MinLevel is the lowest level included.
	MinLevel slog.Level
	// Filter is a case-insensitive substring matched against the rendered line;
	// empty matches everything.
	Filter string
	// Count is the maximum number of newest lines returned; zero or less means
	// the whole window.
	Count int
}

// Window is a mutex-guarded ring of log entries.
type Window struct {
	mu      sync.Mutex
	entries []Entry
	next    int
	full    bool
}

// New returns a window holding capacity entries; a non-positive capacity uses
// DefaultCapacity.
func New(capacity int) *Window {
	if capacity <= 0 {
		capacity = DefaultCapacity
	}
	return &Window{entries: make([]Entry, capacity)}
}

// Cap returns the window capacity.
func (w *Window) Cap() int { return len(w.entries) }

func (w *Window) add(e Entry) {
	w.mu.Lock()
	w.entries[w.next] = e
	w.next++
	if w.next == len(w.entries) {
		w.next = 0
		w.full = true
	}
	w.mu.Unlock()
}

// snapshot returns the entries oldest to newest.
func (w *Window) snapshot() []Entry {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.full {
		return append([]Entry(nil), w.entries[:w.next]...)
	}
	out := make([]Entry, 0, len(w.entries))
	out = append(out, w.entries[w.next:]...)
	return append(out, w.entries[:w.next]...)
}

// Lines returns the rendered lines matching q, oldest to newest, limited to the
// newest q.Count.
func (w *Window) Lines(q Query) []string {
	filter := strings.ToLower(q.Filter)
	var out []string
	for _, e := range w.snapshot() {
		if e.Level < q.MinLevel {
			continue
		}
		line := Format(e)
		if filter != "" && !strings.Contains(strings.ToLower(line), filter) {
			continue
		}
		out = append(out, line)
	}
	if q.Count > 0 && len(out) > q.Count {
		out = out[len(out)-q.Count:]
	}
	return out
}

// Format renders one entry as
// `2006-01-02T15:04:05Z07:00 LEVEL msg key=value ...` in UTC. Values with
// whitespace, quotes or '=' are quoted.
func Format(e Entry) string {
	var b strings.Builder
	b.WriteString(e.Time.UTC().Format(time.RFC3339))
	b.WriteByte(' ')
	b.WriteString(e.Level.String())
	b.WriteByte(' ')
	b.WriteString(e.Message)
	for _, a := range e.Attrs {
		b.WriteByte(' ')
		b.WriteString(a.Key)
		b.WriteByte('=')
		b.WriteString(quote(a.Value))
	}
	return b.String()
}

func quote(v string) string {
	if v == "" || strings.ContainsAny(v, " \t\r\n\"=") {
		return fmt.Sprintf("%q", v)
	}
	return v
}

// ParseLevel maps debug/info/warn/error (case-insensitive) to a level. ok is
// false for anything else.
func ParseLevel(s string) (slog.Level, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug, true
	case "info":
		return slog.LevelInfo, true
	case "warn":
		return slog.LevelWarn, true
	case "error":
		return slog.LevelError, true
	}
	return slog.LevelInfo, false
}

// Handler returns a slog.Handler that copies every record into w and then
// passes it to inner. Enabled delegates to inner, so the window holds exactly
// what the configured level accepts.
func (w *Window) Handler(inner slog.Handler) slog.Handler {
	return &teeHandler{w: w, inner: inner}
}

type teeHandler struct {
	w     *Window
	inner slog.Handler
	attrs []Attr
	group string
}

func (h *teeHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.inner.Enabled(ctx, l)
}

func (h *teeHandler) Handle(ctx context.Context, r slog.Record) error {
	e := Entry{Time: r.Time, Level: r.Level, Message: r.Message}
	e.Attrs = append(e.Attrs, h.attrs...)
	r.Attrs(func(a slog.Attr) bool {
		e.Attrs = flatten(e.Attrs, h.group, a)
		return true
	})
	// The lock is taken and released inside add; the inner handler runs
	// outside it.
	h.w.add(e)
	return h.inner.Handle(ctx, r)
}

func (h *teeHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	n := &teeHandler{w: h.w, inner: h.inner.WithAttrs(attrs), group: h.group}
	n.attrs = append([]Attr(nil), h.attrs...)
	for _, a := range attrs {
		n.attrs = flatten(n.attrs, h.group, a)
	}
	return n
}

func (h *teeHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	g := name
	if h.group != "" {
		g = h.group + "." + name
	}
	return &teeHandler{w: h.w, inner: h.inner.WithGroup(name), attrs: h.attrs, group: g}
}

func flatten(dst []Attr, prefix string, a slog.Attr) []Attr {
	a.Value = a.Value.Resolve()
	if a.Equal(slog.Attr{}) {
		return dst
	}
	key := a.Key
	if prefix != "" && key != "" {
		key = prefix + "." + key
	} else if key == "" {
		key = prefix
	}
	if a.Value.Kind() == slog.KindGroup {
		for _, ga := range a.Value.Group() {
			dst = flatten(dst, key, ga)
		}
		return dst
	}
	return append(dst, Attr{Key: key, Value: a.Value.String()})
}
