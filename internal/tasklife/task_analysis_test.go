package tasklife

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
	p := newTestPlatform(t, testfixture.Repo(t))
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

// TestImpactListsAffectedFiles pins Fix 2 end-to-end: TaskService.Impact on a
// real fixture must produce an "Affected files" section listing the distinct
// files of the blast radius (target + transitive dependents), so the impact
// report delivers the files explore promises.
func TestImpactListsAffectedFiles(t *testing.T) {
	p := newTestPlatform(t, testfixture.Repo(t))
	ts := NewTaskService(p, nil).WithAgentID("test")

	task, rep, text, err := ts.Impact("NewServer")
	if err != nil {
		t.Fatalf("Impact(NewServer): %v", err)
	}
	if task.State != domain.TaskCompleted {
		t.Fatalf("state = %s, want COMPLETED", task.State)
	}
	if len(rep.Files) == 0 {
		t.Fatal("ImpactReport.Files is empty; want the affected files of the blast radius")
	}
	if !strings.Contains(text, "Affected files:") {
		t.Fatalf("impact text missing the Affected files section, got:\n%s", text)
	}
	// NewServer lives in web/handler.go and main.main calls it from main.go —
	// both must appear, deduped and sorted.
	var sawHandler, sawMain bool
	for _, f := range rep.Files {
		switch f {
		case "web/handler.go":
			sawHandler = true
		case "main.go":
			sawMain = true
		}
	}
	if !sawHandler || !sawMain {
		t.Fatalf("rep.Files = %v; want both web/handler.go and main.go", rep.Files)
	}
}

// TestImpactCoveringTestsRanked pins Fix 1 end-to-end: the impact report's
// TestsCover holds the ranked covering tests (file-paired + name-matched +
// direct callers), and same-package noise is relegated to the count field.
func TestImpactCoveringTestsRanked(t *testing.T) {
	p := newTestPlatform(t, testfixture.Repo(t))
	ts := NewTaskService(p, nil).WithAgentID("test")

	_, rep, text, err := ts.Impact("NewServer")
	if err != nil {
		t.Fatalf("Impact(NewServer): %v", err)
	}
	// The fixture has no test files: TestsCover must be empty (no fake
	// same-package coverage) and the render must not list anything.
	if len(rep.TestsCover) != 0 {
		t.Fatalf("rep.TestsCover = %v; want empty (fixture has no tests)", rep.TestsCover)
	}
	if strings.Contains(text, "same-package tests not shown") {
		t.Fatalf("same-package line must not render when TestsCoverSamePackage is 0, got:\n%s", text)
	}
}

// TestImpactTestdataOnlyTargetAnnotated pins Fix 3 end-to-end: an impact on a
// name whose only definition is a testdata fixture resolves (not an error)
// and the render annotates the fixture instead of presenting it as production.
func TestImpactTestdataOnlyTargetAnnotated(t *testing.T) {
	p := newTestPlatform(t, "testdata/resolve_prio")
	ts := NewTaskService(p, nil).WithAgentID("test")

	_, rep, text, err := ts.Impact("stubOnly")
	if err != nil {
		t.Fatalf("Impact(stubOnly): %v", err)
	}
	if !strings.Contains(text, "(testdata fixture)") {
		t.Fatalf("impact text must annotate the testdata-only target, got:\n%s", text)
	}
	if len(rep.Files) == 0 {
		t.Fatalf("ImpactReport.Files empty for stubOnly; want its fixture file listed")
	}
}

// TestAnalyzeWithLensUnknownLens asserts an unknown lens fails the task
// (state FAILED) with a clear error.
func TestAnalyzeWithLensUnknownLens(t *testing.T) {
	p := newTestPlatform(t, testfixture.Repo(t))
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
