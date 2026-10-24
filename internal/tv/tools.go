package tv

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/toksikk/gidbig/internal/tool"
)

// toolParameters is the JSON schema for tv_program.
var toolParameters = json.RawMessage(`{
  "type": "object",
  "properties": {
    "mode": {
      "type": "string",
      "enum": ["now", "channel", "search"],
      "description": "now: running and next show (one channel or all); channel: a channel's schedule for one day; search: upcoming shows by title."
    },
    "channel": {
      "type": "string",
      "description": "German TV channel name, e.g. ARD, ZDF, RTL, ProSieben. Required for mode channel, optional for now."
    },
    "day": {
      "type": "string",
      "description": "For mode channel: heute, morgen, übermorgen, gestern or YYYY-MM-DD (default heute)."
    },
    "query": {
      "type": "string",
      "description": "For mode search: title or part of it."
    }
  },
  "required": ["mode"]
}`)

func (m *Module) registerTools() {
	if m.tools == nil {
		return
	}
	m.tools.Register(tool.Def{
		Name:        "tv_program",
		Description: "German TV program (EPG) for the main German channels: what runs now/next, a channel's schedule for a day (yesterday to +7 days), or a title search. Times are Europe/Berlin.",
		Parameters:  toolParameters,
		Handler:     m.toolHandler,
	})
}

// toolHandler returns plain text for the LLM. Problems the model can fix are
// returned as text, not errors.
func (m *Module) toolHandler(_ context.Context, _ tool.Env, args json.RawMessage) (string, error) {
	var in struct {
		Mode    string `json:"mode"`
		Channel string `json:"channel"`
		Day     string `json:"day"`
		Query   string `json:"query"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return "", fmt.Errorf("tv_program: invalid arguments: %w", err)
	}
	ix := m.index()
	if ix == nil {
		return msgNotReady, nil
	}
	now := m.now()
	channel := strings.TrimSpace(in.Channel)
	switch strings.TrimSpace(in.Mode) {
	case "now":
		chs, msg := resolveOrAll(ix, channel)
		if msg != "" {
			return msg, nil
		}
		return renderNow(ix, chs, now, len(chs) <= descResultLimit), nil
	case "channel":
		if channel == "" {
			return "mode channel needs a channel", nil
		}
		ch, ok := ix.resolveChannel(channel)
		if !ok {
			return unknownChannel(ix, channel), nil
		}
		day, ok := parseDay(in.Day, now)
		if !ok {
			return msgBadDay, nil
		}
		return renderDay(ix, ch, day, false), nil
	case "search":
		q := strings.TrimSpace(in.Query)
		if q == "" {
			return "mode search needs a query", nil
		}
		return renderSearch(ix, q, now, searchLimit, true), nil
	default:
		return "mode must be one of now, channel, search", nil
	}
}
