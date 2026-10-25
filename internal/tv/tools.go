package tv

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/toksikk/gidbig/internal/tool"
)

// toolParameters is the JSON schema for tv_program.
var toolParameters = json.RawMessage(`{
  "type": "object",
  "properties": {
    "mode": {
      "type": "string",
      "enum": ["now", "channel", "search", "listings"],
      "description": "now: running and next show (one channel or all); channel: a channel's schedule for one day; search: upcoming shows by title only; listings: upcoming shows filtered by genre category and/or start-time slot over several days and channels, paged."
    },
    "channel": {
      "type": "string",
      "description": "TV channel name, e.g. ARD, ZDF, RTL, ProSieben. Required for mode channel, optional for now."
    },
    "day": {
      "type": "string",
      "description": "For mode channel: heute, morgen, übermorgen, gestern or YYYY-MM-DD (default heute)."
    },
    "query": {
      "type": "string",
      "description": "For mode search: title or part of it."
    },
    "category": {
      "type": "string",
      "description": "For mode listings: genre from the feed (e.g. Krimi, Drama, Komödie), matched as a case-insensitive substring of any of a show's genres."
    },
    "from": {
      "type": "string",
      "description": "For mode listings: earliest start time HH:MM (inclusive). Default 00:00."
    },
    "to": {
      "type": "string",
      "description": "For mode listings: latest start time HH:MM (exclusive). Default 24:00. If to <= from the slot wraps past midnight."
    },
    "channels": {
      "type": "array",
      "items": {"type": "string"},
      "description": "For mode listings: channel names; omit or [\"all\"] for every channel."
    },
    "days": {
      "type": "integer",
      "description": "For mode listings: number of calendar days starting today, 1 to 7 (default 7)."
    },
    "offset": {
      "type": "integer",
      "description": "For mode listings: index of the first result to show, from the previous page's footer (default 0)."
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
		Description: "TV program (EPG) for the configured channels (by default the main German channels): what runs now/next, a channel's schedule for a day (mode channel: yesterday to +7 days), a title-only search, or listings filtered by genre and start-time slot across channels and days, paged with offset (search and listings: as far ahead as the index reaches, about 7 to 9 days). Genres come from the feed; it has no film or format field, so ask for a genre like Krimi or Drama instead of \"Spielfilm\". Times are in the configured time zone (default Europe/Berlin).",
		Parameters:  toolParameters,
		Handler:     m.toolHandler,
	})
}

// toolHandler returns plain text for the LLM. Problems the model can fix are
// returned as text, not errors.
func (m *Module) toolHandler(_ context.Context, _ tool.Env, args json.RawMessage) (string, error) {
	var in struct {
		Mode     string   `json:"mode"`
		Channel  string   `json:"channel"`
		Day      string   `json:"day"`
		Query    string   `json:"query"`
		Category string   `json:"category"`
		From     string   `json:"from"`
		To       string   `json:"to"`
		Channels []string `json:"channels"`
		Days     *int     `json:"days"`
		Offset   int      `json:"offset"`
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
		day, ok := parseDay(in.Day, now, ix.loc)
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
	case "listings":
		f, msg := listingsFilter(ix, in.Category, in.From, in.To, in.Channels, in.Days)
		if msg != "" {
			return msg, nil
		}
		if in.Offset < 0 {
			return "offset must not be negative", nil
		}
		return renderListings(ix, f, strings.TrimSpace(in.Category), now, in.Offset), nil
	default:
		return "mode must be one of now, channel, search, listings", nil
	}
}

// listingsFilter validates the listings arguments. A non-empty message is a
// hint for the model instead of a filter.
func listingsFilter(ix *index, category, from, to string, channels []string, days *int) (listingFilter, string) {
	f := listingFilter{category: foldText(category), slotFrom: 0, slotTo: 24 * 60, days: maxDayOffset}
	from, to = strings.TrimSpace(from), strings.TrimSpace(to)
	if f.category == "" && from == "" && to == "" {
		return f, "mode listings needs a category or a from/to time slot"
	}
	var ok bool
	if from != "" {
		if f.slotFrom, ok = parseClock(from); !ok {
			return f, "from must be HH:MM"
		}
	}
	if to != "" {
		if f.slotTo, ok = parseClock(to); !ok {
			return f, "to must be HH:MM"
		}
	}
	if days != nil {
		if *days < 1 || *days > maxDayOffset {
			return f, fmt.Sprintf("days must be 1 to %d", maxDayOffset)
		}
		f.days = *days
	}
	all := len(channels) == 0
	for _, c := range channels {
		if strings.EqualFold(strings.TrimSpace(c), "all") {
			all = true
		}
	}
	if all {
		f.chs, _ = resolveOrAll(ix, "")
		return f, ""
	}
	for _, c := range channels {
		ch, ok := ix.resolveChannel(strings.TrimSpace(c))
		if !ok {
			return f, unknownChannel(ix, c)
		}
		if !slices.Contains(f.chs, ch) {
			f.chs = append(f.chs, ch)
		}
	}
	return f, ""
}

// parseClock parses HH:MM (00:00 to 24:00) into minutes after midnight.
func parseClock(s string) (int, bool) {
	if s == "24:00" {
		return 24 * 60, true
	}
	t, err := time.Parse("15:04", s)
	if err != nil {
		return 0, false
	}
	return t.Hour()*60 + t.Minute(), true
}
