package openholidays

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

const publicFixture = `[
 {"id":"1","startDate":"2026-12-25","endDate":"2026-12-25","type":"Public","name":[{"language":"EN","text":"Christmas Day"},{"language":"DE","text":"1. Weihnachtstag"}],"nationwide":true},
 {"id":"2","startDate":"2026-11-01","endDate":"2026-11-01","type":"Public","name":[{"language":"DE","text":"Allerheiligen"}],"nationwide":false,"subdivisions":[{"code":"DE-BY","shortName":"BY"},{"code":"DE-NW","shortName":"NW"}]},
 {"id":"3","startDate":"2026-11-18","endDate":"2026-11-18","type":"Public","name":[{"language":"DE","text":"Buß- und Bettag"}],"nationwide":false,"subdivisions":[{"code":"DE-SN","shortName":"SN"}]}
]`

const schoolFixture = `[
 {"id":"s1","startDate":"2026-12-23","endDate":"2027-01-06","type":"School","name":[{"language":"DE","text":"Weihnachtsferien"}],"nationwide":false,"subdivisions":[{"code":"DE-NW","shortName":"NW"}]}
]`

const countriesFixture = `[{"isoCode":"DE","name":[{"language":"DE","text":"Deutschland"}]},{"isoCode":"AT","name":[{"language":"DE","text":"Österreich"}]}]`

const subdivisionsFixture = `[
 {"code":"DE-BY","shortName":"BY","name":[{"language":"DE","text":"Bayern"}],"children":[{"code":"DE-BY-AU","shortName":"AU","name":[{"language":"DE","text":"Augsburg"}]}]},
 {"code":"DE-NW","shortName":"NW","name":[{"language":"DE","text":"Nordrhein-Westfalen"}]}
]`

// fakeAPI serves the OpenHolidays endpoints and counts requests.
type fakeAPI struct {
	srv          *httptest.Server
	holidayCalls atomic.Int64
	metaCalls    atomic.Int64
	status       int
	lastQuery    atomic.Value
	lastPath     atomic.Value
	delay        time.Duration
}

func newFakeAPI(t *testing.T) *fakeAPI {
	t.Helper()
	f := &fakeAPI{status: http.StatusOK}
	serve := func(body string, counter *atomic.Int64) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			counter.Add(1)
			f.lastQuery.Store(r.URL.RawQuery)
			f.lastPath.Store(r.URL.Path)
			if f.delay > 0 {
				time.Sleep(f.delay)
			}
			if f.status != http.StatusOK {
				w.WriteHeader(f.status)
				_, _ = w.Write([]byte(`{"title":"Bad","detail":"range too long"}`))
				return
			}
			_, _ = w.Write([]byte(body))
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/PublicHolidays", serve(publicFixture, &f.holidayCalls))
	mux.HandleFunc("/SchoolHolidays", serve(schoolFixture, &f.holidayCalls))
	mux.HandleFunc("/Countries", serve(countriesFixture, &f.metaCalls))
	mux.HandleFunc("/Subdivisions", serve(subdivisionsFixture, &f.metaCalls))
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

var fixedNow = time.Date(2026, 10, 25, 15, 0, 0, 0, time.UTC)

func newTestModule(t *testing.T) (*Module, *fakeAPI) {
	t.Helper()
	f := newFakeAPI(t)
	m := New()
	m.client = &client{httpClient: f.srv.Client(), baseURL: f.srv.URL}
	m.now = func() time.Time { return fixedNow }
	t.Cleanup(func() { _ = m.Shutdown() })
	return m, f
}
