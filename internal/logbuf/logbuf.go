// Package logbuf keeps a bounded, in-memory window of the newest log records
// the process produced. The window is what the owner reads through the
// /admin logs slash command and the owner-only web page /admin/logs, so what the
// bot logs can be inspected without shell access to the container it runs in.
//
// Records are captured once, in the slog handler every log call already goes
// through, and stored already flattened to a single line of text. Both surfaces
// render that same text, so they cannot disagree about what the bot logged.
package logbuf

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	// DefaultCapacity is how many records a Buffer holds unless told otherwise.
	DefaultCapacity = 500
	// MaxLineRunes caps the message and the attribute text of one stored line, so
	// a single log call carrying a huge payload cannot crowd out the window.
	MaxLineRunes = 600
	// maxAttrValueRunes caps one attribute value.
	maxAttrValueRunes = 160
	// truncationMark ends a value that was cut to fit its cap.
	truncationMark = "…"
)

// Line is one captured log record, flattened to a single line of text.
type Line struct {
	Time  time.Time
	Level slog.Level
	// Message is the record's message with control characters escaped.
	Message string
	// Attrs holds the record's attributes as `key=value key2="two words"`,
	// prefixed with the groups the handler was given through WithGroup. It is
	// empty when the record carries no attributes.
	Attrs string
	// Source is the call site as "file.go:line", empty when it is unknown.
	Source string
}

// String renders the line the way both surfaces show it.
func (l Line) String() string {
	var sb strings.Builder
	sb.WriteString(l.Time.Format("2006-01-02 15:04:05.000"))
	sb.WriteByte(' ')
	sb.WriteString(LevelLabel(l.Level))
	sb.WriteByte(' ')
	sb.WriteString(l.Message)
	if l.Attrs != "" {
		sb.WriteByte(' ')
		sb.WriteString(l.Attrs)
	}
	if l.Source != "" {
		sb.WriteString(" (")
		sb.WriteString(l.Source)
		sb.WriteByte(')')
	}
	return sb.String()
}

// LevelLabel is the label of the level band a record falls into. There are four
// bands, so a custom level like Info+1 reports INFO; the four are also what a
// caller can filter by.
func LevelLabel(level slog.Level) string {
	switch {
	case level < slog.LevelInfo:
		return "DEBUG"
	case level < slog.LevelWarn:
		return "INFO"
	case level < slog.LevelError:
		return "WARN"
	default:
		return "ERROR"
	}
}

// Buffer is a fixed-size ring of the newest lines, safe for concurrent use.
type Buffer struct {
	mu    sync.Mutex
	lines []Line
	// next is where the next line is written, held how many slots are in use and
	// evicted how many lines were dropped because the ring was full.
	next    int
	held    int
	evicted uint64
}

// New returns a Buffer holding the newest capacity lines. A capacity below one
// falls back to DefaultCapacity.
func New(capacity int) *Buffer {
	if capacity < 1 {
		capacity = DefaultCapacity
	}
	return &Buffer{lines: make([]Line, capacity)}
}

// Add stores a line, replacing the oldest one once the buffer is full.
func (b *Buffer) Add(line Line) {
	line.sanitize()
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.held == len(b.lines) {
		b.evicted++
	}
	b.lines[b.next] = line
	b.next = (b.next + 1) % len(b.lines)
	if b.held < len(b.lines) {
		b.held++
	}
}

// Snapshot returns the held lines, oldest first. It is a copy: the caller may
// keep or cut it without holding the lock.
func (b *Buffer) Snapshot() []Line {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]Line, 0, b.held)
	start := (b.next - b.held + len(b.lines)) % len(b.lines)
	for i := 0; i < b.held; i++ {
		out = append(out, b.lines[(start+i)%len(b.lines)])
	}
	return out
}

// Len is how many lines the buffer currently holds.
func (b *Buffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.held
}

// Capacity is the largest number of lines the buffer holds.
func (b *Buffer) Capacity() int { return len(b.lines) }

// Evicted is how many lines were dropped because the buffer was full.
func (b *Buffer) Evicted() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.evicted
}

// sanitize flattens a line and bounds its length, so one record always renders
// as one line and no record can dominate the window.
func (l *Line) sanitize() {
	l.Message = truncate(escapeControl(l.Message), MaxLineRunes)
	l.Attrs = truncate(escapeControl(l.Attrs), MaxLineRunes)
	l.Source = truncate(escapeControl(l.Source), maxAttrValueRunes)
}

// escapeControl keeps a value on one line: a newline becomes the two characters
// \n, a carriage return \r, a tab a space, and any other control character is
// dropped.
func escapeControl(s string) string {
	if !strings.ContainsFunc(s, func(r rune) bool { return r < ' ' || r == 0x7f }) {
		return s
	}
	var sb strings.Builder
	sb.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\n':
			sb.WriteString(`\n`)
		case r == '\r':
			sb.WriteString(`\r`)
		case r == '\t':
			sb.WriteByte(' ')
		case r < ' ' || r == 0x7f:
			// Other control characters carry no text of their own.
		default:
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

// truncate caps a value at limit runes, marking the cut with an ellipsis.
func truncate(s string, limit int) string {
	if utf8.RuneCountInString(s) <= limit {
		return s
	}
	return string([]rune(s)[:limit]) + truncationMark
}

// Handler is a slog.Handler that stores every record it handles in a Buffer and
// then passes the record to the handler it wraps. Only records the wrapped
// handler accepts are stored, because Enabled decides for both.
type Handler struct {
	inner  slog.Handler
	buf    *Buffer
	attrs  []slog.Attr
	groups []string
}

// NewHandler wraps inner so every accepted record is also kept in buf. A nil
// inner handler only collects, which is what a caller that wants the window
// without a second sink needs.
func NewHandler(inner slog.Handler, buf *Buffer) slog.Handler {
	return &Handler{inner: inner, buf: buf}
}

// Enabled reports whether the wrapped handler accepts the level.
func (h *Handler) Enabled(ctx context.Context, level slog.Level) bool {
	if h.inner == nil {
		return true
	}
	return h.inner.Enabled(ctx, level)
}

// Handle stores the record and forwards it to the wrapped handler.
func (h *Handler) Handle(ctx context.Context, r slog.Record) error {
	if h.buf != nil {
		h.buf.Add(h.line(r))
	}
	if h.inner == nil {
		return nil
	}
	return h.inner.Handle(ctx, r)
}

// WithAttrs returns a handler that carries attrs into every stored line too.
func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}
	next := &Handler{buf: h.buf, groups: h.groups}
	next.attrs = append(next.attrs, h.attrs...)
	next.attrs = append(next.attrs, attrs...)
	if h.inner != nil {
		next.inner = h.inner.WithAttrs(attrs)
	}
	return next
}

// WithGroup returns a handler that prefixes the group name onto every stored
// attribute, matching what the wrapped handler does with the same name. An
// empty name is a no-op, as slog specifies.
func (h *Handler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	next := &Handler{buf: h.buf, attrs: h.attrs}
	next.groups = append(next.groups, h.groups...)
	next.groups = append(next.groups, name)
	if h.inner != nil {
		next.inner = h.inner.WithGroup(name)
	}
	return next
}

// line flattens a record, including the attributes the handler carries from With
// and WithGroup, into the text the buffer stores.
func (h *Handler) line(r slog.Record) Line {
	var sb strings.Builder
	for _, a := range h.attrs {
		appendAttr(&sb, h.groups, a)
	}
	r.Attrs(func(a slog.Attr) bool {
		appendAttr(&sb, h.groups, a)
		return true
	})
	return Line{
		Time:    r.Time,
		Level:   r.Level,
		Message: r.Message,
		Attrs:   strings.TrimSpace(sb.String()),
		Source:  sourceOf(r.PC),
	}
}

// appendAttr writes one attribute as `key=value`. groups are the enclosing group
// names; a group value is flattened under its own key so `g.k=v` reports which
// group the value came from. Values are cut at maxAttrValueRunes.
func appendAttr(sb *strings.Builder, groups []string, a slog.Attr) {
	a.Value = a.Value.Resolve()
	if a.Equal(slog.Attr{}) {
		return
	}
	key := a.Key
	if key == "" {
		// slog's own handlers spell an empty key with a sole "!".
		key = "!"
	}
	if len(groups) > 0 {
		key = strings.Join(groups, ".") + "." + key
	}

	if a.Value.Kind() == slog.KindGroup {
		nested := make([]string, 0, len(groups)+1)
		nested = append(nested, groups...)
		nested = append(nested, a.Key)
		for _, ga := range a.Value.Group() {
			appendAttr(sb, nested, ga)
		}
		return
	}

	value := a.Value.String()
	if needsQuoting(value) {
		// strconv.Quote escapes the newlines, tabs and control characters an
		// attribute value may carry, so quoting covers both jobs at once.
		value = strconv.Quote(value)
	} else {
		value = escapeControl(value)
	}
	value = truncate(value, maxAttrValueRunes)
	sb.WriteString(key)
	sb.WriteByte('=')
	sb.WriteString(value)
	sb.WriteByte(' ')
}

// needsQuoting reports whether a value has to be quoted to stay readable on one
// line: anything with whitespace, a quote or an equals sign would otherwise run
// into the next attribute.
func needsQuoting(value string) bool {
	return strings.ContainsFunc(value, func(r rune) bool {
		return r <= ' ' || r == '"' || r == '=' || r == 0x7f
	})
}

// sourceOf resolves a record's call site to "file.go:line". The program counter
// is on every record, so the window reports the call site even in the production
// JSON log, which does not print it.
func sourceOf(pc uintptr) string {
	if pc == 0 {
		return ""
	}
	frame, _ := runtime.CallersFrames([]uintptr{pc}).Next()
	if frame.File == "" {
		return ""
	}
	return fmt.Sprintf("%s:%d", filepath.Base(frame.File), frame.Line)
}
