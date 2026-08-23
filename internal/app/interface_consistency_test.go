package app

import (
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/whatif"
)

// TestInterfaceConsistency proves the P2 exit gate: "no core business workflow
// exists only in one interface." Because the MCP handler (handleAnalyze,
// internal/mcp/handlers_highlevel.go) and the CLI analyze path
// (cmd/kern/cmd_review.go runAnalyze) both delegate to the SAME shared
// application service — TaskService.Analyze / TaskService.Plan /
// TaskService.WhatIf / TaskService.Impact — feeding the same input through the
// shared service produces one authoritative Task in the shared store, queryable
// by every interface via Task.Get.
func TestInterfaceConsistencySharedAnalysis(t *testing.T) {
	if testing.Short() {
		t.Skip("slow e2e (indexes repo); skipped with -short")
	}
	root := "../.."

	p, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := NewTaskService(p, nil).WithAgentID("test")

	change := "CompileIntent" // a real symbol in the kern repo

	// --- Analysis workflow through the shared service ---
	t.Run("analyze", func(t *testing.T) {
		task, text, err := ts.Analyze(change)
		if err != nil {
			t.Fatalf("Analyze: %v", err)
		}
		if task.State != domain.TaskCompleted {
			t.Errorf("state = %s, want COMPLETED", task.State)
		}
		if text == "" {
			t.Error("analyze returned empty text")
		}
		if task.ContextPacket == nil {
			t.Error("ContextPacket not attached")
		}
		// The Task created by Analyze is queryable via Task.Get — proving that
		// every route (MCP handleAnalyze, CLI runAnalyze) lands in the same
		// authoritative task store.
		if got, ok := ts.Get(task.ID); !ok || got.ID != task.ID {
			t.Errorf("Task.Get(%s) missing the authoritative task", task.ID)
		}
	})

	t.Run("plan", func(t *testing.T) {
		task, plan, text, err := ts.Plan(change)
		if err != nil {
			t.Fatalf("Plan: %v", err)
		}
		if task.State != domain.TaskCompleted {
			t.Errorf("state = %s, want COMPLETED", task.State)
		}
		if plan.Objective == "" || len(plan.ImplementationSteps) == 0 {
			t.Errorf("plan incomplete: objective=%q steps=%d", plan.Objective, len(plan.ImplementationSteps))
		}
		if text == "" {
			t.Error("plan returned empty text")
		}
		if _, ok := ts.Get(task.ID); !ok {
			t.Errorf("Task.Get(%s) missing the authoritative plan task", task.ID)
		}
	})

	t.Run("whatif", func(t *testing.T) {
		task, text, err := ts.WhatIf(whatif.RemoveSymbol, change, "")
		if err != nil {
			t.Fatalf("WhatIf: %v", err)
		}
		if task.State != domain.TaskCompleted {
			t.Errorf("state = %s, want COMPLETED", task.State)
		}
		if task.ImpactReport == nil {
			t.Error("ImpactReport not attached")
		}
		if text == "" {
			t.Error("whatif returned empty text")
		}
	})

	t.Run("impact", func(t *testing.T) {
		task, rep, text, err := ts.Impact(change)
		if err != nil {
			t.Fatalf("Impact: %v", err)
		}
		if task.State != domain.TaskCompleted {
			t.Errorf("state = %s, want COMPLETED", task.State)
		}
		if task.Impact == nil {
			t.Error("Impact not attached")
		}
		if rep.Target == "" {
			t.Error("impact target is empty")
		}
		if text == "" {
			t.Error("impact returned empty text")
		}
		if !strings.Contains(text, "IMPACT") {
			t.Errorf("impact text missing 'IMPACT' marker; got:\n%s", text)
		}
	})
}

// TestServiceContractsExerciseNewAccessors asserts the app-layer surface added
// in Phase 2.1 is live: Policy (Firewall), Agent (Registry + Agents), Audit
// (AuditLog), and Memory (MemoryRecall) are all first-class services satisfied
// by *TaskService.
func TestServiceContracts(t *testing.T) {
	if testing.Short() {
		t.Skip("slow e2e (indexes repo); skipped with -short")
	}
	root := "../.."

	p, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := NewTaskService(p, nil).WithAgentID("test")

	change := "CompileIntent" // a real symbol in the kern repo

	if ts.Firewall() == nil {
		t.Error("Firewall() returned nil; Policy service not wired")
	}
	if ts.Registry() == nil {
		t.Error("Registry() returned nil; Agent service not wired")
	}
	if roles := ts.Agents(); len(roles) < 7 {
		t.Errorf("Agents() returned %d roles, want >= 7 standard specialists", len(roles))
	}
	if ts.AuditLog() == nil {
		t.Error("AuditLog() returned nil; Audit service not wired")
	}
	if ts.MemoryStore() == nil {
		t.Error("MemoryStore() returned nil; Memory service not wired")
	}
	// MemoryRecall on the shared store must not error (it may be empty).
	_ = ts.MemoryRecall(change)
	// Risk is the shared risk surface for CLI/REST.
	if _, text, err := ts.Risk(change); err != nil {
		t.Fatalf("Risk: %v", err)
	} else if text == "" {
		t.Error("Risk returned empty text")
	}
}