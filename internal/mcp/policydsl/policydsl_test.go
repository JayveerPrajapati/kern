package policydsl

import (
	"context"
	"strings"
	"testing"
)

func TestEvaluateDefaultAllowed(t *testing.T) {
	ctx := context.Background()
	res, err := Evaluate(ctx, map[string]any{
		"files": []string{"pkg/math.go"},
	})
	if err != nil {
		t.Fatalf("Evaluate failed: %v", err)
	}
	if !strings.Contains(res, "ALLOWED") {
		t.Errorf("expected ALLOWED in report, got: %s", res)
	}
}

func TestEvaluateBlockedImport(t *testing.T) {
	ctx := context.Background()
	res, err := Evaluate(ctx, map[string]any{
		"files":   []string{"pkg/math.go"},
		"imports": []string{"unsafe"},
	})
	if err != nil {
		t.Fatalf("Evaluate failed: %v", err)
	}
	if !strings.Contains(res, "BLOCKED") {
		t.Errorf("expected BLOCKED in report for unsafe import, got: %s", res)
	}
}
