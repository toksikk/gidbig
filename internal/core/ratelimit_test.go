package gidbig

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/toksikk/gidbig/internal/cfg"
)

func TestIPRateLimiter_BurstThenDeny(t *testing.T) {
	l := newIPRateLimiter(3, 1)

	for i := 0; i < 3; i++ {
		if allowed, _, _ := l.allow("client"); !allowed {
			t.Fatalf("request %d denied inside burst", i+1)
		}
	}

	allowed, retryAfter, newlyBlocked := l.allow("client")
	if allowed {
		t.Fatal("fourth request allowed past burst limit")
	}
	if !newlyBlocked {
		t.Error("first rejection should report newlyBlocked")
	}
	if retryAfter <= 0 {
		t.Errorf("retryAfter = %v, want positive", retryAfter)
	}
}

func TestIPRateLimiter_RefillsOverTime(t *testing.T) {
	l := newIPRateLimiter(3, 2)
	now := time.Now()
	l.now = func() time.Time { return now }

	for i := 0; i < 3; i++ {
		l.allow("client")
	}
	if allowed, _, _ := l.allow("client"); allowed {
		t.Fatal("request allowed with an empty bucket")
	}

	now = now.Add(1500 * time.Millisecond) // 3 tokens' worth at 2/s, capped at burst 3
	for i := 0; i < 3; i++ {
		if allowed, _, _ := l.allow("client"); !allowed {
			t.Fatalf("request %d denied after refill", i+1)
		}
	}
}

func TestIPRateLimiter_RefillIsCappedAtBurst(t *testing.T) {
	l := newIPRateLimiter(2, 1)
	now := time.Now()
	l.now = func() time.Time { return now }

	l.allow("client")
	now = now.Add(time.Hour)
	for i := 0; i < 2; i++ {
		if allowed, _, _ := l.allow("client"); !allowed {
			t.Fatalf("request %d denied after long idle", i+1)
		}
	}
	if allowed, _, _ := l.allow("client"); allowed {
		t.Error("bucket refilled past burst size")
	}
}

func TestIPRateLimiter_KeysAreIndependent(t *testing.T) {
	l := newIPRateLimiter(1, 1)

	if allowed, _, _ := l.allow("a"); !allowed {
		t.Fatal("first client denied")
	}
	if allowed, _, _ := l.allow("a"); allowed {
		t.Fatal("first client not limited")
	}
	if allowed, _, _ := l.allow("b"); !allowed {
		t.Error("second client limited by the first client's bucket")
	}
}

func TestIPRateLimiter_LogsBlockOncePerEpisode(t *testing.T) {
	l := newIPRateLimiter(1, 1)
	now := time.Now()
	l.now = func() time.Time { return now }

	l.allow("client")
	if _, _, newlyBlocked := l.allow("client"); !newlyBlocked {
		t.Error("first rejection should report newlyBlocked")
	}
	if _, _, newlyBlocked := l.allow("client"); newlyBlocked {
		t.Error("repeat rejection should not report newlyBlocked")
	}

	now = now.Add(2 * time.Second)
	if allowed, _, _ := l.allow("client"); !allowed {
		t.Fatal("client not allowed after refill")
	}
	if _, _, newlyBlocked := l.allow("client"); !newlyBlocked {
		t.Error("rejection after a successful request should report newlyBlocked again")
	}
}

func TestIPRateLimiter_SweepsIdleBuckets(t *testing.T) {
	l := newIPRateLimiter(1, 1)
	now := time.Now()
	l.now = func() time.Time { return now }

	l.allow("stale-a")
	l.allow("stale-b")
	if len(l.buckets) != 2 {
		t.Fatalf("tracked buckets = %d, want 2", len(l.buckets))
	}

	now = now.Add(l.idleTTL + time.Hour)
	l.allow("fresh")

	if _, ok := l.buckets["stale-a"]; ok {
		t.Error("idle bucket stale-a not swept")
	}
	if _, ok := l.buckets["stale-b"]; ok {
		t.Error("idle bucket stale-b not swept")
	}
	if len(l.buckets) != 1 {
		t.Errorf("tracked buckets = %d, want 1", len(l.buckets))
	}
}

func TestIPRateLimiter_LongIdleClientIsAllowedAgain(t *testing.T) {
	l := newIPRateLimiter(2, 1)
	now := time.Now()
	l.now = func() time.Time { return now }

	for i := 0; i < 2; i++ {
		if allowed, _, _ := l.allow("client"); !allowed {
			t.Fatalf("request %d denied inside burst", i+1)
		}
	}
	if allowed, _, _ := l.allow("client"); allowed {
		t.Fatal("third request allowed past burst limit")
	}

	now = now.Add(l.idleTTL + time.Hour)
	for i := 0; i < 2; i++ {
		if allowed, _, _ := l.allow("client"); !allowed {
			t.Fatalf("request %d denied after a long idle period", i+1)
		}
	}
}

func TestIPRateLimiter_RejectsNewClientsWhenTableFull(t *testing.T) {
	var logs bytes.Buffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previousLogger) })

	l := newIPRateLimiter(1, 1)
	l.maxClients = 2
	now := time.Now()
	l.now = func() time.Time { return now }

	l.allow("a")
	l.allow("b")

	allowed, retryAfter, newlyBlocked := l.allow("c")
	if allowed {
		t.Fatal("new client allowed while the bucket table is full")
	}
	if retryAfter <= 0 {
		t.Errorf("retryAfter = %v, want positive", retryAfter)
	}
	if newlyBlocked {
		t.Error("capacity rejection should not report newlyBlocked")
	}
	if len(l.buckets) != 2 {
		t.Errorf("tracked buckets = %d, want 2", len(l.buckets))
	}

	// The table-full warning is logged once per episode, not per rejection.
	l.allow("d")
	if got := strings.Count(logs.String(), "rate limit table full"); got != 1 {
		t.Errorf("table-full log lines = %d, want 1", got)
	}
}

func TestIPRateLimiter_FullTableSweepsStaleBucketForNewClient(t *testing.T) {
	l := newIPRateLimiter(1, 1)
	l.maxClients = 1
	now := time.Now()
	l.now = func() time.Time { return now }

	l.allow("old")
	now = now.Add(l.idleTTL + time.Hour)

	if allowed, _, _ := l.allow("new"); !allowed {
		t.Fatal("new client denied even though the only tracked bucket was stale")
	}
	if _, ok := l.buckets["old"]; ok {
		t.Error("stale bucket survived the forced sweep")
	}
}

func TestConcurrencyLimiter_BoundsInFlight(t *testing.T) {
	c := newConcurrencyLimiter(2)

	first := c.tryAcquire()
	second := c.tryAcquire()
	if !first || !second {
		t.Fatalf("slots available inside the limit were rejected (first=%v second=%v)", first, second)
	}
	if c.tryAcquire() {
		t.Error("acquired a slot past the limit")
	}

	c.release()
	if !c.tryAcquire() {
		t.Error("released slot was not reusable")
	}
}

func TestResolveEsoLimits_Defaults(t *testing.T) {
	burst, refillPerSecond, maxConcurrent, timeout := resolveEsoLimits(cfg.EsoAPIConfig{})

	if burst != esoRateLimitBurst {
		t.Errorf("burst = %d, want %d", burst, esoRateLimitBurst)
	}
	if refillPerSecond != esoRateLimitPerSecond {
		t.Errorf("refillPerSecond = %v, want %v", refillPerSecond, esoRateLimitPerSecond)
	}
	if maxConcurrent != esoMaxConcurrentRequests {
		t.Errorf("maxConcurrent = %d, want %d", maxConcurrent, esoMaxConcurrentRequests)
	}
	if timeout != esoRequestTimeout {
		t.Errorf("timeout = %v, want %v", timeout, esoRequestTimeout)
	}
}

func TestResolveEsoLimits_Overrides(t *testing.T) {
	burst, refillPerSecond, maxConcurrent, timeout := resolveEsoLimits(cfg.EsoAPIConfig{
		RateLimitBurst:        2,
		RateLimitPerMinute:    120,
		MaxConcurrentRequests: 1,
		RequestTimeoutSeconds: 3,
	})

	if burst != 2 {
		t.Errorf("burst = %d, want 2", burst)
	}
	if refillPerSecond != 2 {
		t.Errorf("refillPerSecond = %v, want 2", refillPerSecond)
	}
	if maxConcurrent != 1 {
		t.Errorf("maxConcurrent = %d, want 1", maxConcurrent)
	}
	if timeout != 3*time.Second {
		t.Errorf("timeout = %v, want %v", timeout, 3*time.Second)
	}
}

func TestResolveEsoLimits_NonPositiveFallsBackToDefaults(t *testing.T) {
	burst, refillPerSecond, maxConcurrent, timeout := resolveEsoLimits(cfg.EsoAPIConfig{
		RateLimitBurst:        -1,
		RateLimitPerMinute:    -1,
		MaxConcurrentRequests: -1,
		RequestTimeoutSeconds: -1,
	})

	if burst != esoRateLimitBurst || refillPerSecond != esoRateLimitPerSecond ||
		maxConcurrent != esoMaxConcurrentRequests || timeout != esoRequestTimeout {
		t.Errorf("negative values did not fall back: burst=%d refill=%v concurrent=%d timeout=%v",
			burst, refillPerSecond, maxConcurrent, timeout)
	}
}

func TestClientIP(t *testing.T) {
	tests := []struct {
		remoteAddr string
		want       string
	}{
		{"192.0.2.1:1234", "192.0.2.1"},
		{"192.0.2.1", "192.0.2.1"},
		{"[2001:db8::1]:80", "2001:db8::1"},
		{"garbage", "garbage"},
	}
	for _, test := range tests {
		req := httptest.NewRequest(http.MethodGet, "/api/eso", nil)
		req.RemoteAddr = test.remoteAddr
		if got := clientIP(req); got != test.want {
			t.Errorf("clientIP(%q) = %q, want %q", test.remoteAddr, got, test.want)
		}
	}
}
