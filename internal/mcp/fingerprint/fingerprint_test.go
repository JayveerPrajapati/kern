package fingerprint

import (
	"context"
	"strings"
	"testing"
)

func TestFingerprintEmptyAudit(t *testing.T) {
	ctx := context.Background()
	res, err := Analyze(ctx, Hooks{}, map[string]any{})
	if err != nil {
		t.Fatalf("Analyze failed: %v", err)
	}
	if !strings.Contains(res, "AGENT BEHAVIOR FINGERPRINT") {
		t.Errorf("expected report header, got: %s", res)
	}
}
