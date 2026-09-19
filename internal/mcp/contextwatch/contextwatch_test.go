package contextwatch

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAnalyze(t *testing.T) {
	heavyLog := `2026-09-06T12:00:00Z ERROR [database] connection timed out after 30s
Traceback (most recent call last):
  File "db.py", line 45, in connect
    raise ConnectionTimeout("pool exhausted")
ConnectionTimeout: pool exhausted
`
	var sb strings.Builder
	for i := 0; i < 50; i++ {
		sb.WriteString(heavyLog)
		sb.WriteString("\n")
	}
	sampleContext := sb.String()

	// Plain text
	res, err := Analyze(sampleContext, 5000, "")
	if err != nil {
		t.Fatalf("Analyze error: %v", err)
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

	// JSON
	resJSON, err := Analyze(sampleContext, 5000, "json")
	if err != nil {
		t.Fatalf("Analyze JSON error: %v", err)
	}

	var analysis Analysis
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

	// Empty error
	if _, err := Analyze("", 1000, ""); err == nil {
		t.Error("expected error on empty text")
	}
}
