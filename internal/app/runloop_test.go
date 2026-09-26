package app

import (
	"context"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/loop"
)

// TestRunLoopRoutesThroughService verifies kern_loop now routes through
// TaskService.RunLoop : an authoritative Task is created and a loop
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

// TestRunLoopWiresLearningByDefault verifies the app builder wires the
// continuous-learning extractor into the loop whenever a memory store exists
// (single wiring point covering kern_loop/kern_do MCP + CLI loop/do). At L1
// the learn stage runs and writes the run's own lesson + episodic memories,
// which form a recurring scope pattern above the default threshold of 1, so
// Result.LearnedConstraints must be populated — proof the extractor is not
// left nil.
func TestRunLoopWiresLearningByDefault(t *testing.T) {
	ts := NewTaskService(&Platform{root: t.TempDir()}, nil)
	task, res, err := ts.RunLoopContext(context.Background(), "explain the caching strategy", loop.L1)
	if err != nil {
		t.Fatalf("RunLoopContext: %v", err)
	}
	if task == nil || res == nil {
		t.Fatal("RunLoopContext returned nil task or result")
	}
	if len(res.LearnedConstraints) == 0 {
		t.Fatal("expected the wired learn stage to surface constraints into Result.LearnedConstraints")
	}
}

// TestRunLoopContextCancelled locks the oracle-gate ctx threading: a cancelled
// context must stop RunLoopContext BEFORE any stage runs, and the aborted run
// must still be observable — the created Task is marked FAILED (terminal), not
// silently abandoned.
func TestRunLoopContextCancelled(t *testing.T) {
	ts := NewTaskService(&Platform{root: t.TempDir()}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancelled before the run starts

	task, res, err := ts.RunLoopContext(ctx, "explain the caching strategy", loop.L0)
	if err == nil {
		t.Fatal("RunLoopContext with a cancelled context must return the cancellation error")
	}
	if res != nil {
		t.Error("RunLoopContext with a cancelled context must not run any stage")
	}
	if task == nil {
		t.Fatal("RunLoopContext must still create a Task so the cancelled run is observable")
	}
	if task.State != domain.TaskFailed {
		t.Errorf("cancelled run task state = %s, want FAILED (terminal and observable)", task.State)
	}
}

// TestRunDoRoutesThroughService verifies `kern do` now routes through
// TaskService.RunDo (autonomous loop): an authoritative Task is created and a
// loop Result is returned, so the CLI no longer orchestrates the loop engine
// (coder/planner/memory/flight) inline. The loop itself runs build/test
// commands, so the assertion is about routing and task-tracking, not the
// loop's internal outcome. L0 is used so the coder/planner stage gates (>= L2)
// never invoke a live LLM.
func TestRunDoRoutesThroughService(t *testing.T) {
	ts := NewTaskService(&Platform{root: t.TempDir()}, nil)

	task, res, _ := ts.RunDo("explain the caching strategy", loop.L0)

	if task == nil {
		t.Fatal("RunDo returned nil task")
	}
	if res == nil {
		t.Fatal("RunDo returned nil result")
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
