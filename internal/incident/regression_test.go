package incident

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/memory"
)

// TestInjectRegressionFullPipeline is the Phase 11.1 proof point: an explicitly
// injected known regression flows through the whole incident pipeline and
// resolves deterministically to the injected symbol. It asserts:
//
//	inject → alert → correlate → root cause identifies the injected regression
//	file/symbol → candidate fix passes verification.
func TestInjectRegressionFullPipeline(t *testing.T) {
	root := fixtureRoot(t)
	mem := memory.NewMemoryStore(t.TempDir())
	fw := governance.NewFirewall()
	// Give a fixer a "fix" permission on prod so the governance risk step does
	// not block the sandboxed candidate fix (same shape as TestEnginePipeline).
	fw.WithAgents(governance.NewAgent("sre", "SRE", "sre", []governance.Permission{{Resource: "prod", Action: "fix"}}))

	const regressionFile = "pkg/validate.go"
	eng, alert, err := InjectRegression(context.Background(), root, "checkout", regressionFile, mem, fw)
	if err != nil {
		t.Fatalf("InjectRegression: %v", err)
	}

	// --- inject → alert ---
	inc := eng.IngestAlert(alert)
	if inc.Status != domain.IncidentOpen {
		t.Fatalf("status = %q, want OPEN", inc.Status)
	}

	// --- correlate ---
	eng.Correlate(inc)
	if inc.AffectedService != "checkout" {
		t.Fatalf("affected service = %q, want checkout", inc.AffectedService)
	}

	// --- root cause identifies the injected regression symbol/file ---
	eng.RootCause(inc)
	if len(inc.Hypotheses) == 0 {
		t.Fatal("expected root-cause hypotheses")
	}
	if inc.RootCause == nil {
		t.Fatal("expected a stated root cause for an injected regression")
	}
	if !strings.Contains(inc.RootCause.Summary, regressionFile) {
		t.Errorf("root cause summary %q should reference injected regression %q", inc.RootCause.Summary, regressionFile)
	}
	// The top-ranked deploy hypothesis should be corroborated by the injected
	// error event referencing the changed file.
	if !strings.Contains(inc.Hypotheses[0].Statement, regressionFile) {
		t.Errorf("top hypothesis %q should reference injected regression %q", inc.Hypotheses[0].Statement, regressionFile)
	}
	if inc.Status != domain.IncidentRootCauseFound {
		t.Fatalf("status = %q, want ROOT_CAUSE_FOUND", inc.Status)
	}

	// --- candidate fix passes verification ---
	diff, err := eng.ApplyAndVerifyFix(inc, func(workDir string) error {
		p := filepath.Join(workDir, "fix.go")
		return os.WriteFile(p, []byte("package main\n\nfunc Greet(name string) string { return \"hi \" + name }\n"), 0o644)
	})
	if err != nil {
		t.Fatalf("ApplyAndVerifyFix: %v", err)
	}
	if diff == "" {
		t.Fatal("expected a non-empty diff for the candidate fix")
	}
	if inc.Status != domain.IncidentFixVerified {
		t.Fatalf("status = %q, want FIX_VERIFIED", inc.Status)
	}
	if inc.Verification == "" {
		t.Fatal("expected a verification summary")
	}

	// Guard against a vacuous timestamp: the injection must be recent.
	if time.Since(alert.OccurredAt) > time.Hour {
		t.Errorf("alert OccurredAt too old: %v", alert.OccurredAt)
	}
}
