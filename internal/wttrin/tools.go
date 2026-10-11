package wttrin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/toksikk/gidbig/internal/tool"
)

// weatherParameters is the JSON schema for the weather tool.
var weatherParameters = json.RawMessage(`{
  "type": "object",
  "properties": {
    "location": {
      "type": "string",
      "description": "City or place name."
    },
    "forecast": {
      "type": "boolean",
      "description": "When true, return the multi-day forecast instead of current conditions."
    }
  },
  "required": ["location"]
}`)

// registerTools registers the tools owned by the wttrin module.
func (m *Module) registerTools() {
	if m.tools == nil {
		return
	}
	m.tools.Register(tool.Def{
		Name:        "weather",
		Description: "Report current weather or a multi-day forecast for a location via wttr.in. Read-only, served from the module's cached lookup. The location is supplied explicitly by the caller.",
		Parameters:  weatherParameters,
		Handler:     m.weatherToolHandler,
	})
}

// weatherToolHandler renders weather by reusing the module's cache and
// formatters. It skips the LLM outro that /wttr adds, so the tool stays a
// single deterministic lookup.
func (m *Module) weatherToolHandler(_ context.Context, _ tool.Env, args json.RawMessage) (string, error) {
	var in struct {
		Location string `json:"location"`
		Forecast bool   `json:"forecast"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return "", fmt.Errorf("weather: invalid arguments: %w", err)
	}
	location := strings.TrimSpace(in.Location)
	if location == "" {
		return "no location given", nil
	}

	res, err := m.getWeatherCached(location)
	if err != nil {
		return "", fmt.Errorf("weather: %w", err)
	}
	if len(res.CurrentCondition) == 0 || len(res.NearestArea) == 0 || len(res.Weather) == 0 {
		return fmt.Sprintf("no weather data for %q", location), nil
	}

	if in.Forecast {
		for _, day := range res.Weather {
			if len(day.Hourly) == 0 {
				return fmt.Sprintf("no forecast data for %q", location), nil
			}
		}
		return buildForecastString(res), nil
	}

	out := buildWeatherString(res)
	if out == "" {
		return fmt.Sprintf("no weather data for %q", location), nil
	}
	return out, nil
}
