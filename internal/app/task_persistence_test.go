package app

import (
	"testing"

	"github.com/JayveerPrajapati/kern/internal/testfixture"
	"github.com/JayveerPrajapati/kern/internal/whatif"
)

// storeTaskCount returns how many task records the service's persisted store
// currently holds. The store is a JSON file under the cache dir keyed by the
// project root, shared by every TaskService built for the same root.
func storeTaskCount(t *testing.T, ts *TaskService) int {
	t.Helper()
	if ts.store == nil {
		t.Fatal("task service has no persisted store")
	}
	list, err := ts.store.List()
	if err != nil {
		t.Fatalf("store.List: %v", err)
	}
	return len(list)
}

// TestAnalysisCommandsDoNotPersistTaskRecords locks F9: read-only analysis
// commands (analyze/what-if/plan/impact) must NOT write task records to the
// persisted task store — they are workflow-state-free unless a caller
// explicitly opts in via WithTaskPersistence(true). Audit transitions and
// artifacts still record (they are the log); only the task record (workflow
// state) is skipped. Workflow commands (loop/do/execute) create tasks via
// Create and keep persisting as before.
func TestAnalysisCommandsDoNotPersistTaskRecords(t *testing.T) {
	p, err := New(testfixture.Repo(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := NewTaskService(p, nil).WithAgentID("test")
	before := storeTaskCount(t, ts)

	// analyze
	if _, _, err := ts.Analyze("NewServer"); err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if got := storeTaskCount(t, ts); got != before {
		t.Fatalf("Analyze persisted %d task record(s): store grew %d -> %d (want no growth)", got-before, before, got)
	}

	// what-if
	if _, _, err := ts.WhatIf(whatif.SplitService, "NewServer", ""); err != nil {
		t.Fatalf("WhatIf: %v", err)
	}
	if got := storeTaskCount(t, ts); got != before {
		t.Fatalf("WhatIf persisted %d task record(s): store grew %d -> %d (want no growth)", got-before, before, got)
	}

	// plan
	if _, _, _, err := ts.Plan("NewServer"); err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if got := storeTaskCount(t, ts); got != before {
		t.Fatalf("Plan persisted %d task record(s): store grew %d -> %d (want no growth)", got-before, before, got)
	}

	// impact
	if _, _, _, err := ts.Impact("NewServer"); err != nil {
		t.Fatalf("Impact: %v", err)
	}
	if got := storeTaskCount(t, ts); got != before {
		t.Fatalf("Impact persisted %d task record(s): store grew %d -> %d (want no growth)", got-before, before, got)
	}

	// The in-memory task lifecycle still works: the analysis tasks are
	// tracked in the registry even though no record was written.
	if tsk := ts.List(); len(tsk) != 4 {
		t.Fatalf("registry tasks = %d, want 4 (analyze/what-if/plan/impact)", len(tsk))
	}

	// Explicit opt-in restores persistence (the CLI --task flag / MCP
	// authoritative task-record surfaces call WithTaskPersistence(true)).
	ts2 := NewTaskService(p, nil).WithAgentID("test").WithTaskPersistence(true)
	before2 := storeTaskCount(t, ts2)
	if _, _, err := ts2.Analyze("NewServer"); err != nil {
		t.Fatalf("Analyze (opt-in): %v", err)
	}
	if got := storeTaskCount(t, ts2); got != before2+1 {
		t.Fatalf("opt-in Analyze should persist exactly 1 task record, store grew %d -> %d (want %d)", before2, got, before2+1)
	}

	// Workflow commands keep persisting: Run (the kern run entry) creates an
	// authoritative task record even on a default service.
	ts3 := NewTaskService(p, nil).WithAgentID("test")
	before3 := storeTaskCount(t, ts3)
	if _, err := ts3.Run("analyze NewServer"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := storeTaskCount(t, ts3); got != before3+1 {
		t.Fatalf("workflow Run should persist exactly 1 task record, store grew %d -> %d (want %d)", before3, got, before3+1)
	}
}
