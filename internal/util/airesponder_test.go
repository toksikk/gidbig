package util

import (
	"context"
	"errors"
	"testing"
)

func TestAIResponder_Generate_AIPath(t *testing.T) {
	r := &AIResponder{
		SystemPromptTemplate: "Match these examples: {{examples}}",
		ExamplePool:          []string{"a", "b", "c"},
		ExampleCount:         2,
		Fallback:             func() string { return "fallback" },
		GenerateFn: func(_ context.Context, _, _ string) (string, error) {
			return "AI result", nil
		},
	}
	got := r.Generate(context.Background())
	if got != "AI result" {
		t.Fatalf("expected 'AI result', got %q", got)
	}
}

func TestAIResponder_Generate_FallbackOnError(t *testing.T) {
	r := &AIResponder{
		SystemPromptTemplate: "{{examples}}",
		ExamplePool:          []string{"a"},
		ExampleCount:         1,
		Fallback:             func() string { return "fallback" },
		GenerateFn: func(_ context.Context, _, _ string) (string, error) {
			return "", errors.New("simulated failure")
		},
	}
	got := r.Generate(context.Background())
	if got != "fallback" {
		t.Fatalf("expected 'fallback', got %q", got)
	}
}

func TestAIResponder_Generate_FallbackOnEmptyResponse(t *testing.T) {
	r := &AIResponder{
		SystemPromptTemplate: "{{examples}}",
		ExamplePool:          []string{"a"},
		ExampleCount:         1,
		Fallback:             func() string { return "fallback" },
		GenerateFn: func(_ context.Context, _, _ string) (string, error) {
			return "   ", nil
		},
	}
	got := r.Generate(context.Background())
	if got != "fallback" {
		t.Fatalf("expected 'fallback' on whitespace-only response, got %q", got)
	}
}

func TestAIResponder_Generate_FallbackWhenGenerateFnNil(t *testing.T) {
	r := &AIResponder{
		Fallback: func() string { return "nil-fn fallback" },
	}
	got := r.Generate(context.Background())
	if got != "nil-fn fallback" {
		t.Fatalf("expected 'nil-fn fallback', got %q", got)
	}
}

func TestAIResponder_Generate_InjectsExamplesIntoPrompt(t *testing.T) {
	var capturedSys string
	r := &AIResponder{
		SystemPromptTemplate: "Examples: {{examples}}",
		ExamplePool:          []string{"foo"},
		ExampleCount:         1,
		Fallback:             func() string { return "x" },
		GenerateFn: func(_ context.Context, sys, _ string) (string, error) {
			capturedSys = sys
			return "ok", nil
		},
	}
	r.Generate(context.Background())
	if capturedSys != "Examples: foo" {
		t.Fatalf("unexpected system prompt: %q", capturedSys)
	}
}

func TestAIResponder_Generate_TrimsResponse(t *testing.T) {
	r := &AIResponder{
		SystemPromptTemplate: "{{examples}}",
		ExamplePool:          []string{"a"},
		ExampleCount:         1,
		Fallback:             func() string { return "x" },
		GenerateFn: func(_ context.Context, _, _ string) (string, error) {
			return "  trimmed  ", nil
		},
	}
	got := r.Generate(context.Background())
	if got != "trimmed" {
		t.Fatalf("expected trimmed output, got %q", got)
	}
}

func TestPickExamples_EmptyPool(t *testing.T) {
	got := pickExamples(nil, 3)
	if len(got) != 0 {
		t.Fatalf("expected empty result for nil pool, got %v", got)
	}
}

func TestPickExamples_NLargerThanPool(t *testing.T) {
	pool := []string{"a", "b"}
	got := pickExamples(pool, 10)
	if len(got) != len(pool) {
		t.Fatalf("expected %d items, got %d", len(pool), len(got))
	}
}

func TestPickExamples_ExactCount(t *testing.T) {
	pool := []string{"a", "b", "c", "d", "e"}
	got := pickExamples(pool, 3)
	if len(got) != 3 {
		t.Fatalf("expected 3 items, got %d", len(got))
	}
	seen := make(map[string]bool)
	for _, v := range got {
		if seen[v] {
			t.Fatalf("duplicate entry in pickExamples result: %q", v)
		}
		seen[v] = true
	}
}
