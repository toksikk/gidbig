package tmdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const (
	// defaultBaseURL is the TMDB v3 API root.
	defaultBaseURL = "https://api.themoviedb.org/3"
	requestTimeout = 10 * time.Second
	// maxBodyBytes caps one response body; a details response with every
	// region's providers stays far below it.
	maxBodyBytes = 1 << 20
)

// Media kinds accepted by the TMDB API and carried in select-menu values.
const (
	kindMovie = "movie"
	kindTV    = "tv"
)

// ErrRateLimited reports a TMDB 429 response.
var ErrRateLimited = errors.New("TMDB rate limited, try again shortly")

// Client is a minimal TMDB v3 client authenticated with the API Read Access
// Token as a Bearer header. The token is never put into a URL or an error.
type Client struct {
	base  string
	token string
	http  *http.Client
}

// NewClient returns a client for the public TMDB API.
func NewClient(token string) *Client {
	return &Client{base: defaultBaseURL, token: token, http: &http.Client{Timeout: requestTimeout}}
}

// Match is one movie or series search hit.
type Match struct {
	Kind  string
	ID    int
	Title string
	Year  string
}

// Provider is one watch-provider entry.
type Provider struct {
	ID       int    `json:"provider_id"`
	Name     string `json:"provider_name"`
	Priority int    `json:"display_priority"`
}

// RegionProviders is the watch-provider block for one country.
type RegionProviders struct {
	Link     string     `json:"link"`
	Flatrate []Provider `json:"flatrate"`
	Free     []Provider `json:"free"`
	Ads      []Provider `json:"ads"`
	Rent     []Provider `json:"rent"`
	Buy      []Provider `json:"buy"`
}

// Details is a movie or series with its providers for every region.
type Details struct {
	Match
	Regions map[string]RegionProviders
}

// searchResult covers the fields of movie, tv and person results.
type searchResult struct {
	MediaType    string `json:"media_type"`
	ID           int    `json:"id"`
	Title        string `json:"title"`
	Name         string `json:"name"`
	ReleaseDate  string `json:"release_date"`
	FirstAirDate string `json:"first_air_date"`
}

func (r searchResult) toMatch(kind string) Match {
	title, date := r.Title, r.ReleaseDate
	if kind == kindTV {
		title, date = r.Name, r.FirstAirDate
	}
	return Match{Kind: kind, ID: r.ID, Title: title, Year: yearOf(date)}
}

func yearOf(date string) string {
	if len(date) >= 4 {
		return date[:4]
	}
	return ""
}

// SearchMulti searches movies and series; persons are dropped.
func (c *Client) SearchMulti(ctx context.Context, query, language string) ([]Match, error) {
	var payload struct {
		Results []searchResult `json:"results"`
	}
	q := url.Values{"query": {query}, "language": {language}, "page": {"1"}, "include_adult": {"false"}}
	if err := c.get(ctx, "/search/multi", q, &payload); err != nil {
		return nil, err
	}
	out := make([]Match, 0, len(payload.Results))
	for _, r := range payload.Results {
		if r.MediaType != kindMovie && r.MediaType != kindTV {
			continue
		}
		out = append(out, r.toMatch(r.MediaType))
	}
	return out, nil
}

// Search searches one kind, optionally filtered by year (0 means no filter).
func (c *Client) Search(ctx context.Context, kind, query, language string, year int) ([]Match, error) {
	if kind != kindMovie && kind != kindTV {
		return nil, fmt.Errorf("tmdb: unknown kind %q", kind)
	}
	var payload struct {
		Results []searchResult `json:"results"`
	}
	q := url.Values{"query": {query}, "language": {language}, "page": {"1"}, "include_adult": {"false"}}
	if year > 0 {
		if kind == kindMovie {
			q.Set("year", strconv.Itoa(year))
		} else {
			q.Set("first_air_date_year", strconv.Itoa(year))
		}
	}
	if err := c.get(ctx, "/search/"+kind, q, &payload); err != nil {
		return nil, err
	}
	out := make([]Match, 0, len(payload.Results))
	for _, r := range payload.Results {
		out = append(out, r.toMatch(kind))
	}
	return out, nil
}

// Details fetches a movie or series together with its watch providers in one
// request (append_to_response=watch/providers).
func (c *Client) Details(ctx context.Context, kind string, id int, language string) (Details, error) {
	if kind != kindMovie && kind != kindTV {
		return Details{}, fmt.Errorf("tmdb: unknown kind %q", kind)
	}
	var payload struct {
		searchResult
		WatchProviders struct {
			Results map[string]RegionProviders `json:"results"`
		} `json:"watch/providers"`
	}
	q := url.Values{"language": {language}, "append_to_response": {"watch/providers"}}
	if err := c.get(ctx, "/"+kind+"/"+strconv.Itoa(id), q, &payload); err != nil {
		return Details{}, err
	}
	m := payload.toMatch(kind)
	m.ID = id
	return Details{Match: m, Regions: payload.WatchProviders.Results}, nil
}

// get performs one authenticated GET and decodes the JSON body into out.
func (c *Client) get(ctx context.Context, path string, q url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path+"?"+q.Encode(), nil)
	if err != nil {
		return fmt.Errorf("tmdb %s: build request: %w", path, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("tmdb %s: request: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusTooManyRequests {
		return fmt.Errorf("tmdb %s: %w", path, ErrRateLimited)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("tmdb %s: status %d", path, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes+1))
	if err != nil {
		return fmt.Errorf("tmdb %s: read body: %w", path, err)
	}
	if len(body) > maxBodyBytes {
		return fmt.Errorf("tmdb %s: response exceeds %d bytes", path, maxBodyBytes)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("tmdb %s: decode: %w", path, err)
	}
	return nil
}
