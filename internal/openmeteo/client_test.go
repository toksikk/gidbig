package openmeteo

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestGeocodePrefersGerman(t *testing.T) {
	f := newFakeAPI(t)
	p, ok, err := f.client().geocode(context.Background(), "Winterberg")
	if err != nil || !ok {
		t.Fatalf("geocode: ok=%v err=%v", ok, err)
	}
	if p.CountryCode != "DE" || p.Admin1 != "Nordrhein-Westfalen" {
		t.Errorf("got %+v, want NRW", p)
	}
}

func TestGeocodeFallsBackToFirst(t *testing.T) {
	f := newFakeAPI(t)
	f.geocodeBody = `{"results":[{"name":"Paris","country_code":"FR"},{"name":"Paris","country_code":"US"}]}`
	p, ok, err := f.client().geocode(context.Background(), "Paris")
	if err != nil || !ok || p.CountryCode != "FR" {
		t.Fatalf("got %+v ok=%v err=%v", p, ok, err)
	}
}

func TestGeocodeNoResults(t *testing.T) {
	f := newFakeAPI(t)
	f.geocodeBody = `{}`
	_, ok, err := f.client().geocode(context.Background(), "Nowhere")
	if err != nil || ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
}

func TestGetJSONNonOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()
	c := &client{httpClient: srv.Client(), geocodeURL: srv.URL}
	_, _, err := c.geocode(context.Background(), "x")
	if err == nil || !strings.Contains(err.Error(), "502") {
		t.Fatalf("err = %v", err)
	}
}

func TestGetJSONOversizedBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat(" ", maxBodyBytes+10)))
	}))
	defer srv.Close()
	c := &client{httpClient: srv.Client(), geocodeURL: srv.URL}
	_, _, err := c.geocode(context.Background(), "x")
	if !errors.Is(err, errBodyTooLarge) {
		t.Fatalf("err = %v", err)
	}
}

func TestForecastQuery(t *testing.T) {
	f := newFakeAPI(t)
	fc, err := f.client().forecast(context.Background(), 51.19, 8.53, 10)
	if err != nil {
		t.Fatalf("forecast: %v", err)
	}
	if len(fc.Daily.Time) != 10 || len(fc.Hourly.Time) != 240 {
		t.Errorf("decoded %d days %d hours", len(fc.Daily.Time), len(fc.Hourly.Time))
	}
	q, _ := url.ParseQuery(f.lastForecast.Load().(string))
	for k, want := range map[string]string{
		"daily": dailyVars, "hourly": hourlyVars, "current": currentVars,
		"forecast_days": "10", "timezone": "auto", "latitude": "51.1900", "longitude": "8.5300",
	} {
		if got := q.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
}
