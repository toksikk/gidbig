package gidbig

import (
	"context"
	"log/slog"
	"runtime"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
)

const (
	discordWatchdogInterval = 30 * time.Second
	discordWatchdogTimeout  = 5 * time.Minute
)

type discordHealth struct {
	session          *discordgo.Session
	started          time.Time
	lockBlockedSince time.Time
}

// check never waits for the session lock. The previous fork pin deadlocked
// when Open processed Op9 while holding this lock. Retain this independent
// check as a safety net: even monitoring RLock would hang on a future deadlock.
func (h *discordHealth) check(now time.Time, timeout time.Duration) (string, []any) {
	attrs := []any{"component", "discord_watchdog", "goroutines", runtime.NumGoroutine()}
	if !h.session.TryRLock() {
		if h.lockBlockedSince.IsZero() {
			h.lockBlockedSince = now
		}
		blocked := now.Sub(h.lockBlockedSince)
		attrs = append(attrs, "session_lock_available", false, "lock_blocked_ms", blocked.Milliseconds())
		if blocked >= timeout {
			return "session lock stalled (possible gateway reconnect deadlock)", attrs
		}
		return "", attrs
	}
	ack := h.session.LastHeartbeatAck
	h.session.RUnlock()
	h.lockBlockedSince = time.Time{}

	// LastHeartbeatSent is written under the fork's private wsMutex, so reading
	// it under the public session lock would race. ACK is protected by this lock.
	// Open seeds ACK on Hello; this is liveness, not a heartbeat latency metric.
	lastProgress := ack
	if lastProgress.IsZero() {
		lastProgress = h.started
	}
	age := now.Sub(lastProgress)
	attrs = append(attrs, "session_lock_available", true, "last_heartbeat_ack", ack,
		"heartbeat_ack_age_ms", age.Milliseconds())
	if age >= timeout {
		return "no gateway heartbeat progress", attrs
	}
	return "", attrs
}

// runDiscordWatchdog must start before Open and stop before normal shutdown.
// exit bypasses Close and background-task waits: they may need the stuck lock.
// The deployment's process supervisor must restart the resulting nonzero exit.
func runDiscordWatchdog(ctx context.Context, s *discordgo.Session, interval, timeout time.Duration, exit func()) {
	h := discordHealth{session: s, started: time.Now()}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			if ctx.Err() != nil {
				return
			}
			reason, attrs := h.check(now, timeout)
			if reason == "" {
				slog.Info("Discord gateway health", attrs...)
				continue
			}
			attrs = append(attrs, "reason", reason, "timeout_ms", timeout.Milliseconds())
			slog.Error("Discord gateway stalled; exiting for supervisor restart", attrs...)
			logGoroutineStacks()
			exit()
			return
		}
	}
}

func logGoroutineStacks() {
	// Bounded and split by goroutine to avoid one enormous log record. Unlike a
	// heap dump this contains call stacks, not message bodies or session tokens.
	buf := make([]byte, 1<<20)
	n := runtime.Stack(buf, true)
	for i, stack := range strings.Split(strings.TrimSpace(string(buf[:n])), "\n\n") {
		slog.Error("Discord stall goroutine stack", "component", "discord_watchdog",
			"index", i, "truncated", n == len(buf), "stack", stack)
	}
}
