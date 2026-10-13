package gidbig

import (
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/toksikk/gidbig/internal/cfg"
)

// Limits guarding the LLM-backed /api/eso endpoint. The endpoint is
// unauthenticated and every allowed request costs provider tokens, so it is
// protected by a per-client token bucket (blunt request-rate cap) and a global
// concurrency cap (bounded in-flight generations) plus a per-request timeout.
const (
	// esoRateLimitBurst is how many requests one client may issue back to back
	// before its bucket empties.
	esoRateLimitBurst = 5
	// esoRateLimitPerSecond is the sustained per-client refill rate.
	esoRateLimitPerSecond = 0.5
	// esoRateLimitMaxClients caps tracked buckets so a flood of distinct source
	// addresses cannot grow the map without bound. New clients are rejected
	// while the table is full and no stale bucket can be evicted.
	esoRateLimitMaxClients = 4096
	// esoRateLimitSweepInterval bounds how often stale buckets are collected.
	esoRateLimitSweepInterval = time.Minute
	// esoRateLimitIdleTTL is how long an idle, refilled bucket is kept.
	esoRateLimitIdleTTL = 10 * time.Minute

	// esoMaxConcurrentRequests bounds simultaneous generations so a burst cannot
	// fan out unbounded provider calls (and hit provider rate limits that also
	// starve the gippity reply and classifier paths).
	esoMaxConcurrentRequests = 4
	// esoRequestTimeout bounds a single generation. On expiry the responder
	// falls back to the local generator rather than waiting on the provider.
	esoRequestTimeout = 15 * time.Second
)

// tokenBucket is a single client's bucket. blocked remembers that the client is
// currently rejected so the caller logs a block episode once, not once per
// rejected request.
type tokenBucket struct {
	tokens  float64
	last    time.Time
	blocked bool
}

// ipRateLimiter is a token bucket keyed by client address. now is injectable so
// tests can advance time without sleeping.
type ipRateLimiter struct {
	mu         sync.Mutex
	buckets    map[string]*tokenBucket
	burst      float64
	refillRate float64 // tokens per second
	maxClients int
	idleTTL    time.Duration
	sweepEvery time.Duration
	lastSweep  time.Time
	fullLogged bool
	now        func() time.Time
}

func newIPRateLimiter(burst int, refillPerSecond float64) *ipRateLimiter {
	return &ipRateLimiter{
		buckets:    make(map[string]*tokenBucket),
		burst:      float64(burst),
		refillRate: refillPerSecond,
		maxClients: esoRateLimitMaxClients,
		idleTTL:    esoRateLimitIdleTTL,
		sweepEvery: esoRateLimitSweepInterval,
		now:        time.Now,
	}
}

// allow consumes one token for key. It reports whether the request may proceed,
// how long the client should wait before retrying, and whether this call is the
// first rejection since the client was last allowed.
func (l *ipRateLimiter) allow(key string) (allowed bool, retryAfter time.Duration, newlyBlocked bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	l.sweepLocked(now, false)

	bucket, ok := l.buckets[key]
	if !ok {
		if len(l.buckets) >= l.maxClients {
			l.sweepLocked(now, true)
			if len(l.buckets) >= l.maxClients {
				if !l.fullLogged {
					l.fullLogged = true
					slog.Warn("ESO API rate limit table full; rejecting unknown clients", "max_clients", l.maxClients)
				}
				return false, l.waitFor(1), false
			}
		}
		bucket = &tokenBucket{tokens: l.burst, last: now}
		l.buckets[key] = bucket
		l.fullLogged = false
	} else {
		bucket.tokens = l.refilledTokens(bucket, now)
		bucket.last = now
	}

	if bucket.tokens < 1 {
		newlyBlocked = !bucket.blocked
		bucket.blocked = true
		return false, l.waitFor(1 - bucket.tokens), newlyBlocked
	}

	bucket.tokens--
	bucket.blocked = false
	return true, 0, false
}

// waitFor returns the time needed to refill missing tokens, floored at one
// second so the Retry-After header always names a positive delay.
func (l *ipRateLimiter) waitFor(missing float64) time.Duration {
	if l.refillRate <= 0 {
		return l.sweepEvery
	}
	wait := time.Duration(missing / l.refillRate * float64(time.Second))
	if wait < time.Second {
		return time.Second
	}
	return wait
}

// refilledTokens returns bucket's token count as of now without mutating it.
func (l *ipRateLimiter) refilledTokens(bucket *tokenBucket, now time.Time) float64 {
	elapsed := now.Sub(bucket.last).Seconds()
	if elapsed <= 0 {
		return bucket.tokens
	}
	return min(l.burst, bucket.tokens+elapsed*l.refillRate)
}

// sweepLocked drops buckets that are idle and would already be back at full
// burst. force ignores the interval, for the case where the table is full and a
// slot is needed.
func (l *ipRateLimiter) sweepLocked(now time.Time, force bool) {
	if !force && now.Sub(l.lastSweep) < l.sweepEvery {
		return
	}
	l.lastSweep = now
	for key, bucket := range l.buckets {
		if now.Sub(bucket.last) >= l.idleTTL && l.refilledTokens(bucket, now) >= l.burst {
			delete(l.buckets, key)
		}
	}
}

// concurrencyLimiter bounds simultaneous work with a non-blocking acquire:
// callers that find the limit saturated are rejected at once instead of piling
// up queued goroutines.
type concurrencyLimiter struct {
	slots chan struct{}
}

func newConcurrencyLimiter(n int) *concurrencyLimiter {
	return &concurrencyLimiter{slots: make(chan struct{}, n)}
}

func (c *concurrencyLimiter) tryAcquire() bool {
	select {
	case c.slots <- struct{}{}:
		return true
	default:
		return false
	}
}

func (c *concurrencyLimiter) release() { <-c.slots }

// resolveEsoLimits maps the optional web.eso config onto runtime limits. The
// built-in defaults apply whenever a field is omitted or non-positive, so the
// config section stays entirely optional.
func resolveEsoLimits(esoConfig cfg.EsoAPIConfig) (burst int, refillPerSecond float64, maxConcurrent int, timeout time.Duration) {
	burst = esoRateLimitBurst
	if esoConfig.RateLimitBurst > 0 {
		burst = esoConfig.RateLimitBurst
	}

	refillPerSecond = esoRateLimitPerSecond
	if esoConfig.RateLimitPerMinute > 0 {
		refillPerSecond = float64(esoConfig.RateLimitPerMinute) / 60
	}

	maxConcurrent = esoMaxConcurrentRequests
	if esoConfig.MaxConcurrentRequests > 0 {
		maxConcurrent = esoConfig.MaxConcurrentRequests
	}

	timeout = esoRequestTimeout
	if esoConfig.RequestTimeoutSeconds > 0 {
		timeout = time.Duration(esoConfig.RequestTimeoutSeconds) * time.Second
	}
	return burst, refillPerSecond, maxConcurrent, timeout
}

// clientIP returns the request's source address without its port. Only the
// transport address is used: X-Forwarded-For is attacker-controlled, and a
// reverse proxy in front is expected to enforce its own per-client limits.
func clientIP(r *http.Request) string {
	ip, _, _, err := parseIPPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ip.String()
}
