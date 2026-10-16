package logbuf

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

func line(message string, level slog.Level) Line {
	return Line{Time: time.Date(2026, 10, 16, 9, 12, 33, 123000000, time.UTC), Level: level, Message: message}
}

// TestBuffer_KeepsTheNewestLines pins the ring's order and its bound: a full
// buffer drops the oldest line, and Snapshot is oldest first.
func TestBuffer_KeepsTheNewestLines(t *testing.T) {
	buf := New(3)
	for _, msg := range []string{"one", "two", "three", "four"} {
		buf.Add(line(msg, slog.LevelInfo))
	}

	got := buf.Snapshot()
	if len(got) != 3 {
		t.Fatalf("Snapshot() returned %d lines, want 3", len(got))
	}
	var messages []string
	for _, l := range got {
		messages = append(messages, l.Message)
	}
	if strings.Join(messages, ",") != "two,three,four" {
		t.Errorf("held lines = %v, want two,three,four", messages)
	}
	if buf.Len() != 3 || buf.Capacity() != 3 {
		t.Errorf("Len()/Capacity() = %d/%d, want 3/3", buf.Len(), buf.Capacity())
	}
	if buf.Evicted() != 1 {
		t.Errorf("Evicted() = %d, want 1", buf.Evicted())
	}
}

// TestBuffer_SnapshotIsACopy makes sure a caller cannot write into the ring
// through the slice it was handed.
func TestBuffer_SnapshotIsACopy(t *testing.T) {
	buf := New(2)
	buf.Add(line("one", slog.LevelInfo))
	snap := buf.Snapshot()
	snap[0].Message = "changed"

	if got := buf.Snapshot()[0].Message; got != "one" {
		t.Errorf("stored line = %q, want %q", got, "one")
	}
}

func TestNew_DefaultCapacity(t *testing.T) {
	if got := New(0).Capacity(); got != DefaultCapacity {
		t.Errorf("New(0).Capacity() = %d, want %d", got, DefaultCapacity)
	}
	if got := New(-5).Capacity(); got != DefaultCapacity {
		t.Errorf("New(-5).Capacity() = %d, want %d", got, DefaultCapacity)
	}
}

// TestBuffer_ConcurrentAdd moves the ring from several goroutines: the race
// detector is the assertion, and the counts have to survive it.
func TestBuffer_ConcurrentAdd(t *testing.T) {
	buf := New(32)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				buf.Add(line("message", slog.LevelInfo))
			}
		}()
	}
	wg.Wait()

	if buf.Len() != 32 {
		t.Errorf("Len() = %d, want the capacity 32", buf.Len())
	}
	if buf.Evicted() != 400-32 {
		t.Errorf("Evicted() = %d, want %d", buf.Evicted(), 400-32)
	}
	if got := len(buf.Snapshot()); got != 32 {
		t.Errorf("Snapshot() returned %d lines, want 32", got)
	}
}

// TestHandler_StoresAndForwards covers the handler's two jobs at once: the
// record has to reach the wrapped handler and the buffer.
func TestHandler_StoresAndForwards(t *testing.T) {
	var outer bytes.Buffer
	buf := New(4)
	logger := slog.New(NewHandler(slog.NewJSONHandler(&outer, &slog.HandlerOptions{Level: slog.LevelWarn}), buf))

	logger.Warn("kept", "key", "value")
	logger.Info("dropped: below the wrapped handler's level")

	if !strings.Contains(outer.String(), `"msg":"kept"`) {
		t.Errorf("wrapped handler did not receive the record: %q", outer.String())
	}
	if strings.Contains(outer.String(), "dropped") {
		t.Errorf("the wrapped handler's level was not honoured: %q", outer.String())
	}
	got := buf.Snapshot()
	if len(got) != 1 {
		t.Fatalf("buffer holds %d lines, want 1 (Enabled must decide for both)", len(got))
	}
	if got[0].Message != "kept" || got[0].Level != slog.LevelWarn || got[0].Attrs != "key=value" {
		t.Errorf("stored line = %+v", got[0])
	}
}

// TestHandler_CapturesSource pins that the window reports the call site even
// when the wrapped handler is not configured to print it.
func TestHandler_CapturesSource(t *testing.T) {
	buf := New(1)
	logger := slog.New(NewHandler(nil, buf))
	logger.Info("with source")

	src := buf.Snapshot()[0].Source
	if !strings.HasPrefix(src, "logbuf_test.go:") {
		t.Errorf("Source = %q, want this test file", src)
	}
}

// TestHandler_WithAttrsAndGroups checks that attributes attached to the logger
// and group names appear on the stored line the way the wrapped handler spells
// them.
func TestHandler_WithAttrsAndGroups(t *testing.T) {
	buf := New(1)
	logger := slog.New(NewHandler(nil, buf)).
		With("component", "test").
		WithGroup("gateway").
		With("shard", 2)

	logger.Info("hello", slog.Group("event", slog.String("type", "READY")), "plain", "a b")

	attrs := buf.Snapshot()[0].Attrs
	for _, want := range []string{"component=test", "gateway.shard=2", "gateway.event.type=READY", `plain="a b"`} {
		if !strings.Contains(attrs, want) {
			t.Errorf("attrs %q is missing %q", attrs, want)
		}
	}
}

// TestHandler_EmptyAttrIsDropped matches slog's own handlers: an attribute with
// no key and no value carries nothing and is not rendered.
func TestHandler_EmptyAttrIsDropped(t *testing.T) {
	buf := New(1)
	logger := slog.New(NewHandler(nil, buf))
	logger.Info("bare", slog.Attr{})

	if got := buf.Snapshot()[0].Attrs; got != "" {
		t.Errorf("Attrs = %q, want empty", got)
	}
}

// TestHandler_EmptyGroupNameIsANoOp follows the slog contract: WithGroup("")
// returns the same handler.
func TestHandler_EmptyGroupNameIsANoOp(t *testing.T) {
	h := NewHandler(nil, New(1))
	if h.WithGroup("") != h {
		t.Error("WithGroup(\"\") returned a different handler")
	}
	if h.WithAttrs(nil) != h {
		t.Error("WithAttrs(nil) returned a different handler")
	}
}

// TestHandler_EnabledWithoutInnerHandler keeps the collect-only mode usable.
func TestHandler_EnabledWithoutInnerHandler(t *testing.T) {
	h := NewHandler(nil, New(1))
	if !h.Enabled(context.Background(), slog.LevelError) {
		t.Error("a handler without an inner handler must accept every level")
	}
	if err := h.Handle(context.Background(), slog.Record{}); err != nil {
		t.Errorf("Handle() = %v, want nil", err)
	}
}

// TestLine_SanitizeKeepsOneLinePerRecord pins that a multi-line message and
// control characters cannot break the window's one-line-per-record rendering.
func TestLine_SanitizeKeepsOneLinePerRecord(t *testing.T) {
	buf := New(1)
	buf.Add(Line{
		Time:    time.Now(),
		Level:   slog.LevelInfo,
		Message: "first\nsecond\ttabbed\r\nthird\x07bell",
		Attrs:   "key=a\nb",
		Source:  "a.go:1\ndropped",
	})

	got := buf.Snapshot()[0]
	if strings.ContainsAny(got.Message, "\n\r	\x07") {
		t.Errorf("message still carries control characters: %q", got.Message)
	}
	// A CRLF keeps both escapes: the window reports what the record contained
	// instead of pretending the carriage return was not there.
	if want := `first\nsecond tabbed\r\nthirdbell`; got.Message != want {
		t.Errorf("message = %q, want %q", got.Message, want)
	}
	if want := `key=a\nb`; got.Attrs != want {
		t.Errorf("attrs = %q, want %q", got.Attrs, want)
	}
	if want := `a.go:1\ndropped`; got.Source != want {
		t.Errorf("source = %q, want %q", got.Source, want)
	}
	if strings.Contains(got.String(), "\n") {
		t.Errorf("rendered line is not single-line: %q", got.String())
	}
}

// TestLine_TruncatesLongValues bounds one record's footprint in the window.
func TestLine_TruncatesLongValues(t *testing.T) {
	buf := New(1)
	buf.Add(Line{
		Time:    time.Now(),
		Level:   slog.LevelInfo,
		Message: strings.Repeat("m", MaxLineRunes+50),
		Attrs:   strings.Repeat("a", MaxLineRunes+50),
	})

	got := buf.Snapshot()[0]
	for name, value := range map[string]string{"message": got.Message, "attrs": got.Attrs} {
		if runes := len([]rune(value)); runes != MaxLineRunes+1 {
			t.Errorf("%s is %d runes, want %d (cut plus the ellipsis)", name, runes, MaxLineRunes+1)
		}
		if !strings.HasSuffix(value, truncationMark) {
			t.Errorf("%s was cut without a marker", name)
		}
	}
}

// TestHandler_TruncatesOneLongAttributeValue keeps a single oversized value from
// eating the whole per-line budget.
func TestHandler_TruncatesOneLongAttributeValue(t *testing.T) {
	buf := New(1)
	slog.New(NewHandler(nil, buf)).Info("payload", "body", strings.Repeat("a", maxAttrValueRunes+50))

	attrs := buf.Snapshot()[0].Attrs
	value := strings.TrimPrefix(attrs, "body=")
	if runes := len([]rune(value)); runes != maxAttrValueRunes+1 {
		t.Errorf("value is %d runes, want %d", runes, maxAttrValueRunes+1)
	}
}

// TestLine_StringRendersEveryPart ties the rendered line to the fields both
// surfaces show.
func TestLine_StringRendersEveryPart(t *testing.T) {
	got := Line{
		Time:    time.Date(2026, 10, 16, 9, 12, 33, 123000000, time.UTC),
		Level:   slog.LevelWarn,
		Message: "something happened",
		Attrs:   "key=value",
		Source:  "cmd.go:42",
	}.String()

	want := "2026-10-16 09:12:33.123 WARN something happened key=value (cmd.go:42)"
	if got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

// TestLine_StringWithoutOptionalParts keeps the empty attribute list and an
// unknown source from rendering as empty pieces.
func TestLine_StringWithoutOptionalParts(t *testing.T) {
	got := line("plain", slog.LevelError).String()
	if want := "2026-10-16 09:12:33.123 ERROR plain"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

func TestLevelLabel(t *testing.T) {
	cases := map[slog.Level]string{
		slog.LevelDebug - 4: "DEBUG",
		slog.LevelDebug:     "DEBUG",
		slog.LevelInfo - 1:  "DEBUG",
		slog.LevelInfo:      "INFO",
		slog.LevelWarn:      "WARN",
		slog.LevelError:     "ERROR",
		slog.LevelError + 4: "ERROR",
	}
	for level, want := range cases {
		if got := LevelLabel(level); got != want {
			t.Errorf("LevelLabel(%d) = %q, want %q", level, got, want)
		}
	}
}

// errTestError is logged as an attribute value to cover the common error-attr
// case that every handler formats for itself.
var errTestError = errors.New("boom\nsecond line")

func TestHandler_ErrorAttributeIsFlattened(t *testing.T) {
	buf := New(1)
	slog.New(NewHandler(nil, buf)).Error("failed", "error", errTestError)

	attrs := buf.Snapshot()[0].Attrs
	if want := `error="boom\nsecond line"`; attrs != want {
		t.Errorf("Attrs = %q, want %q", attrs, want)
	}
}

// TestHandler_JSONHandlerStillReceivesTheRecord guards the delegation against a
// handler wrapper that swallows records.
func TestHandler_JSONHandlerStillReceivesTheRecord(t *testing.T) {
	var outer bytes.Buffer
	buf := New(1)
	logger := slog.New(NewHandler(slog.NewJSONHandler(&outer, nil), buf))
	logger.Info("message", "count", 7)

	var decoded map[string]any
	if err := json.Unmarshal(outer.Bytes(), &decoded); err != nil {
		t.Fatalf("the wrapped JSON handler produced invalid output: %v", err)
	}
	if decoded["msg"] != "message" || decoded["count"] != float64(7) {
		t.Errorf("decoded record = %v", decoded)
	}
	if got := buf.Snapshot()[0].Attrs; got != "count=7" {
		t.Errorf("Attrs = %q, want %q", got, "count=7")
	}
}
