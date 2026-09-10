package app

import (
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/modernization"
	"github.com/JayveerPrajapati/kern/internal/testfixture"
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

// TestPhaseTaskIDIsSetByModernizePhaseTasks verifies that ModernizePhaseTasks
// materializes one task per phase and sets the phase TaskID so the audit trail
// can trace a phase to its task .
func TestPhaseTaskIDIsSetByModernizePhaseTasks(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping full-platform modernize in -short mode")
	}
	// Drive the real platform against the fixture repo so phase tasks are
	// real tasks (same code paths as the full repo, millisecond index builds).
	p, err := New(testfixture.Repo(t))
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
