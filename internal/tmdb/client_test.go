package tmdb

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const testToken = "secret-test-token"

// stubServer serves canned TMDB responses and records each request.
type stubServer struct {
	*httptest.Server
	requests atomic.Int32
	last     atomic.Pointer[http.Request]
}

func newStub(t *testing.T, h http.HandlerFunc) *stubServer {
	t.Helper()
	s := &stubServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.requests.Add(1)
		s.last.Store(r)
		h(w, r)
	}))
	t.Cleanup(s.Close)
	return s
}

func testClient(base string) *Client {
	return &Client{base: base, token: testToken, http: &http.Client{Timeout: time.Second}}
}

const multiJSON = `{"results":[
 {"media_type":"tv","id":70523,"name":"Dark","first_air_date":"2017-12-01"},
 {"media_type":"person","id":1,"name":"Some Person"},
 {"media_type":"movie","id":2,"title":"Dark Movie","release_date":"2005-01-01"}]}`

const detailsJSON = `{"id":70523,"name":"Dark","first_air_date":"2017-12-01",
 "watch/providers":{"results":{"DE":{"link":"https://www.themoviedb.org/tv/70523/watch?locale=DE",
  "flatrate":[{"provider_id":9,"provider_name":"Amazon","display_priority":5},{"provider_id":8,"provider_name":"Netflix","display_priority":1}],
  "ads":[{"provider_id":300,"provider_name":"Pluto","display_priority":2}],
  "rent":[{"provider_id":2,"provider_name":"Apple TV","display_priority":1}],
  "buy":[{"provider_id":3,"provider_name":"Google Play","display_priority":1}]}}}}`

func TestClient_SendsBearerAndLanguage(t *testing.T) {
	srv := newStub(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(multiJSON)) })
	got, err := testClient(srv.URL).SearchMulti(context.Background(), "Dark", "de-DE")
	if err != nil {
		t.Fatalf("SearchMulti: %v", err)
	}
	r := srv.last.Load()
	if h := r.Header.Get("Authorization"); h != "Bearer "+testToken {
		t.Errorf("Authorization = %q", h)
	}
	if strings.Contains(r.URL.String(), testToken) {
		t.Errorf("token leaked into URL %q", r.URL)
	}
	if r.URL.Path != "/search/multi" || r.URL.Query().Get("language") != "de-DE" || r.URL.Query().Get("include_adult") != "false" || r.URL.Query().Get("query") != "Dark" {
		t.Errorf("unexpected request %q", r.URL)
	}
	if len(got) != 2 {
		t.Fatalf("matches = %+v, want persons dropped", got)
	}
	if got[0] != (Match{Kind: kindTV, ID: 70523, Title: "Dark", Year: "2017"}) || got[1] != (Match{Kind: kindMovie, ID: 2, Title: "Dark Movie", Year: "2005"}) {
		t.Errorf("matches = %+v", got)
	}
}

func TestClient_SearchYearFilter(t *testing.T) {
	srv := newStub(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"results":[]}`)) })
	c := testClient(srv.URL)
	if _, err := c.Search(context.Background(), kindMovie, "Oppenheimer", "en", 2023); err != nil {
		t.Fatal(err)
	}
	if r := srv.last.Load(); r.URL.Path != "/search/movie" || r.URL.Query().Get("year") != "2023" {
		t.Errorf("movie request %q", r.URL)
	}
	if _, err := c.Search(context.Background(), kindTV, "Dark", "en", 2017); err != nil {
		t.Fatal(err)
	}
	if r := srv.last.Load(); r.URL.Path != "/search/tv" || r.URL.Query().Get("first_air_date_year") != "2017" {
		t.Errorf("tv request %q", r.URL)
	}
	if _, err := c.Search(context.Background(), "person", "x", "en", 0); err == nil {
		t.Error("Search(person) = nil error")
	}
}

func TestClient_Details(t *testing.T) {
	srv := newStub(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(detailsJSON)) })
	d, err := testClient(srv.URL).Details(context.Background(), kindTV, 70523, "de-DE")
	if err != nil {
		t.Fatalf("Details: %v", err)
	}
	r := srv.last.Load()
	if r.URL.Path != "/tv/70523" || r.URL.Query().Get("append_to_response") != "watch/providers" {
		t.Errorf("request %q", r.URL)
	}
	if d.Title != "Dark" || d.Year != "2017" || len(d.Regions["DE"].Flatrate) != 2 {
		t.Errorf("details = %+v", d)
	}
}

func TestClient_Errors(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"unauthorized", http.StatusUnauthorized, `{}`, "status 401"},
		{"rate limited", http.StatusTooManyRequests, `{}`, "rate limited"},
		{"server error", http.StatusInternalServerError, `{}`, "status 500"},
		{"malformed json", http.StatusOK, `{"results":`, "decode"},
		{"oversize body", http.StatusOK, strings.Repeat(" ", maxBodyBytes+10), "exceeds"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newStub(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			})
			_, err := testClient(srv.URL).SearchMulti(context.Background(), "x", "de-DE")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tt.want)
			}
			if strings.Contains(err.Error(), testToken) {
				t.Errorf("token leaked into error %q", err)
			}
			if tt.status == http.StatusTooManyRequests && !errors.Is(err, ErrRateLimited) {
				t.Errorf("err = %v, want ErrRateLimited", err)
			}
		})
	}
}
