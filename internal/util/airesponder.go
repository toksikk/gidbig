package util

import (
	"context"
	"errors"
	"math/rand/v2"
	"strings"
)

var errEmptyAIResponse = errors.New("AI returned an empty response")

// AIResponder generates text via an LLM with few-shot examples and falls back to a
// deterministic function when the AI call fails or returns an empty result.
type AIResponder struct {
	SystemPromptTemplate string
	ExamplePool          []string
	ExampleCount         int
	Fallback             func() string
	GenerateFn           func(ctx context.Context, system, user string) (string, error)
	OnFallback           func(error)
}

// Generate selects ExampleCount entries from ExamplePool, injects them into
// SystemPromptTemplate replacing "{{examples}}", calls GenerateFn, and returns the
// trimmed response. Falls back to Fallback() on any error or empty result.
func (r *AIResponder) Generate(ctx context.Context) string {
	return r.GenerateWithPrompt(ctx, "Generate a response.")
}

// GenerateWithPrompt is like Generate but lets the caller supply the user-turn message,
// enabling topic-scoped or otherwise customised requests.
func (r *AIResponder) GenerateWithPrompt(ctx context.Context, userPrompt string) string {
	if r.GenerateFn == nil {
		if r.OnFallback != nil {
			r.OnFallback(errors.New("AI generate function is not configured"))
		}
		return r.Fallback()
	}
	examples := pickExamples(r.ExamplePool, r.ExampleCount)
	sys := strings.ReplaceAll(r.SystemPromptTemplate, "{{examples}}", strings.Join(examples, "\n\n"))
	text, err := r.GenerateFn(ctx, sys, userPrompt)
	if err != nil {
		if r.OnFallback != nil {
			r.OnFallback(err)
		}
		return r.Fallback()
	}
	if strings.TrimSpace(text) == "" {
		if r.OnFallback != nil {
			r.OnFallback(errEmptyAIResponse)
		}
		return r.Fallback()
	}
	return strings.TrimSpace(text)
}

func pickExamples(pool []string, n int) []string {
	if len(pool) == 0 || n <= 0 {
		return nil
	}
	if n >= len(pool) {
		cp := make([]string, len(pool))
		copy(cp, pool)
		return cp
	}
	perm := rand.Perm(len(pool))
	result := make([]string, n)
	for i := range n {
		result[i] = pool[perm[i]]
	}
	return result
}
