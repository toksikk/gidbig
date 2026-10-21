package openmeteo

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// fixtureForecast builds a deterministic forecast for days days starting 2026-10-22.
func fixtureForecast(days int) forecastResponse {
	var fc forecastResponse
	fc.Latitude, fc.Longitude, fc.Timezone = 51.19, 8.53, "Europe/Berlin"
	fc.Current.Temperature = 6
	fc.Current.Apparent = 3
	fc.Current.Humidity = 90
	fc.Current.WeatherCode = 61
	fc.Current.WindSpeed = 14
	fc.Current.WindDirection = 225
	fc.Current.CloudCover = 100
	start := time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC)
	for d := 0; d < days; d++ {
		date := start.AddDate(0, 0, d).Format("2006-01-02")
		fc.Daily.Time = append(fc.Daily.Time, date)
		fc.Daily.WeatherCode = append(fc.Daily.WeatherCode, 61)
		fc.Daily.TempMin = append(fc.Daily.TempMin, 3)
		fc.Daily.TempMax = append(fc.Daily.TempMax, 7)
		fc.Daily.PrecipSum = append(fc.Daily.PrecipSum, 3.3)
		fc.Daily.PrecipProbMax = append(fc.Daily.PrecipProbMax, 63)
		fc.Daily.Sunshine = append(fc.Daily.Sunshine, 11517)
		fc.Daily.WindMax = append(fc.Daily.WindMax, 18)
		fc.Daily.GustsMax = append(fc.Daily.GustsMax, 41)
		fc.Daily.WindDir = append(fc.Daily.WindDir, 225)
		fc.Daily.UVMax = append(fc.Daily.UVMax, 1)
		fc.Daily.SnowSum = append(fc.Daily.SnowSum, 0)
		fc.Daily.Sunrise = append(fc.Daily.Sunrise, date+"T07:58")
		fc.Daily.Sunset = append(fc.Daily.Sunset, date+"T18:21")
		if d == 1 {
			// Day 2: dry, partly cloudy, westerly wind.
			last := len(fc.Daily.Time) - 1
			fc.Daily.WeatherCode[last] = 2
			fc.Daily.TempMax[last], fc.Daily.TempMin[last] = 9, 2
			fc.Daily.PrecipSum[last], fc.Daily.PrecipProbMax[last] = 0, 5
			fc.Daily.Sunshine[last] = 24480
			fc.Daily.WindMax[last], fc.Daily.GustsMax[last], fc.Daily.WindDir[last] = 12, 30, 270
		}
		for h := 0; h < 24; h++ {
			fc.Hourly.Time = append(fc.Hourly.Time, fmt.Sprintf("%sT%02d:00", date, h))
			fc.Hourly.Temperature = append(fc.Hourly.Temperature, 4)
			fc.Hourly.Precipitation = append(fc.Hourly.Precipitation, 0.1)
			fc.Hourly.PrecipProb = append(fc.Hourly.PrecipProb, float64(h))
			fc.Hourly.WeatherCode = append(fc.Hourly.WeatherCode, 0)
			fc.Hourly.CloudCover = append(fc.Hourly.CloudCover, 6)
			fc.Hourly.WindSpeed = append(fc.Hourly.WindSpeed, 10)
		}
	}
	return fc
}

const geocodeFixture = `{"results":[
 {"name":"Winterberg","admin1":"Zürich","country":"Schweiz","country_code":"CH","latitude":47.45,"longitude":8.69},
 {"name":"Winterberg","admin1":"Nordrhein-Westfalen","country":"Deutschland","country_code":"DE","latitude":51.19,"longitude":8.53}
]}`

// fakeAPI serves both Open-Meteo endpoints and counts requests.
type fakeAPI struct {
	srv            *httptest.Server
	geocodeCalls   atomic.Int64
	forecastCalls  atomic.Int64
	forecastStatus int
	geocodeBody    string
	lastForecast   atomic.Value // url.Values as string
	delay          time.Duration
}

func newFakeAPI(t *testing.T) *fakeAPI {
	t.Helper()
	f := &fakeAPI{forecastStatus: http.StatusOK, geocodeBody: geocodeFixture}
	mux := http.NewServeMux()
	mux.HandleFunc("/geo", func(w http.ResponseWriter, _ *http.Request) {
		f.geocodeCalls.Add(1)
		_, _ = w.Write([]byte(f.geocodeBody))
	})
	mux.HandleFunc("/fc", func(w http.ResponseWriter, r *http.Request) {
		f.forecastCalls.Add(1)
		f.lastForecast.Store(r.URL.RawQuery)
		if f.delay > 0 {
			time.Sleep(f.delay)
		}
		if f.forecastStatus != http.StatusOK {
			w.WriteHeader(f.forecastStatus)
			return
		}
		days := 7
		_, _ = fmt.Sscanf(r.URL.Query().Get("forecast_days"), "%d", &days)
		_ = json.NewEncoder(w).Encode(fixtureForecast(days))
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeAPI) client() *client {
	return &client{httpClient: f.srv.Client(), forecastURL: f.srv.URL + "/fc", geocodeURL: f.srv.URL + "/geo"}
}

func newTestModule(t *testing.T) (*Module, *fakeAPI) {
	t.Helper()
	f := newFakeAPI(t)
	m := New()
	m.client = f.client()
	return m, f
}
