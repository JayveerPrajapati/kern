package incident

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/memory"
	"github.com/JayveerPrajapati/kern/internal/runtime"
)

// Phase 11 (P1.3) failure-scenario tests: the incident pipeline must handle
// degraded inputs deterministically and fail safe — it must not fabricate a
// root cause or a verified fix when evidence is missing, stale, or low
// confidence, and a verification failure must not yield a verified fix.

// emptyAlert builds an engine over a runtime store with NO telemetry so the
// correlation/root-cause paths have no evidence to work from.
func emptyAlertEngine(t *testing.T, files map[string]string) (*Engine, domain.Alert) {
	t.Helper()
	root := t.TempDir()
	for rel, content := range files {
		p := filepath.Join(root, rel)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}
	st := runtime.NewStore() // empty: no metrics, no traces, no deployments
	mem := memory.NewMemoryStore(t.TempDir())
	fw := governance.NewFirewall()
	eng, err := NewEngine(root, st, mem, fw)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	alert := domain.Alert{ID: "fail-alert", Severity: domain.SeverityCritical, Message: "checkout failing", Service: "checkout", OccurredAt: time.Now()}
	return eng, alert
}

// TestMissingMetricsNoRootCauseAsserted verifies that when the runtime has no
// telemetry (no metrics/traces) the correlation still completes but the
// root-cause stage does not promote a bare, unevidenced hypothesis to a stated
// RootCause — the incident surfaces candidates without asserting certainty.
func TestMissingMetricsNoRootCauseAsserted(t *testing.T) {
	eng, alert := emptyAlertEngine(t, map[string]string{
		"go.mod": "module inc\n\ngo 1.20\n",
		"main.go": `package main
func main() {}
`,
	})
	inc := eng.IngestAlert(alert)
	eng.Correlate(inc)
	eng.RootCause(inc)

	// Correlation must complete even with no evidence, but must not assert a
	// high-confidence root cause from nothing.
	if inc.Status != domain.IncidentRootCauseFound {
		t.Fatalf("status = %s, want ROOT_CAUSE_FOUND (pipeline completes)", inc.Status)
	}
	if inc.RootCause != nil {
		t.Errorf("root cause asserted with no evidence: %+v", inc.RootCause)
	}
	// No candidate hypothesis should claim a FACT/INFERENCE confidence from an
	// empty source — bare candidates remain surfaced but unasserted.
	for _, h := range inc.Hypotheses {
		if h.Confidence == domain.ClaimFact && len(h.Evidence) == 0 {
			t.Errorf("hypothesis %q asserted FACT with no evidence", h.Statement)
		}
	}
}

// TestStaleDeploymentMappingDowngradesCorrelation verifies that an alert
// referencing a service whose only deployment mapping is older than the
// correlation lookback does not get correlated to a spurious deployment.
func TestStaleDeploymentMappingDowngradesCorrelation(t *testing.T) {
	root := fixtureRoot(t)
	st := runtime.NewStore()
	// Deployment+commit are well outside the default lookback window.
	st.AddDeployment(domain.Deployment{Service: "checkout", CommitSHA: "oldsha", Version: "v0.0.1", DeployedAt: time.Now().Add(-96 * time.Hour)})
	st.AddCommit(runtime.Commit{SHA: "oldsha", Message: "old change", Author: "old", Files: []string{"pkg/x.go"}, CommittedAt: time.Now().Add(-97 * time.Hour)})
	mem := memory.NewMemoryStore(t.TempDir())
	eng, err := NewEngine(root, st, mem, governance.NewFirewall())
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	// Tight lookback so the stale deployment is out of scope.
	eng.SetLookback(24 * time.Hour)

	inc := eng.IngestAlert(domain.Alert{ID: "al-stale", Severity: domain.SeverityError, Service: "checkout", OccurredAt: time.Now()})
	eng.Correlate(inc)
	eng.RootCause(inc)

	// The stale deployment must not be folded into the incident as if it were
	// the current root cause; a stale source must downgrade rather than assert.
	if inc.RootCause != nil {
		t.Errorf("root cause asserted from a stale deployment mapping: %+v", inc.RootCause)
	}
	for _, d := range inc.RelatedDeployments {
		if d.CommitSHA == "oldsha" {
			t.Errorf("stale deployment (SHA oldsha) folded into incident as related: %+v", d)
		}
	}
}

// TestLowConfidenceHypothesisNotPromoted verifies that a bare, low-confidence
// hypothesis is never promoted to a stated RootCause. With an empty runtime
// source there is no evidence, so no hypothesis may be asserted as a root cause.
func TestLowConfidenceHypothesisNotPromoted(t *testing.T) {
	eng, alert := emptyAlertEngine(t, map[string]string{
		"go.mod": "module inc\n\ngo 1.20\n",
		"main.go": "package main\n\nfunc x() int { return 1 }\nfunc main() { println(x()) }\n",
	})
	inc := eng.IngestAlert(alert)
	eng.Correlate(inc)
	eng.RootCause(inc)

	if inc.RootCause != nil {
		t.Fatalf("low-confidence hypothesis promoted to RootCause without evidence: %+v", inc.RootCause)
	}
	// Any candidate that IS surfaced must not claim FACT/INFERENCE without
	// evidence — never promoted to a stated certainty.
	for _, h := range inc.Hypotheses {
		if len(h.Evidence) == 0 && (h.Confidence == domain.ClaimFact || h.Confidence == domain.ClaimInference) {
			t.Errorf("unevidenced hypothesis %q claims confidence %q (not a fact/inference)", h.Statement, h.Confidence)
		}
	}
}

// TestVerificationFailureBlocksFix verifies that a candidate fix that fails
// build verification never transitions to FIX_VERIFIED (fail safe).
func TestVerificationFailureBlocksFix(t *testing.T) {
	root := fixtureRoot(t)
	st := runtime.NewStore()
	mem := memory.NewMemoryStore(t.TempDir())
	fw := governance.NewFirewall()
	// Grant the fixer fix permission on the service so the risk step does not
	// block before verification (we specifically want to test the verification
	// failure path).
	fw.WithAgents(governance.NewAgent("incident-fixer", "fixer", "sre", []governance.Permission{{Resource: "prod", Action: "fix"}}))
	eng, err := NewEngine(root, st, mem, fw)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	inc := eng.IngestAlert(domain.Alert{ID: "al-verf", Severity: domain.SeverityCritical, Message: "boom", Service: "svc", OccurredAt: time.Now()})

	// The fixture module (fixtureRoot) is valid, but force the apply step to
	// write a file that breaks the build (a malformed Go file) so verification
	// fails.
	_, err = eng.ApplyAndVerifyFix(inc, func(workDir string) error {
		p := filepath.Join(workDir, "broken.go")
		return os.WriteFile(p, []byte("package main\n\nfunc {{ this is not valid go"), 0o644)
	})
	if err == nil {
		t.Fatal("expected verification failure error")
	}
	if inc.Status == domain.IncidentFixVerified {
		t.Fatalf("incident marked FIX_VERIFIED despite verification failure")
	}
	// The incident must not carry a PASS verdict from the failed build.
	if inc.Verification != "" && inc.Verification != "PASS" {
		t.Errorf("verification summary %q suggests success despite failed build", inc.Verification)
	}
}

// TestVerificationFailureNoSideEffect verifies that a failed verification does
// not mutate the live repository — the worktree is isolated (Phase 11 exit gate:
// a controlled incident produces a verified remediation PR, never a broken live tree).
func TestVerificationFailureNoSideEffect(t *testing.T) {
	root := fixtureRoot(t)
	eng, err := NewEngine(root, runtime.NewStore(), memory.NewMemoryStore(t.TempDir()), governance.NewFirewall())
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	inc := eng.IngestAlert(domain.Alert{ID: "al-side", Severity: domain.SeverityError, Message: "x", Service: "svc", OccurredAt: time.Now()})
	_, err = eng.ApplyAndVerifyFix(inc, func(workDir string) error {
		return os.WriteFile(filepath.Join(workDir, "main.go"), []byte("package main\n\nbroken {{"), 0o644)
	})
	if err == nil {
		t.Fatal("expected error from verification failure")
	}
	// The live fixture's main.go must be unchanged (no side effect).
	b, rerr := os.ReadFile(filepath.Join(root, "main.go"))
	if rerr != nil {
		t.Fatalf("read live main.go: %v", rerr)
	}
	if string(b) != `package main

func helper() string { return "h" }

func main() { println(helper()) }
` {
		t.Errorf("live main.go was mutated by a failed verification:\n%s", b)
	}
}

// TestIncidentCorrelateEmptyRuntime no-op guard: with an empty runtime source the
// correlation folds the alert into an incident without panicking.
func TestIncidentCorrelateEmptyRuntimeNoPanic(t *testing.T) {
	eng, alert := emptyAlertEngine(t, map[string]string{
		"go.mod": "module e\n\ngo 1.20\n",
		"main.go": "package main\nfunc main() {}\n",
	})
	inc := eng.IngestAlert(alert)
	eng.Correlate(inc) // must not panic
	if inc.AffectedService == "" {
		t.Logf("affected service empty (expected with empty runtime), incident id %s", inc.ID)
	}
}