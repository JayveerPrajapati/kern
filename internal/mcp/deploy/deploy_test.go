package deploy

import (
	"context"
	"strings"
	"testing"
)

func TestDeployEmptyTask(t *testing.T) {
	ctx := context.Background()
	_, err := Deploy(ctx, Hooks{}, map[string]any{
		"task_id": "",
	})
	if err == nil || !strings.Contains(err.Error(), "task_id is required") {
		t.Fatalf("expected error on empty task_id, got: %v", err)
	}
}
