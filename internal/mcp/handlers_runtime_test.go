package mcp

import (
	"context"
	"strings"
	"testing"
)

// TestHandleRuntimeStatusUnwired: with no runtime source configured, the
// status snapshot reports wired=false with the enable hint (the same shape
// as `kern runtime status --json`).
func TestHandleRuntimeStatusUnwired(t *testing.T) {
	t.Parallel()
	srv := newTestServer()
	ctx := context.Background()
	res, err := srv.handleRuntime(ctx, map[string]any{"action": "status"})
	if err != nil {
		t.Fatalf("handleRuntime(status) failed: %v", err)
	}
	if !strings.Contains(res, `"wired": false`) {
		t.Errorf("status snapshot missing wired:false, got: %s", res)
	}
	if !strings.Contains(res, "KERN_PROMETHEUS_URL") {
		t.Errorf("status snapshot missing the enable hint, got: %s", res)
	}
}

// TestHandleRuntimeDrift: drift always returns the report shape (matched /
// prod_only / code_only) even with no source wired.
func TestHandleRuntimeDrift(t *testing.T) {
	t.Parallel()
	srv := newTestServer()
	ctx := context.Background()
	res, err := srv.handleRuntime(ctx, map[string]any{"action": "drift"})
	if err != nil {
		t.Fatalf("handleRuntime(drift) failed: %v", err)
	}
	for _, want := range []string{`"matched"`, `"prod_only"`, `"code_only"`} {
		if !strings.Contains(res, want) {
			t.Errorf("drift snapshot missing %s, got: %s", want, res)
		}
	}
}

// TestHandleRuntimeUnknownAction: an invalid action is rejected with a
// clear error.
func TestHandleRuntimeUnknownAction(t *testing.T) {
	t.Parallel()
	srv := newTestServer()
	ctx := context.Background()
	if _, err := srv.handleRuntime(ctx, map[string]any{"action": "bogus"}); err == nil {
		t.Fatal("handleRuntime(bogus) = nil error, want unknown-action error")
	} else if !strings.Contains(err.Error(), "bogus") {
		t.Errorf("error = %q, want it to name the unknown action", err)
	}
}
