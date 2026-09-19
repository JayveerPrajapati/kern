package flight

import (
	"context"
	"strings"
	"testing"
)

func TestFlightEmptyTask(t *testing.T) {
	ctx := context.Background()
	_, err := Flight(ctx, map[string]any{
		"task": "",
	})
	if err == nil || !strings.Contains(err.Error(), "task is required") {
		t.Fatalf("expected error on empty task, got: %v", err)
	}
}

func TestFlightNonExistentTask(t *testing.T) {
	ctx := context.Background()
	res, err := Flight(ctx, map[string]any{
		"root": t.TempDir(),
		"task": "non-existent-task-123",
	})
	if err != nil {
		t.Fatalf("Flight failed: %v", err)
	}
	if !strings.Contains(res, "no flight records") && !strings.Contains(res, "Trail") {
		t.Errorf("unexpected Flight output: %q", res)
	}
}
