package app

import (
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/testfixture"
)

// sameOrder reports whether two claim slices have identical Statement order.
func sameOrder(a, b []domain.Claim) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Statement != b[i].Statement {
			return false
		}
	}
	return true
}

// TestAnalyzeWithLensReordersFacts asserts the lens actually re-ranks the
// packet facts before rendering and task attachment: for a change with mixed
// evidence, the security lens changes fact order vs the balanced lens (which
// never reorders), and the balanced lens matches plain Analyze.
func TestAnalyzeWithLensReordersFacts(t *testing.T) {
	p, err := New(testfixture.Repo(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := NewTaskService(p, nil).WithAgentID("test")

	secTask, secText, err := ts.AnalyzeWithLens("NewServer", "security")
	if err != nil {
		t.Fatalf("AnalyzeWithLens(security): %v", err)
	}
	if secTask.State != domain.TaskCompleted {
		t.Errorf("state = %s, want COMPLETED", secTask.State)
	}
	if secTask.ContextPacket == nil || len(secTask.ContextPacket.Facts) == 0 {
		t.Fatal("ContextPacket facts not attached")
	}
	if secText == "" {
		t.Error("analyze returned empty text")
	}
	if secTask.CreatedBy != "test" {
		t.Errorf("CreatedBy = %q, want 'test'", secTask.CreatedBy)
	}

	balTask, _, err := ts.AnalyzeWithLens("NewServer", "balanced")
	if err != nil {
		t.Fatalf("AnalyzeWithLens(balanced): %v", err)
	}
	if sameOrder(secTask.ContextPacket.Facts, balTask.ContextPacket.Facts) {
		t.Error("security lens should reorder facts vs balanced for NewServer")
	}

	plainTask, _, err := ts.Analyze("NewServer")
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if !sameOrder(plainTask.ContextPacket.Facts, balTask.ContextPacket.Facts) {
		t.Error("balanced lens should not reorder facts vs plain Analyze")
	}
}

// TestAnalyzeWithLensUnknownLens asserts an unknown lens fails the task
// (state FAILED) with a clear error.
func TestAnalyzeWithLensUnknownLens(t *testing.T) {
	p, err := New(testfixture.Repo(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := NewTaskService(p, nil).WithAgentID("test")

	task, _, err := ts.AnalyzeWithLens("NewServer", "bogus-lens")
	if err == nil {
		t.Fatal("unknown lens should error")
	}
	if !strings.Contains(err.Error(), `unknown lens "bogus-lens"`) {
		t.Errorf("error = %q, want mention unknown lens", err.Error())
	}
	if task == nil {
		t.Fatal("task should be returned (failed)")
	}
	if task.State != domain.TaskFailed {
		t.Errorf("state = %s, want FAILED", task.State)
	}
}
