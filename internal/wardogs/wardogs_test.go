package wardogs

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

func newTestModule() *Module {
	return &Module{
		fetchFn: func(context.Context) (string, error) { return "NO", nil },
		now:     time.Now,
	}
}

func TestFormatStatus(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		contains string
	}{
		{name: "yes", raw: "YES", contains: "✅"},
		{name: "no", raw: "NO", contains: "❌"},
		{name: "lowercase and padded", raw: "  yes ", contains: "✅"},
		{name: "unknown", raw: "MAYBE", contains: "Unknown"},
		{name: "empty", raw: "", contains: "Unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatStatus(tt.raw)
			if !strings.Contains(got, tt.contains) {
				t.Errorf("formatStatus(%q) = %q, want it to contain %q", tt.raw, got, tt.contains)
			}
		})
	}
}

func TestFormatStatusListsSourcesWithoutPreviews(t *testing.T) {
	got := formatStatus("NO")

	for _, url := range []string{SiteURL, SteamDiscussionsURL} {
		if !strings.Contains(got, "<"+url+">") {
			t.Errorf("formatStatus = %q, want %q wrapped in angle brackets", got, url)
		}
	}

	// Angle brackets around every URL are what suppresses Discord's link preview.
	if bare, wrapped := strings.Count(got, "https://"), strings.Count(got, "<https://"); bare != wrapped {
		t.Errorf("formatStatus = %q, want all %d URLs wrapped in angle brackets", got, bare)
	}

	// The pinned Linux statement must not be cited as a standing source: it goes
	// stale as soon as the verdict flips to YES.
	if strings.Contains(got, "588436698284962639") {
		t.Errorf("formatStatus = %q, want no pinned statement link", got)
	}
	if !strings.Contains(got, "check manually") {
		t.Errorf("formatStatus = %q, want the forum link labelled for a manual check", got)
	}
}

func TestFormatStatusUnknownEchoesRawValue(t *testing.T) {
	got := formatStatus("MAYBE")

	if !strings.Contains(got, "`MAYBE`") {
		t.Errorf("formatStatus = %q, want the raw tracker value", got)
	}
}

func TestDisplayStatusSanitisesTrackerValue(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "plain value kept", raw: "NO", want: "NO"},
		{name: "backticks dropped", raw: "`NO`", want: "NO"},
		{name: "newlines and tabs flattened", raw: "NO\n\nYES\t", want: "NO YES"},
		{name: "control characters dropped", raw: "NO\x00\x1b[31m", want: "NO[31m"},
		{name: "unreadable value", raw: "\n\t", want: "unreadable"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := displayStatus(tt.raw); got != tt.want {
				t.Errorf("displayStatus(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestDisplayStatusCapsLongValue(t *testing.T) {
	got := displayStatus(strings.Repeat("A", 2000))

	if runes := []rune(got); len(runes) > maxStatusDisplayLen+1 {
		t.Errorf("displayStatus kept %d runes, want at most %d", len(runes), maxStatusDisplayLen+1)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("displayStatus = %q, want a truncation marker", got)
	}
}

// A tracker value that fills the whole response body must not push the reply past
// Discord's 2000-character content limit: an over-long edit is rejected and the
// deferred interaction would stay unanswered for the whole cache TTL.
func TestFormatStatusStaysWithinDiscordLimit(t *testing.T) {
	for _, raw := range []string{
		"NO",
		"YES",
		strings.Repeat("A", 2000),
		strings.Repeat("Ä", maxBodyBytes),
	} {
		got := formatStatus(raw)
		if runes := []rune(got); len(runes) > maxContentRunes {
			t.Errorf("formatStatus of a %d-byte status produced %d runes, want at most %d",
				len(raw), len(runes), maxContentRunes)
		}
	}
}

func TestClampContentLeavesShortContentUntouched(t *testing.T) {
	in := formatStatus("NO")
	if got := clampContent(in); got != in {
		t.Errorf("clampContent changed a short message: %q", got)
	}
}

func TestStatusCachedUsesCacheWithinTTL(t *testing.T) {
	m := newTestModule()
	now := time.Date(2026, 9, 17, 8, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now }
	calls := 0
	m.fetchFn = func(context.Context) (string, error) {
		calls++
		return "NO", nil
	}

	for i := 0; i < 3; i++ {
		got, err := m.statusCached(context.Background())
		if err != nil {
			t.Fatalf("statusCached returned error: %v", err)
		}
		if got != "NO" {
			t.Fatalf("statusCached = %q, want %q", got, "NO")
		}
	}

	if calls != 1 {
		t.Errorf("fetch called %d times, want 1 within the cache TTL", calls)
	}
}

func TestStatusCachedRefetchesAfterTTL(t *testing.T) {
	m := newTestModule()
	now := time.Date(2026, 9, 17, 8, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now }
	calls := 0
	m.fetchFn = func(context.Context) (string, error) {
		calls++
		if calls == 1 {
			return "NO", nil
		}
		return "YES", nil
	}

	if _, err := m.statusCached(context.Background()); err != nil {
		t.Fatalf("first statusCached returned error: %v", err)
	}
	now = now.Add(statusCacheTTL + time.Second)
	got, err := m.statusCached(context.Background())
	if err != nil {
		t.Fatalf("second statusCached returned error: %v", err)
	}
	if got != "YES" {
		t.Errorf("statusCached after TTL = %q, want %q", got, "YES")
	}
	if calls != 2 {
		t.Errorf("fetch called %d times, want 2 after the cache expired", calls)
	}
}

func TestStatusCachedDoesNotCacheErrors(t *testing.T) {
	m := newTestModule()
	calls := 0
	m.fetchFn = func(context.Context) (string, error) {
		calls++
		if calls == 1 {
			return "", errors.New("boom")
		}
		return "NO", nil
	}

	if _, err := m.statusCached(context.Background()); err == nil {
		t.Fatal("expected an error from the first statusCached call")
	}
	got, err := m.statusCached(context.Background())
	if err != nil {
		t.Fatalf("second statusCached returned error: %v", err)
	}
	if got != "NO" {
		t.Errorf("statusCached = %q, want %q", got, "NO")
	}
}

// The concurrency tests run inside a synctest bubble. synctest.Wait returns once
// every caller is durably blocked — fetching or waiting on the in-flight call — so
// no caller can be "late" and the assertion does not depend on the scheduler,
// GOMAXPROCS or timing.
func TestStatusCachedSingleFlightOnColdCache(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := newTestModule()
		var mu sync.Mutex
		calls := 0
		release := make(chan struct{})
		m.fetchFn = func(context.Context) (string, error) {
			mu.Lock()
			calls++
			mu.Unlock()
			<-release
			return "NO", nil
		}

		const callers = 12
		results := make([]string, callers)
		errs := make([]error, callers)
		var wg sync.WaitGroup
		for i := 0; i < callers; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				results[idx], errs[idx] = m.statusCached(context.Background())
			}(i)
		}

		synctest.Wait()
		if got := fetchCount(&mu, &calls); got != 1 {
			t.Errorf("fetch called %d times while %d callers were queued, want 1", got, callers)
		}

		close(release)
		wg.Wait()

		if got := fetchCount(&mu, &calls); got != 1 {
			t.Errorf("fetch called %d times for %d concurrent callers, want 1", got, callers)
		}
		for i := range results {
			if errs[i] != nil {
				t.Errorf("caller %d returned error: %v", i, errs[i])
			}
			if results[i] != "NO" {
				t.Errorf("caller %d got %q, want %q", i, results[i], "NO")
			}
		}
	})
}

func TestStatusCachedSingleFlightSharesErrorAndRetries(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := newTestModule()
		var mu sync.Mutex
		calls := 0
		release := make(chan struct{})
		m.fetchFn = func(context.Context) (string, error) {
			mu.Lock()
			calls++
			mu.Unlock()
			<-release
			return "", errors.New("boom")
		}

		const callers = 6
		errs := make([]error, callers)
		var wg sync.WaitGroup
		for i := 0; i < callers; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				_, errs[idx] = m.statusCached(context.Background())
			}(i)
		}

		synctest.Wait()
		close(release)
		wg.Wait()

		if got := fetchCount(&mu, &calls); got != 1 {
			t.Errorf("fetch called %d times, want 1", got)
		}
		for i := range errs {
			if errs[i] == nil {
				t.Errorf("caller %d expected the shared fetch error", i)
			}
		}

		// A failed fetch is not cached, and the failed call must not stay in flight:
		// the next call retries instead of every later caller reusing the failure.
		m.fetchFn = func(context.Context) (string, error) {
			mu.Lock()
			calls++
			mu.Unlock()
			return "NO", nil
		}
		got, err := m.statusCached(context.Background())
		if err != nil {
			t.Fatalf("retry after a failed fetch returned error: %v", err)
		}
		if got != "NO" {
			t.Errorf("retry after a failed fetch = %q, want %q", got, "NO")
		}
		if n := fetchCount(&mu, &calls); n != 2 {
			t.Errorf("fetch called %d times, want 2 after the retry", n)
		}
	})
}

func TestStatusCachedWaiterHonoursContext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := newTestModule()
		var mu sync.Mutex
		calls := 0
		release := make(chan struct{})
		m.fetchFn = func(context.Context) (string, error) {
			mu.Lock()
			calls++
			mu.Unlock()
			<-release
			return "NO", nil
		}

		fetching := make(chan struct{})
		go func() {
			defer close(fetching)
			_, _ = m.statusCached(context.Background())
		}()
		synctest.Wait()

		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := m.statusCached(ctx); !errors.Is(err, context.Canceled) {
			t.Errorf("waiter error = %v, want context.Canceled", err)
		}
		if got := fetchCount(&mu, &calls); got != 1 {
			t.Errorf("fetch called %d times, want 1", got)
		}

		close(release)
		<-fetching

		m.mu.Lock()
		inflight := m.inflight
		m.mu.Unlock()
		if inflight != nil {
			t.Error("in-flight call was not cleared")
		}
	})
}

func fetchCount(mu *sync.Mutex, calls *int) int {
	mu.Lock()
	defer mu.Unlock()
	return *calls
}

func TestComposeStatusPropagatesError(t *testing.T) {
	m := newTestModule()
	m.fetchFn = func(context.Context) (string, error) { return "", errors.New("boom") }

	if _, err := m.composeStatus(context.Background()); err == nil {
		t.Fatal("expected composeStatus to propagate the fetch error")
	}
}

func TestComposeStatusFormatsFetchResult(t *testing.T) {
	m := newTestModule()

	got, err := m.composeStatus(context.Background())
	if err != nil {
		t.Fatalf("composeStatus returned error: %v", err)
	}
	if !strings.Contains(got, "❌") {
		t.Errorf("composeStatus = %q, want a negative verdict", got)
	}
}

func TestComposeStatusBoundsOversizedTrackerValue(t *testing.T) {
	m := newTestModule()
	m.fetchFn = func(context.Context) (string, error) {
		return strings.Repeat("A", 2000) + "`\n", nil
	}

	got, err := m.composeStatus(context.Background())
	if err != nil {
		t.Fatalf("composeStatus returned error: %v", err)
	}
	if runes := []rune(got); len(runes) > maxContentRunes {
		t.Errorf("composeStatus produced %d runes, want at most %d", len(runes), maxContentRunes)
	}
	if got := strings.Count(got, "`"); got != 2 {
		t.Errorf("composeStatus kept %d backticks, want only the 2 of the code span", got)
	}
}

func TestFetchStatusFromParsesJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Accept"); got != "application/json" {
			t.Errorf("Accept header = %q, want application/json", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"NO"}`))
	}))
	defer server.Close()

	got, err := fetchStatusFrom(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("fetchStatusFrom returned error: %v", err)
	}
	if got != "NO" {
		t.Errorf("fetchStatusFrom = %q, want %q", got, "NO")
	}
}

func TestFetchStatusFromErrors(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    string
		wantErr string
	}{
		{name: "server error", status: http.StatusInternalServerError, body: `{"status":"NO"}`, wantErr: "unexpected status code 500"},
		{name: "malformed json", status: http.StatusOK, body: `{"status":`, wantErr: "decode status response"},
		{name: "empty status", status: http.StatusOK, body: `{"status":"  "}`, wantErr: "did not contain a status value"},
		{name: "html body", status: http.StatusOK, body: `<html></html>`, wantErr: "decode status response"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()

			_, err := fetchStatusFrom(context.Background(), server.URL)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestFetchStatusFromHonoursContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"NO"}`))
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := fetchStatusFrom(ctx, server.URL); err == nil {
		t.Fatal("expected a cancelled context to fail the request")
	}
}

func TestFetchStatusFromRejectsUnreachableEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := server.URL
	server.Close()

	if _, err := fetchStatusFrom(context.Background(), url); err == nil {
		t.Fatal("expected an error for an unreachable endpoint")
	}
}
