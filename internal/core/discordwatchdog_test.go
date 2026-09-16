package gidbig

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/gorilla/websocket"
)

func TestDiscordHealth(t *testing.T) {
	now := time.Now()
	s := &discordgo.Session{}
	h := discordHealth{session: s, started: now}
	timeout := time.Minute
	check := func(at time.Time, want string) {
		t.Helper()
		reason, _ := h.check(at, timeout)
		if !strings.Contains(reason, want) || (want == "" && reason != "") {
			t.Fatalf("reason = %q, want %q", reason, want)
		}
	}
	check(now, "") // Startup grace with no ACK yet.
	check(now.Add(timeout), "no gateway heartbeat progress")
	s.LastHeartbeatAck = now.Add(timeout)
	check(now.Add(timeout), "") // ACK, not chat activity, proves liveness.
	s.Lock()
	check(now.Add(timeout), "")
	check(now.Add(2*timeout), "session lock stalled")
	s.Unlock()
	s.LastHeartbeatAck = now.Add(2 * timeout)
	check(now.Add(2*timeout), "")
	s.Lock()
	check(now.Add(3*timeout), "") // A recovered lock resets the stall timer.
	s.Unlock()
}

func TestDiscordWatchdogCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	runDiscordWatchdog(ctx, &discordgo.Session{}, time.Millisecond, time.Millisecond, func() {
		t.Fatal("cancelled watchdog must not exit the process")
	})
}

func TestDiscordWatchdogStaleHeartbeat(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	exited := false
	runDiscordWatchdog(ctx, &discordgo.Session{LastHeartbeatAck: time.Now().Add(-time.Hour)},
		time.Millisecond, time.Minute, func() { exited = true })
	if !exited {
		t.Fatal("watchdog did not exit on stale heartbeat")
	}
}

func TestDiscordWatchdogBlockedSession(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	s := &discordgo.Session{}
	s.Lock()
	defer s.Unlock()
	exited := false
	runDiscordWatchdog(ctx, s, time.Millisecond, 5*time.Millisecond, func() { exited = true })
	if !exited {
		t.Fatal("watchdog did not exit while the session lock was blocked")
	}
}

// Exercise the production sequence against the actual fork. Isolate it in a
// subprocess so a dependency regression cannot deadlock the whole test suite.
// Success requires a new dispatch AND heartbeat ACK after reconnect, not a
// watchdog restart. Both resumable and non-resumable Op9 must recover.
func TestDiscordReconnectInvalidSessionDuringResume(t *testing.T) {
	if os.Getenv("GIDBIG_TEST_OP9_CHILD") == "1" {
		runInvalidSessionChild(t)
		return
	}
	for _, scenario := range []struct {
		name, resumable, opcode, log string
	}{
		{"invalid session requires identify", "false", "9", "Received Op 9 (Invalid Session) from gateway"},
		{"invalid session allows resume", "true", "9", "Received Op 9 (Invalid Session) from gateway"},
		{"reconnect during resume", "true", "7", "Received Op 7 (Reconnect) from gateway"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestDiscordReconnectInvalidSessionDuringResume$")
			cmd.Env = append(os.Environ(), "GIDBIG_TEST_OP9_CHILD=1", "GIDBIG_TEST_RESUMABLE="+scenario.resumable, "GIDBIG_TEST_OPCODE="+scenario.opcode)
			out, err := cmd.CombinedOutput()
			if ctx.Err() != nil {
				t.Fatalf("gateway reconnect hung: %s", out)
			}
			if err != nil {
				t.Fatalf("gateway reconnect failed: %v: %s", err, out)
			}
			for _, want := range []string{"gateway recovered with dispatch and heartbeat ACK", scenario.log} {
				if !strings.Contains(string(out), want) {
					t.Errorf("missing %q in child output: %s", want, out)
				}
			}
		})
	}
}

func runInvalidSessionChild(t *testing.T) {
	resumable := os.Getenv("GIDBIG_TEST_RESUMABLE") == "true"
	var connections atomic.Int32
	var recoverySent atomic.Int64
	messages := make(chan struct{}, 1)
	upgrader := websocket.Upgrader{}
	var gatewayURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/gateway" {
			_ = json.NewEncoder(w).Encode(map[string]string{"url": gatewayURL})
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		attempt := connections.Add(1)
		_ = conn.WriteJSON(map[string]any{"op": 10, "d": map[string]int{"heartbeat_interval": 41250}})
		var packet struct {
			Op int `json:"op"`
		}
		if err := conn.ReadJSON(&packet); err != nil {
			return
		}
		switch attempt {
		case 1:
			if packet.Op != 2 {
				return
			}
			_ = conn.WriteJSON(map[string]any{"op": 0, "s": 1, "t": "READY", "d": map[string]any{
				"session_id": "test-session", "resume_gateway_url": gatewayURL,
				"user": map[string]string{"id": "test-bot"},
			}})
			_ = conn.WriteJSON(map[string]any{"op": 7, "d": nil})
		case 2:
			if packet.Op != 6 {
				return
			}
			if os.Getenv("GIDBIG_TEST_OPCODE") == "7" {
				_ = conn.WriteJSON(map[string]any{"op": 7, "d": nil})
			} else {
				_ = conn.WriteJSON(map[string]any{"op": 9, "d": resumable})
			}
		case 3:
			if resumable {
				if packet.Op != 6 {
					return
				}
				_ = conn.WriteJSON(map[string]any{"op": 0, "s": 2, "t": "RESUMED", "d": map[string]any{}})
			} else {
				if packet.Op != 2 {
					return
				}
				_ = conn.WriteJSON(map[string]any{"op": 0, "s": 1, "t": "READY", "d": map[string]any{
					"session_id": "new-session", "resume_gateway_url": gatewayURL,
					"user": map[string]string{"id": "test-bot"},
				}})
			}
			recoverySent.Store(time.Now().UnixNano())
			_ = conn.WriteJSON(map[string]any{"op": 0, "s": 3, "t": "MESSAGE_CREATE", "d": map[string]string{"id": "recovered-message"}})
		default:
			return
		}
		for {
			if err := conn.ReadJSON(&packet); err != nil {
				return
			}
			if packet.Op == 1 {
				_ = conn.WriteJSON(map[string]any{"op": 11, "d": nil})
			}
		}
	}))
	defer server.Close()
	gatewayURL = "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"
	oldGateway := discordgo.EndpointGateway
	discordgo.EndpointGateway = server.URL + "/gateway"
	defer func() { discordgo.EndpointGateway = oldGateway }()
	s, err := discordgo.New("Bot fake-token")
	if err != nil {
		t.Fatal(err)
	}
	configureDiscordgoLogging(s, false)
	s.AddHandler(func(_ *discordgo.Session, m *discordgo.MessageCreate) {
		if m.ID == "recovered-message" {
			messages <- struct{}{}
		}
	})
	if err := s.Open(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-messages:
	case <-time.After(5 * time.Second):
		t.Fatal("no message dispatch after invalid session")
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if s.TryRLock() {
			ack := s.LastHeartbeatAck
			s.RUnlock()
			if ack.After(time.Unix(0, recoverySent.Load())) {
				fmt.Println("gateway recovered with dispatch and heartbeat ACK")
				os.Exit(0)
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("no heartbeat ACK after invalid session")
}
