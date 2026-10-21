package openmeteo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/toksikk/gidbig/internal/tool"
)

// weatherParameters is the JSON schema for the weather tool.
var weatherParameters = json.RawMessage(`{
  "type": "object",
  "properties": {
    "location": {
      "type": "string",
      "description": "City, place or region name. Used as a label when latitude/longitude are given."
    },
    "latitude": {
      "type": "number",
      "description": "Latitude in decimal degrees (-90..90). Pass together with longitude."
    },
    "longitude": {
      "type": "number",
      "description": "Longitude in decimal degrees (-180..180). Pass together with latitude."
    },
    "days": {
      "type": "integer",
      "description": "Forecast days, 1-16 (default 7). Only used when forecast is true."
    },
    "forecast": {
      "type": "boolean",
      "description": "When true, return the daily forecast instead of current conditions and today."
    }
  },
  "required": ["location"]
}`)

// registerTools registers the tools owned by the openmeteo module.
func (m *Module) registerTools() {
	if m.tools == nil {
		return
	}
	m.tools.Register(tool.Def{
		Name:        "weather",
		Description: "Weather now or daily forecast (up to 16 days) via Open-Meteo. Prefer passing latitude/longitude for regions or ambiguous names (e.g. Sauerland); location is then only a label. Forecast includes daily rain sum, rain probability, sunshine hours, and a 3-hourly breakdown for the first 3 days.",
		Parameters:  weatherParameters,
		Handler:     m.weatherToolHandler,
	})
}

// weatherToolHandler returns plain-text weather for the LLM. Input problems
// the model can fix are returned as strings; Open-Meteo failures are errors.
func (m *Module) weatherToolHandler(ctx context.Context, _ tool.Env, args json.RawMessage) (string, error) {
	var in struct {
		Location  string   `json:"location"`
		Latitude  *float64 `json:"latitude"`
		Longitude *float64 `json:"longitude"`
		Days      *int     `json:"days"`
		Forecast  bool     `json:"forecast"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return "", fmt.Errorf("weather: invalid arguments: %w", err)
	}
	location := strings.TrimSpace(in.Location)
	if location == "" {
		return "no location given", nil
	}
	if utf8.RuneCountInString(location) > maxLocationLen {
		return "", fmt.Errorf("weather: location too long (max %d characters)", maxLocationLen)
	}
	if (in.Latitude == nil) != (in.Longitude == nil) {
		return "latitude and longitude must be given together", nil
	}
	hasCoords := in.Latitude != nil
	var lat, lon float64
	if hasCoords {
		lat, lon = *in.Latitude, *in.Longitude
		if lat < -90 || lat > 90 || lon < -180 || lon > 180 {
			return "latitude must be within -90..90 and longitude within -180..180", nil
		}
	}
	days := 1
	if in.Forecast {
		days = defaultDays
		if in.Days != nil {
			days = clampDays(*in.Days)
		}
	}

	p, fc, err := m.lookup(ctx, location, hasCoords, lat, lon, days)
	if errors.Is(err, errNotFound) {
		return fmt.Sprintf("no location found for %q", location), nil
	}
	if err != nil {
		return "", fmt.Errorf("weather: %w", err)
	}
	return buildToolText(p, fc, in.Forecast), nil
}
