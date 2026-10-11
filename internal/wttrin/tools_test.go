package wttrin

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/toksikk/gidbig/internal/tool"
)

const weatherFixture = `{
  "current_condition": [{
    "temp_C": "15",
    "FeelsLikeC": "14",
    "Humidity": "60",
    "windspeedKmph": "10",
    "winddirDegree": "180",
    "weatherCode": "113",
    "weatherDesc": [{"value": "Sunny"}]
  }],
  "nearest_area": [{
    "areaName": [{"value": "Berlin"}],
    "country": [{"value": "Germany"}],
    "region": [{"value": ""}],
    "latitude": "52.5",
    "longitude": "13.4"
  }],
  "weather": [{
    "date": "2026-10-11",
    "maxtempC": "18",
    "mintempC": "9",
    "totalSnow_cm": "0",
    "hourly": [{
      "windspeedKmph": "10",
      "winddirDegree": "180",
      "precipMM": "0",
      "chanceoffog": "0",
      "chanceoffrost": "0",
      "chanceofhightemp": "0",
      "chanceofrain": "0",
      "chanceofsnow": "0",
      "chanceofthunder": "0",
      "chanceofwindy": "0"
    }]
  }]
}`

func weatherToolModule(t *testing.T) *Module {
	t.Helper()
	var resp wttrinResponse
	if err := json.Unmarshal([]byte(weatherFixture), &resp); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	m := newTestModule()
	m.getWeatherFn = func(string) (wttrinResponse, error) { return resp, nil }
	m.tools = tool.NewRegistry()
	m.registerTools()
	return m
}

func TestRegisterToolsRegistersWeather(t *testing.T) {
	m := weatherToolModule(t)

	if _, ok := m.tools.Get("weather"); !ok {
		t.Fatal("weather was not registered")
	}
}

func TestWeatherToolHandlerCurrent(t *testing.T) {
	m := weatherToolModule(t)

	def, _ := m.tools.Get("weather")
	out, err := def.Handler(context.Background(), tool.Env{}, json.RawMessage(`{"location":"Berlin"}`))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	for _, want := range []string{"Berlin", "15°C"} {
		if !strings.Contains(out, want) {
			t.Errorf("weather output missing %q: %s", want, out)
		}
	}
}

func TestWeatherToolHandlerForecast(t *testing.T) {
	m := weatherToolModule(t)

	def, _ := m.tools.Get("weather")
	out, err := def.Handler(context.Background(), tool.Env{}, json.RawMessage(`{"location":"Berlin","forecast":true}`))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if !strings.Contains(out, "18°C / 9°C") {
		t.Errorf("forecast output = %q", out)
	}
}

func TestWeatherToolHandlerEmptyLocation(t *testing.T) {
	m := weatherToolModule(t)

	def, _ := m.tools.Get("weather")
	out, err := def.Handler(context.Background(), tool.Env{}, json.RawMessage(`{"location":"  "}`))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if !strings.Contains(out, "no location given") {
		t.Errorf("empty location output = %q", out)
	}
}

func TestRegisterToolsNilRegistryIsSafe(t *testing.T) {
	m := New()
	m.registerTools()
}
