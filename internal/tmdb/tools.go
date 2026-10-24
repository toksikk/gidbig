package tmdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/toksikk/gidbig/internal/tool"
)

// toolName is the LLM tool exposed by this module.
const toolName = "streaming_providers"

// streamingProvidersParameters is the JSON schema for the streaming_providers tool.
var streamingProvidersParameters = json.RawMessage(`{
  "type": "object",
  "properties": {
    "title": {"type": "string", "description": "Movie or series title to look up."},
    "type": {"type": "string", "enum": ["movie", "tv"], "description": "Restrict to movies or series. Omit when unknown."},
    "year": {"type": "integer", "description": "Release year (movie) or first air year (series) to disambiguate. Only applied when type is set."},
    "region": {"type": "string", "description": "Two-letter country code, e.g. DE. Omit to use the bot's default region."}
  },
  "required": ["title"]
}`)

type streamingArgs struct {
	Title  string `json:"title"`
	Type   string `json:"type"`
	Year   int    `json:"year"`
	Region string `json:"region"`
}

// registerTools registers the tools owned by the tmdb module.
func (m *Module) registerTools() {
	if m.tools == nil {
		return
	}
	m.tools.Register(tool.Def{
		Name:        toolName,
		Description: "Look up where a movie or TV series can be streamed (subscription, free, with ads) in a country, using TMDB/JustWatch data. Uses the top search match; the result names the match so you can say which title was assumed. Always mention the JustWatch attribution.",
		Parameters:  streamingProvidersParameters,
		Handler:     m.streamingToolHandler,
	})
}

// streamingToolHandler searches TMDB, takes the top match and returns its
// providers for the region as plain text.
func (m *Module) streamingToolHandler(ctx context.Context, _ tool.Env, raw json.RawMessage) (string, error) {
	var args streamingArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("parse arguments: %w", err)
	}
	args.Title = strings.TrimSpace(args.Title)
	if args.Title == "" {
		return "", errors.New("title is required")
	}
	if args.Type != "" && args.Type != kindMovie && args.Type != kindTV {
		return "", fmt.Errorf("type must be movie or tv, got %q", args.Type)
	}
	if args.Year < 0 || args.Year > 3000 {
		return "", fmt.Errorf("year %d is out of range", args.Year)
	}
	region, err := m.normalizeRegion(args.Region)
	if err != nil {
		return "", err
	}

	var matches []Match
	if args.Type == "" {
		matches, err = m.client.SearchMulti(ctx, args.Title, m.language())
	} else {
		matches, err = m.client.Search(ctx, args.Type, args.Title, m.language(), args.Year)
	}
	if err != nil {
		return "", fmt.Errorf("search: %w", err)
	}
	if len(matches) == 0 {
		return fmt.Sprintf("No movie or series found for %q.", args.Title), nil
	}
	d, err := m.details(ctx, matches[0].Kind, matches[0].ID)
	if err != nil {
		return "", fmt.Errorf("details: %w", err)
	}
	return formatText(d, region), nil
}
