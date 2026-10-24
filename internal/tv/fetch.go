package tv

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

const (
	feedURL       = "https://www.free-epg.de/api/epg/de.xml.gz"
	cacheFile     = "epg_de.xml.gz"
	metaFile      = "epg_de.meta.json"
	userAgent     = "gidbig (+https://github.com/toksikk/gidbig)"
	fetchTimeout  = 2 * time.Minute
	maxFeedBytes  = 64 << 20
	statusNotMod  = "304"
	statusUpdated = "200"
)

// cacheMeta is the sidecar holding the HTTP validators of the cached feed.
type cacheMeta struct {
	ETag         string    `json:"etag,omitempty"`
	LastModified string    `json:"last_modified,omitempty"`
	FetchedAt    time.Time `json:"fetched_at"`
}

// fetcher downloads the feed into dir with conditional GETs.
type fetcher struct {
	client *http.Client
	url    string
	dir    string
	now    func() time.Time
}

func newFetcher(dir string) *fetcher {
	return &fetcher{client: &http.Client{Timeout: fetchTimeout}, url: feedURL, dir: dir, now: time.Now}
}

func (f *fetcher) cachePath() string { return filepath.Join(f.dir, cacheFile) }
func (f *fetcher) metaPath() string  { return filepath.Join(f.dir, metaFile) }

// readMeta loads the sidecar; a missing or broken file yields the zero value.
func (f *fetcher) readMeta() cacheMeta {
	var m cacheMeta
	b, err := os.ReadFile(f.metaPath())
	if err != nil {
		return cacheMeta{}
	}
	if json.Unmarshal(b, &m) != nil {
		return cacheMeta{}
	}
	return m
}

func (f *fetcher) writeMeta(m cacheMeta) error {
	b, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("tv: encode cache meta: %w", err)
	}
	if err := os.WriteFile(f.metaPath(), b, 0o644); err != nil {
		return fmt.Errorf("tv: write cache meta: %w", err)
	}
	return nil
}

// cacheAge reports how old the cached feed is. ok is false without a cache.
func (f *fetcher) cacheAge() (time.Duration, bool) {
	st, err := os.Stat(f.cachePath())
	if err != nil {
		return 0, false
	}
	at := st.ModTime()
	if m := f.readMeta(); !m.FetchedAt.IsZero() {
		at = m.FetchedAt
	}
	return f.now().Sub(at), true
}

// fetch downloads the feed when it changed. It returns the HTTP status label
// ("200" or "304") and whether the cache file was replaced. On any error the
// existing cache is left untouched.
func (f *fetcher) fetch(ctx context.Context) (status string, changed bool, err error) {
	if err := os.MkdirAll(f.dir, 0o755); err != nil {
		return "", false, fmt.Errorf("tv: fetch epg: create cache dir: %w", err)
	}
	meta := f.readMeta()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.url, nil)
	if err != nil {
		return "", false, fmt.Errorf("tv: fetch epg: %w", err)
	}
	// An explicit Accept-Encoding stops the transport from decompressing, so
	// the gzip body is stored as received.
	req.Header.Set("Accept-Encoding", "gzip")
	req.Header.Set("User-Agent", userAgent)
	if _, err := os.Stat(f.cachePath()); err == nil {
		if meta.ETag != "" {
			req.Header.Set("If-None-Match", meta.ETag)
		}
		if meta.LastModified != "" {
			req.Header.Set("If-Modified-Since", meta.LastModified)
		}
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return "", false, fmt.Errorf("tv: fetch epg: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusNotModified:
		meta.FetchedAt = f.now()
		return statusNotMod, false, f.writeMeta(meta)
	case http.StatusOK:
	default:
		return "", false, fmt.Errorf("tv: fetch epg: unexpected status %d", resp.StatusCode)
	}

	if err := f.store(resp.Body); err != nil {
		return "", false, err
	}
	meta = cacheMeta{ETag: resp.Header.Get("ETag"), LastModified: resp.Header.Get("Last-Modified"), FetchedAt: f.now()}
	return statusUpdated, true, f.writeMeta(meta)
}

// store writes body gzip-compressed to a temp file and renames it over the cache.
func (f *fetcher) store(body io.Reader) (err error) {
	tmp, err := os.CreateTemp(f.dir, cacheFile+".*.tmp")
	if err != nil {
		return fmt.Errorf("tv: fetch epg: create temp file: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()

	lr := &io.LimitedReader{R: body, N: maxFeedBytes + 1}
	br := bufio.NewReader(lr)
	magic, _ := br.Peek(2)
	if len(magic) == 2 && magic[0] == 0x1f && magic[1] == 0x8b {
		if _, err = io.Copy(tmp, br); err != nil {
			return fmt.Errorf("tv: fetch epg: write cache: %w", err)
		}
	} else {
		zw := gzip.NewWriter(tmp)
		if _, err = io.Copy(zw, br); err != nil {
			return fmt.Errorf("tv: fetch epg: write cache: %w", err)
		}
		if err = zw.Close(); err != nil {
			return fmt.Errorf("tv: fetch epg: write cache: %w", err)
		}
	}
	if lr.N <= 0 {
		return errors.New("tv: fetch epg: feed exceeds size limit")
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("tv: fetch epg: close cache: %w", err)
	}
	if err = os.Rename(tmp.Name(), f.cachePath()); err != nil {
		return fmt.Errorf("tv: fetch epg: replace cache: %w", err)
	}
	return nil
}

// openCache returns a reader over the decompressed cached feed.
func (f *fetcher) openCache() (io.ReadCloser, error) {
	file, err := os.Open(f.cachePath())
	if err != nil {
		return nil, fmt.Errorf("tv: open cache: %w", err)
	}
	zr, err := gzip.NewReader(bufio.NewReader(file))
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("tv: open cache: %w", err)
	}
	return &gzipFile{Reader: zr, file: file}, nil
}

type gzipFile struct {
	*gzip.Reader
	file *os.File
}

func (g *gzipFile) Close() error {
	zerr := g.Reader.Close()
	ferr := g.file.Close()
	return errors.Join(zerr, ferr)
}
