package tv

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeFeed serves a body with an ETag and answers If-None-Match with 304.
type fakeFeed struct {
	srv    *httptest.Server
	hits   atomic.Int64
	body   []byte
	status int
	inm    atomic.Value
}

func newFakeFeed(t *testing.T, body []byte) *fakeFeed {
	t.Helper()
	f := &fakeFeed{body: body, status: http.StatusOK}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.hits.Add(1)
		f.inm.Store(r.Header.Get("If-None-Match"))
		if f.status != http.StatusOK {
			w.WriteHeader(f.status)
			return
		}
		if r.Header.Get("If-None-Match") == `"v1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		w.Header().Set("Last-Modified", "Sat, 24 Oct 2026 19:29:40 GMT")
		_, _ = w.Write(f.body)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func newTestFetcher(t *testing.T, url string) *fetcher {
	t.Helper()
	f := newFetcher(t.TempDir(), "de")
	f.url = url
	f.now = func() time.Time { return fixtureNow }
	return f
}

func readCache(t *testing.T, f *fetcher) string {
	t.Helper()
	rc, err := f.openCache()
	if err != nil {
		t.Fatalf("open cache: %v", err)
	}
	defer func() { _ = rc.Close() }()
	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read cache: %v", err)
	}
	return string(b)
}

func TestFetch_StoresThenNotModified(t *testing.T) {
	feed := newFakeFeed(t, fixtureGzip(t))
	f := newTestFetcher(t, feed.srv.URL)

	status, changed, err := f.fetch(context.Background())
	if err != nil || status != "200" || !changed {
		t.Fatalf("first fetch = %q %v %v", status, changed, err)
	}
	if !strings.Contains(readCache(t, f), "Der Alte") {
		t.Error("cache content missing")
	}
	if m := f.readMeta(); m.ETag != `"v1"` || m.LastModified == "" || !m.FetchedAt.Equal(fixtureNow) {
		t.Errorf("meta = %+v", m)
	}
	if age, ok := f.cacheAge(); !ok || age != 0 {
		t.Errorf("cacheAge = %v %v", age, ok)
	}

	status, changed, err = f.fetch(context.Background())
	if err != nil || status != "304" || changed {
		t.Fatalf("second fetch = %q %v %v", status, changed, err)
	}
	if got := feed.inm.Load(); got != `"v1"` {
		t.Errorf("If-None-Match = %v", got)
	}
}

func TestFetch_ErrorKeepsCache(t *testing.T) {
	feed := newFakeFeed(t, fixtureGzip(t))
	f := newTestFetcher(t, feed.srv.URL)
	if _, _, err := f.fetch(context.Background()); err != nil {
		t.Fatalf("seed: %v", err)
	}
	before, _ := os.ReadFile(f.cachePath())
	feed.status = http.StatusInternalServerError
	if _, _, err := f.fetch(context.Background()); err == nil {
		t.Fatal("500 returned no error")
	}
	after, _ := os.ReadFile(f.cachePath())
	if !bytes.Equal(before, after) {
		t.Error("cache changed after failed fetch")
	}
}

func TestFetch_CompressesPlainBody(t *testing.T) {
	feed := newFakeFeed(t, []byte(fixtureXML(t)))
	f := newTestFetcher(t, feed.srv.URL)
	if _, _, err := f.fetch(context.Background()); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	raw, _ := os.ReadFile(f.cachePath())
	if len(raw) < 2 || raw[0] != 0x1f || raw[1] != 0x8b {
		t.Fatal("cache is not gzip")
	}
	if !strings.Contains(readCache(t, f), "Tatort") {
		t.Error("decompressed cache missing content")
	}
}

func TestStore_Oversized(t *testing.T) {
	f := newFetcher(t.TempDir(), "de")
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, _ = zw.Write([]byte("x"))
	_ = zw.Close()
	big := io.MultiReader(bytes.NewReader(buf.Bytes()), io.LimitReader(zeroReader{}, maxFeedBytes))
	if err := f.store(big); err == nil {
		t.Fatal("oversized body stored")
	}
	if _, err := os.Stat(f.cachePath()); !os.IsNotExist(err) {
		t.Errorf("cache exists after oversized body: %v", err)
	}
	entries, _ := os.ReadDir(f.dir)
	if len(entries) != 0 {
		t.Errorf("temp files left: %d", len(entries))
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}
