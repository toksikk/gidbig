package openholidays

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	defaultBaseURL = "https://openholidaysapi.org"
	maxBodyBytes   = 1 << 20
	httpTimeout    = 15 * time.Second
)

// errBodyTooLarge is returned when a response exceeds maxBodyBytes.
var errBodyTooLarge = errors.New("response body too large")

// localizedText is one translation of a name.
type localizedText struct {
	Language string `json:"language"`
	Text     string `json:"text"`
}

// subdivisionRef is the subdivision reference embedded in a holiday.
type subdivisionRef struct {
	Code      string `json:"code"`
	ShortName string `json:"shortName"`
}

// holiday is one public or school holiday.
type holiday struct {
	ID           string           `json:"id"`
	StartDate    string           `json:"startDate"`
	EndDate      string           `json:"endDate"`
	Type         string           `json:"type"`
	Name         []localizedText  `json:"name"`
	Nationwide   bool             `json:"nationwide"`
	Subdivisions []subdivisionRef `json:"subdivisions"`
}

// country is one entry of /Countries.
type country struct {
	IsoCode string          `json:"isoCode"`
	Name    []localizedText `json:"name"`
}

// subdivision is one entry of /Subdivisions; children are nested.
type subdivision struct {
	Code      string          `json:"code"`
	ShortName string          `json:"shortName"`
	Name      []localizedText `json:"name"`
	Children  []subdivision   `json:"children"`
}

// problemDetails is the RFC 7807 error body returned by the API.
type problemDetails struct {
	Title  string `json:"title"`
	Detail string `json:"detail"`
}

// client talks to the OpenHolidays API.
type client struct {
	httpClient *http.Client
	baseURL    string
}

func newClient() *client {
	return &client{httpClient: &http.Client{Timeout: httpTimeout}, baseURL: defaultBaseURL}
}

func (c *client) getJSON(ctx context.Context, path string, q url.Values, out any) error {
	rawURL := c.baseURL + path
	if len(q) > 0 {
		rawURL += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return fmt.Errorf("openholidays: %s: build request: %w", path, err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("openholidays: %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes+1))
	if err != nil {
		return fmt.Errorf("openholidays: %s: read body: %w", path, err)
	}
	if resp.StatusCode != http.StatusOK {
		var pd problemDetails
		_ = json.Unmarshal(body, &pd)
		msg := strings.TrimSpace(strings.Join(nonEmpty(pd.Title, pd.Detail), ": "))
		if msg != "" {
			return fmt.Errorf("openholidays: %s: status %s: %s", path, resp.Status, msg)
		}
		return fmt.Errorf("openholidays: %s: status %s", path, resp.Status)
	}
	if len(body) > maxBodyBytes {
		return fmt.Errorf("openholidays: %s: %w", path, errBodyTooLarge)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("openholidays: %s: decode: %w", path, err)
	}
	return nil
}

func nonEmpty(s ...string) []string {
	var out []string
	for _, v := range s {
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

// holidays fetches public or school holidays for country (and optional
// subdivision) between from and to, inclusive, as YYYY-MM-DD.
func (c *client) holidays(ctx context.Context, kind, countryCode, subdivisionCode, from, to string) ([]holiday, error) {
	path := "/PublicHolidays"
	if kind == typeSchool {
		path = "/SchoolHolidays"
	}
	q := url.Values{}
	q.Set("countryIsoCode", countryCode)
	q.Set("validFrom", from)
	q.Set("validTo", to)
	if subdivisionCode != "" {
		q.Set("subdivisionCode", subdivisionCode)
	}
	var out []holiday
	if err := c.getJSON(ctx, path, q, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// countries lists the supported countries.
func (c *client) countries(ctx context.Context) ([]country, error) {
	var out []country
	if err := c.getJSON(ctx, "/Countries", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// subdivisions lists the country's subdivisions, flattened.
func (c *client) subdivisions(ctx context.Context, countryCode string) ([]subdivision, error) {
	q := url.Values{}
	q.Set("countryIsoCode", countryCode)
	var tree []subdivision
	if err := c.getJSON(ctx, "/Subdivisions", q, &tree); err != nil {
		return nil, err
	}
	var out []subdivision
	var walk func([]subdivision)
	walk = func(list []subdivision) {
		for _, s := range list {
			children := s.Children
			s.Children = nil
			out = append(out, s)
			walk(children)
		}
	}
	walk(tree)
	return out, nil
}
