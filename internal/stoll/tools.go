package stoll

import (
	"context"
	"encoding/json"

	"github.com/toksikk/gidbig/internal/tool"
)

// stollQuoteParameters is the JSON schema for the stoll_quote tool.
var stollQuoteParameters = json.RawMessage(`{
  "type": "object",
  "properties": {}
}`)

// registerTools registers the tools owned by the stoll module.
func (m *Module) registerTools() {
	if m.tools == nil {
		return
	}
	m.tools.Register(tool.Def{
		Name:        "stoll_quote",
		Description: "Return a random quote attributed to Dr. Axel Stoll. Read-only novelty tool.",
		Parameters:  stollQuoteParameters,
		Handler:     m.quoteHandler,
	})
}

// quoteHandler returns a single generated quote. It is stateless and needs no
// caller scope.
func (m *Module) quoteHandler(_ context.Context, _ tool.Env, _ json.RawMessage) (string, error) {
	return buildQuote(), nil
}
