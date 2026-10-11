package leetoclock

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/toksikk/gidbig/internal/tool"
)

func registerRecordTools(t *testing.T) *Module {
	t.Helper()
	m, _ := newTestModule(t)
	m.tools = tool.NewRegistry()
	m.registerTools()
	return m
}

func TestRegisterToolsRegistersRecords(t *testing.T) {
	m := registerRecordTools(t)

	if _, ok := m.tools.Get("leetoclock_records"); !ok {
		t.Fatal("leetoclock_records was not registered")
	}
}

func TestRecordsHandlerTop(t *testing.T) {
	m := registerRecordTools(t)
	addRecord(t, m, "guild-1", "chan-1", "alice", "m1", time.Now(), 137)

	def, _ := m.tools.Get("leetoclock_records")
	out, err := def.Handler(context.Background(), tool.Env{GuildID: "guild-1"}, json.RawMessage(`{"subcommand":"top"}`))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if !strings.Contains(out, "Leet o'Clock top") {
		t.Errorf("top output = %q", out)
	}
}

func TestRecordsHandlerPlayerDefaultsToCaller(t *testing.T) {
	m := registerRecordTools(t)
	addRecord(t, m, "guild-1", "chan-1", "alice", "m1", time.Now(), 137)

	def, _ := m.tools.Get("leetoclock_records")
	out, err := def.Handler(context.Background(), tool.Env{GuildID: "guild-1", UserID: "alice"}, json.RawMessage(`{"subcommand":"player"}`))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if !strings.Contains(out, "<@alice>") {
		t.Errorf("player output = %q", out)
	}
}

func TestRecordsHandlerRejectsUnknownSubcommand(t *testing.T) {
	m := registerRecordTools(t)

	def, _ := m.tools.Get("leetoclock_records")
	out, err := def.Handler(context.Background(), tool.Env{GuildID: "guild-1"}, json.RawMessage(`{"subcommand":"bogus"}`))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if !strings.Contains(out, "top") {
		t.Errorf("unknown subcommand output = %q", out)
	}
}

func TestRecordsHandlerRequiresGuild(t *testing.T) {
	m := registerRecordTools(t)

	def, _ := m.tools.Get("leetoclock_records")
	out, err := def.Handler(context.Background(), tool.Env{}, json.RawMessage(`{"subcommand":"top"}`))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if !strings.Contains(out, "no guild available") {
		t.Errorf("output = %q", out)
	}
}

func TestRecordsHandlerDate(t *testing.T) {
	m := registerRecordTools(t)
	m.now = func() time.Time { return time.Date(2026, time.September, 24, 13, 0, 0, 0, time.Local) }
	addRecord(t, m, "guild-1", "chan-1", "alice", "m-day", time.Date(2026, time.July, 8, 13, 37, 0, 0, time.Local), 137)
	addRecord(t, m, "guild-1", "chan-1", "alice", "m-other", time.Date(2026, time.July, 9, 13, 37, 0, 0, time.Local), 200)

	def, _ := m.tools.Get("leetoclock_records")
	out, err := def.Handler(context.Background(), tool.Env{GuildID: "guild-1"}, json.RawMessage(`{"subcommand":"top","date":"2026-07-08"}`))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if !strings.Contains(out, "137 ms") || !strings.Contains(out, "2026-07-08") {
		t.Errorf("date output = %q", out)
	}
	if strings.Contains(out, "200 ms") {
		t.Errorf("date output leaked another day: %q", out)
	}

	bad, err := def.Handler(context.Background(), tool.Env{GuildID: "guild-1"}, json.RawMessage(`{"subcommand":"top","date":"08.07.2026"}`))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if !strings.Contains(bad, "invalid date") {
		t.Errorf("invalid date output = %q", bad)
	}
}

func TestRegisterToolsNilRegistryIsSafe(t *testing.T) {
	m := New()
	m.registerTools()
}
