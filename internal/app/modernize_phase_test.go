package app

import (
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/modernization"
)

// TestRenderModernizePhaseText verifies the per-phase render shows
// the phase number, context, ownership, risk, and task id.
func TestRenderModernizePhaseText(t *testing.T) {
	phase := modernization.ExtractionPhase{
		Phase: 2, Context: "billing", Ownership: "@billing", RiskLevel: "medium",
		BlastRadius: 12, TaskID: "t-9",
	}
	text := renderModernizePhaseText(phase)
	for _, want := range []string{"PHASE 2", "billing", "@billing", "medium", "t-9"} {
		if !strings.Contains(text, want) {
			t.Errorf("phase render missing %q:\n%s", want, text)
		}
	}
}

// TestRenderModernizeCandidates verifies the candidate visualization (Phase
// 12.4) renders a line per context with cohesion/deps and per-phase lines.
func TestRenderModernizeCandidates(t *testing.T) {
	plan := modernization.ExtractionPlan{
		Contexts: []modernization.BoundedContext{
			{Name: "checkout", FileCount: 4, Cohesion: 0.8, IncomingDeps: 1, OutgoingDeps: 2, Ownership: "@checkout"},
			{Name: "billing", FileCount: 3, Cohesion: 0.6, IncomingDeps: 2, OutgoingDeps: 1, Ownership: "@billing"},
		},
		Phases: []modernization.ExtractionPhase{
			{Phase: 1, Context: "billing", RiskLevel: "low", TaskID: "t-1"},
		},
	}
	text := renderModernizeCandidates(plan)
	for _, want := range []string{"CANDIDATES", "checkout", "@checkout", "cohesion", "phase 1", "billing"} {
		if !strings.Contains(text, want) {
			t.Errorf("candidate render missing %q:\n%s", want, text)
		}
	}
}

// TestPhaseTaskIDIsSetByModernizePhaseTasks verifies that ModernizePhaseTasks
// materializes one task per phase and sets the phase TaskID so the audit trail
// can trace a phase to its task .
func TestPhaseTaskIDIsSetByModernizePhaseTasks(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping full-platform modernize in -short mode")
	}
	// Drive the real platform against the repo so phase tasks are real tasks.
	p, err := NewWithIndex("../..", sharedTestRepoIndex(t))
	if err != nil {
		t.Skipf("New: %v", err)
	}
	ts := NewTaskService(p, nil).WithAgentID("test")
	_, plan, _, err := ts.Modernize()
	if err != nil || len(plan.Phases) == 0 {
		t.Skipf("modernization produced no phases on this repo (err=%v)", err)
	}
	tasks, err := ts.ModernizePhaseTasks(plan, "")
	if err != nil {
		t.Fatalf("ModernizePhaseTasks: %v", err)
	}
	if len(tasks) != len(plan.Phases) {
		t.Errorf("created %d phase tasks, want %d", len(tasks), len(plan.Phases))
	}
	// The plan's phases must now carry task ids.
	for i, ph := range plan.Phases {
		if ph.TaskID == "" {
			t.Errorf("phase %d task id not set (12.3)", i)
		}
	}
}
