package mcp

import (
	"context"
	"strings"
	"testing"
)

func TestHandlePolicyDSL(t *testing.T) {
	t.Parallel()
	srv := newTestServer()
	ctx := context.Background()

	res, err := srv.handlePolicyDSL(ctx, map[string]any{
		"files":   []string{".github/workflows/deploy.yml", "internal/api/auth.go"},
		"imports": []string{"unsafe"},
		"diff":    "+import \"unsafe\"\n+os.Exit(1)\n",
	})
	if err != nil {
		t.Fatalf("handlePolicyDSL failed: %v", err)
	}

	if !strings.Contains(res, "BLOCKED") {
		t.Errorf("expected BLOCKED verdict in report, got: %s", res)
	}
	if !strings.Contains(res, "protect-workflows") {
		t.Errorf("expected protect-workflows violation, got: %s", res)
	}
}
