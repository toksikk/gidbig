package openholidays

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/toksikk/gidbig/internal/tool"
)

// toolDefaultDays is the tool's default window, kept short for compact answers.
const toolDefaultDays = 30

// holidaysParameters is the JSON schema for the holidays tool.
var holidaysParameters = json.RawMessage(`{
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "country": {
      "type": "string",
      "description": "Two-letter ISO country code, default DE."
    },
    "subdivision": {
      "type": "string",
      "description": "Optional subdivision code such as DE-BY (Bavaria) or DE-NW (North Rhine-Westphalia). Nationwide holidays are always included."
    },
    "type": {
      "type": "string",
      "enum": ["public", "school"],
      "description": "public (default) or school holidays."
    },
    "from": {
      "type": "string",
      "description": "Start date YYYY-MM-DD, default today (Europe/Berlin)."
    },
    "to": {
      "type": "string",
      "description": "End date YYYY-MM-DD, default from + 30 days. Range must be under 3 years."
    }
  }
}`)

// registerTools registers the tools owned by the openholidays module.
func (m *Module) registerTools() {
	if m.tools == nil {
		return
	}
	m.tools.Register(tool.Def{
		Name:        "holidays",
		Description: "Public or school holidays for a country and optional subdivision in a date range, from the OpenHolidays API (36 mostly European countries). Use it to answer whether a date is a holiday somewhere.",
		Parameters:  holidaysParameters,
		Handler:     m.holidaysToolHandler,
	})
}

// holidaysToolHandler returns plain-text holidays for the LLM. Invalid
// arguments and API failures are returned as errors.
func (m *Module) holidaysToolHandler(ctx context.Context, _ tool.Env, args json.RawMessage) (string, error) {
	var in struct {
		Country     string `json:"country"`
		Subdivision string `json:"subdivision"`
		Type        string `json:"type"`
		From        string `json:"from"`
		To          string `json:"to"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &in); err != nil {
			return "", fmt.Errorf("holidays: invalid arguments: %w", err)
		}
	}
	q, err := m.parseQuery(rawQuery{country: in.Country, region: in.Subdivision, kind: in.Type, from: in.From, to: in.To}, spanDaysFrom(toolDefaultDays))
	if err != nil {
		return "", fmt.Errorf("holidays: %w", err)
	}
	hs, err := m.lookup(ctx, q)
	if err != nil {
		return "", fmt.Errorf("holidays: %w", err)
	}
	return buildToolText(q, hs), nil
}
