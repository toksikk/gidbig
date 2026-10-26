package openholidays

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestClientHolidaysQuery(t *testing.T) {
	_, f := newTestModule(t)
	c := &client{httpClient: f.srv.Client(), baseURL: f.srv.URL}
	hs, err := c.holidays(context.Background(), typeSchool, "DE", "DE-NW", "2026-10-25", "2027-10-24")
	if err != nil {
		t.Fatal(err)
	}
	if len(hs) != 1 || hs[0].EndDate != "2027-01-06" {
		t.Fatalf("holidays = %+v", hs)
	}
	q, _ := url.ParseQuery(f.lastQuery.Load().(string))
	if f.lastPath.Load() != "/SchoolHolidays" || q.Get("countryIsoCode") != "DE" || q.Get("subdivisionCode") != "DE-NW" || q.Get("validFrom") != "2026-10-25" || q.Get("validTo") != "2027-10-24" {
		t.Fatalf("path=%v query=%v", f.lastPath.Load(), q)
	}
	if _, err := c.holidays(context.Background(), typePublic, "DE", "", "a", "b"); err != nil {
		t.Fatal(err)
	}
	q, _ = url.ParseQuery(f.lastQuery.Load().(string))
	if f.lastPath.Load() != "/PublicHolidays" || q.Has("subdivisionCode") {
		t.Fatalf("path=%v query=%v", f.lastPath.Load(), q)
	}
}

func TestClientProblemDetails(t *testing.T) {
	_, f := newTestModule(t)
	f.status = http.StatusBadRequest
	c := &client{httpClient: f.srv.Client(), baseURL: f.srv.URL}
	_, err := c.countries(context.Background())
	if err == nil || !strings.Contains(err.Error(), "range too long") || !strings.Contains(err.Error(), "400") {
		t.Fatalf("err = %v", err)
	}
}

func TestClientNon200NoBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadGateway) }))
	t.Cleanup(srv.Close)
	c := &client{httpClient: srv.Client(), baseURL: srv.URL}
	if _, err := c.countries(context.Background()); err == nil || !strings.Contains(err.Error(), "502") {
		t.Fatalf("err = %v", err)
	}
}

func TestClientBodyTooLarge(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat(" ", maxBodyBytes+1)))
	}))
	t.Cleanup(srv.Close)
	c := &client{httpClient: srv.Client(), baseURL: srv.URL}
	if _, err := c.countries(context.Background()); !errors.Is(err, errBodyTooLarge) {
		t.Fatalf("err = %v", err)
	}
}

func TestClientDecodeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{nope`)) }))
	t.Cleanup(srv.Close)
	c := &client{httpClient: srv.Client(), baseURL: srv.URL}
	if _, err := c.countries(context.Background()); err == nil || !strings.Contains(err.Error(), "decode") {
		t.Fatalf("err = %v", err)
	}
}

func TestClientSubdivisionsFlattened(t *testing.T) {
	_, f := newTestModule(t)
	c := &client{httpClient: f.srv.Client(), baseURL: f.srv.URL}
	sd, err := c.subdivisions(context.Background(), "DE")
	if err != nil {
		t.Fatal(err)
	}
	var codes []string
	for _, s := range sd {
		codes = append(codes, s.Code)
	}
	if strings.Join(codes, ",") != "DE-BY,DE-BY-AU,DE-NW" {
		t.Fatalf("codes = %v", codes)
	}
}
