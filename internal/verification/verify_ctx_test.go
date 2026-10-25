package verification

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/verdict"
)

// TestVerifyContextDeadlineKillsSubprocess pins the Phase 2 timeout
// propagation: a caller deadline on the engine kills the in-flight build
// subprocess (prompt return, no orphan) and Verify reports a PARTIAL result
// — the summary names the truncation and a truncated run never reads as a
// clean pass. The bare kern_verify path threads its caller deadline and the
// MCP call ctx through exactly this mechanism (highlevel.Verify →
// verification.WithContext).
func TestVerifyContextDeadlineKillsSubprocess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sleep command not available on windows")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module ctxtest\n\ngo 1.27\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KERN_VERIFY_BUILD", "sleep 30")
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	e := NewEngine(dir).WithContext(ctx)
	start := time.Now()
	res := e.Verify([]string{"build"})
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("Verify blocked on the killed subprocess: %s (deadline was 500ms)", elapsed)
	}
	if res.Build == nil {
		t.Fatal("build sub-result missing")
	}
	if res.Build.OK {
		t.Errorf("a deadline-killed build must not report OK: %+v", res.Build)
	}
	if !strings.Contains(res.Summary, "PARTIAL") {
		t.Errorf("summary must record the truncation, got: %q", res.Summary)
	}
	if res.Verdict == verdict.VerdictPass || res.Verdict == verdict.VerdictPassWithWarning {
		t.Errorf("a truncated run must never read as a pass, got verdict %s", res.Verdict)
	}
}
