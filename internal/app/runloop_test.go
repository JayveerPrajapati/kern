package app

import (
	"testing"

	"github.com/JayveerPrajapati/kern/internal/loop"
)

// TestRunLoopRoutesThroughService verifies kern_loop now routes through
// TaskService.RunLoop (Phase 2.2): an authoritative Task is created and a loop
// Result is returned, so the MCP handler no longer orchestrates the loop engine
// inline. The loop itself runs build/test commands, so the assertion is about
// routing and task-tracking, not the loop's internal outcome.
func TestRunLoopRoutesThroughService(t *testing.T) {
	ts := NewTaskService(&Platform{root: t.TempDir()}, nil)

	task, res, _ := ts.RunLoop("explain the caching strategy", loop.L0)

	if task == nil {
		t.Fatal("RunLoop returned nil task")
	}
	if res == nil {
		t.Fatal("RunLoop returned nil result")
	}
	if res.Intent == "" {
		t.Error("Result.Intent is empty")
	}
	if len(res.Stages) == 0 {
		t.Error("Result.Stages is empty; the loop did not record any stage")
	}
	// The task must exist in the registry (task-tracking is the point).
	if _, ok := ts.Get(task.ID); !ok {
		t.Errorf("task %s was not tracked by the service", task.ID)
	}
}
