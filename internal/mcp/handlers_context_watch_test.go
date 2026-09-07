package mcp

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func TestHandleContextWatch(t *testing.T) {
	s := NewServer(strings.NewReader(""), io.Discard)

	// Simulated heavy context containing logs and code
	heavyLog := `2026-09-06T12:00:00Z ERROR [database] connection timed out after 30s
Traceback (most recent call last):
  File "db.py", line 45, in connect
    raise ConnectionTimeout("pool exhausted")
ConnectionTimeout: pool exhausted
`
	// Repeat to inflate size
	var sb strings.Builder
	for i := 0; i < 50; i++ {
		sb.WriteString(heavyLog)
		sb.WriteString("\n")
	}
	sampleContext := sb.String()

	// Test 1: Plain text analysis
	res, err := s.handleContextWatch(context.Background(), map[string]any{
		"text":   sampleContext,
		"budget": "5000",
	})
	if err != nil {
		t.Fatalf("handleContextWatch error: %v", err)
	}

	if !strings.Contains(res, "CONTEXT WATCH REPORT") {
		t.Errorf("missing header in response: %s", res)
	}
	if !strings.Contains(res, "Bloated Context Segments Identified") {
		t.Errorf("missing bloated segments section: %s", res)
	}
	if !strings.Contains(res, "kern_optimize_log") {
		t.Errorf("expected recommendation to use kern_optimize_log: %s", res)
	}

	// Test 2: JSON output format
	resJSON, err := s.handleContextWatch(context.Background(), map[string]any{
		"text":   sampleContext,
		"budget": "5000",
		"format": "json",
	})
	if err != nil {
		t.Fatalf("handleContextWatch JSON format error: %v", err)
	}

	var analysis ContextWatchAnalysis
	if err := json.Unmarshal([]byte(resJSON), &analysis); err != nil {
		t.Fatalf("failed to decode JSON response: %v; raw=%s", err, resJSON)
	}
	if analysis.TotalTokens <= 0 {
		t.Errorf("expected positive total tokens, got %d", analysis.TotalTokens)
	}
	if analysis.TokenBudget != 5000 {
		t.Errorf("expected budget 5000, got %d", analysis.TokenBudget)
	}
	if len(analysis.HeavySegments) == 0 {
		t.Errorf("expected identified heavy segments")
	}
}

func TestHandleContextSuggestions(t *testing.T) {
	s := NewServer(strings.NewReader(""), io.Discard)
	s.roots = []string{kernRepoRoot}
	res, err := s.handleContext(context.Background(), map[string]any{
		"root":   kernRepoRoot,
		"symbol": "handlePrompt",
	})
	if err != nil {
		t.Fatalf("handleContext error: %v", err)
	}
	if !strings.Contains(res, "Did you mean:") {
		t.Errorf("expected Did you mean suggestions in context result, got %s", res)
	}
}
