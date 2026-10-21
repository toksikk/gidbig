package openmeteo

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/toksikk/gidbig/internal/tool"
)

func callTool(t *testing.T, m *Module, args string) (string, error) {
	t.Helper()
	return m.weatherToolHandler(context.Background(), tool.Env{}, json.RawMessage(args))
}

func TestRegisterToolsRegistersWeather(t *testing.T) {
	m, _ := newTestModule(t)
	m.tools = tool.NewRegistry()
	m.registerTools()
	if _, ok := m.tools.Get("weather"); !ok {
		t.Fatal("weather not registered")
	}
}

func TestRegisterToolsNilRegistryIsSafe(t *testing.T) {
	New().registerTools()
}

func TestToolCoordsSkipGeocode(t *testing.T) {
	m, f := newTestModule(t)
	out, err := callTool(t, m, `{"location":"Sauerland","latitude":51.2,"longitude":8.4,"forecast":true}`)
	if err != nil {
		t.Fatal(err)
	}
	if f.geocodeCalls.Load() != 0 {
		t.Error("geocode called despite coordinates")
	}
	if !strings.HasPrefix(out, "Location: Sauerland (51.20,8.40") || strings.Count(out, "sun 3.2h") != 7 {
		t.Errorf("output:\n%s", out)
	}
}

func TestToolGeocodes(t *testing.T) {
	m, f := newTestModule(t)
	out, err := callTool(t, m, `{"location":"Winterberg"}`)
	if err != nil {
		t.Fatal(err)
	}
	if f.geocodeCalls.Load() != 1 || !strings.Contains(out, "Nordrhein-Westfalen") {
		t.Errorf("calls=%d output:\n%s", f.geocodeCalls.Load(), out)
	}
}

func TestToolNotFound(t *testing.T) {
	m, f := newTestModule(t)
	f.geocodeBody = `{}`
	out, err := callTool(t, m, `{"location":"Nowhere"}`)
	if err != nil || out != `no location found for "Nowhere"` {
		t.Errorf("out=%q err=%v", out, err)
	}
}

func TestToolInputErrors(t *testing.T) {
	m, f := newTestModule(t)
	for args, want := range map[string]string{
		`{"location":"  "}`:                               "no location given",
		`{"location":"x","latitude":51}`:                  "latitude and longitude must be given together",
		`{"location":"x","latitude":91,"longitude":8}`:    "latitude must be within",
		`{"location":"x","latitude":50,"longitude":-181}`: "latitude must be within",
	} {
		out, err := callTool(t, m, args)
		if err != nil || !strings.HasPrefix(out, want) {
			t.Errorf("%s: out=%q err=%v", args, out, err)
		}
	}
	if f.forecastCalls.Load() != 0 {
		t.Error("forecast fetched for invalid input")
	}
}

func TestToolLocationTooLong(t *testing.T) {
	m, f := newTestModule(t)
	args, _ := json.Marshal(map[string]any{"location": strings.Repeat("ä", maxLocationLen+1)})
	if _, err := callTool(t, m, string(args)); err == nil {
		t.Fatal("expected error")
	}
	args, _ = json.Marshal(map[string]any{"location": strings.Repeat("ä", maxLocationLen), "latitude": 1, "longitude": 1})
	if _, err := callTool(t, m, string(args)); err != nil {
		t.Fatalf("at limit: %v", err)
	}
	if f.geocodeCalls.Load() != 0 {
		t.Error("geocode called")
	}
}

func TestToolDaysClamp(t *testing.T) {
	for days, want := range map[int]int{0: 1, -5: 1, 99: 16, 10: 10} {
		m, _ := newTestModule(t)
		args, _ := json.Marshal(map[string]any{"location": "x", "latitude": 1, "longitude": 1, "forecast": true, "days": days})
		out, err := callTool(t, m, string(args))
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Count(out, ", sun "); got != want {
			t.Errorf("days=%d: got %d day lines, want %d", days, got, want)
		}
	}
}

func TestToolUpstreamErrorNoFallback(t *testing.T) {
	m, f := newTestModule(t)
	f.forecastStatus = http.StatusInternalServerError
	out, err := callTool(t, m, `{"location":"x","latitude":1,"longitude":1}`)
	if err == nil || out != "" {
		t.Fatalf("out=%q err=%v", out, err)
	}
}
