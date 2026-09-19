package runtime

import (
	"context"
	"strings"
	"testing"
)

func TestRuntimeStatus(t *testing.T) {
	ctx := context.Background()
	res, err := Runtime(ctx, Hooks{}, map[string]any{
		"action": "status",
	})
	if err != nil {
		t.Fatalf("Runtime status failed: %v", err)
	}
	if !strings.Contains(res, "wired") {
		t.Errorf("expected wired field in json, got: %s", res)
	}
}

func TestRuntimeActions(t *testing.T) {
	ctx := context.Background()

	// Drift
	resDrift, err := Runtime(ctx, Hooks{}, map[string]any{"action": "drift"})
	if err != nil {
		t.Fatalf("Runtime drift failed: %v", err)
	}
	if !strings.Contains(resDrift, "matched") {
		t.Errorf("expected matched in drift result, got: %s", resDrift)
	}

	// Routes
	resRoutes, err := Runtime(ctx, Hooks{}, map[string]any{"action": "routes"})
	if err != nil {
		t.Fatalf("Runtime routes failed: %v", err)
	}
	if !strings.Contains(resRoutes, "[") {
		t.Errorf("expected json array for routes, got: %s", resRoutes)
	}

	// Events
	resEvents, err := Runtime(ctx, Hooks{}, map[string]any{"action": "events"})
	if err != nil {
		t.Fatalf("Runtime events failed: %v", err)
	}
	if !strings.Contains(resEvents, "[") {
		t.Errorf("expected json array for events, got: %s", resEvents)
	}

	// Correlate
	resCorr, err := Runtime(ctx, Hooks{}, map[string]any{
		"action":   "correlate",
		"service":  "payment-service",
		"severity": "high",
	})
	if err != nil {
		t.Fatalf("Runtime correlate failed: %v", err)
	}
	if !strings.Contains(resCorr, "wired") {
		t.Errorf("expected wired field in correlate response, got: %s", resCorr)
	}
}

func TestRuntimeUnknownAction(t *testing.T) {
	ctx := context.Background()
	_, err := Runtime(ctx, Hooks{}, map[string]any{
		"action": "unknown_action_123",
	})
	if err == nil || !strings.Contains(err.Error(), "unknown action") {
		t.Fatalf("expected error on unknown action, got: %v", err)
	}
}
