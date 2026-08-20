package incident

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/memory"
	"github.com/JayveerPrajapati/kern/internal/runtime"
)

// TestMVP3GateEndToEnd runs the MVP3 killer workflow (spec §46, Workflow D):
//
//	Alert → Investigate → Root Cause → Candidate Fix → Sandbox → Verify → PR
//
// end-to-end against a tiny fixture module, wiring the real subsystems: the
// Phase 11 runtime intelligence (Source + correlation), the knowledge graph,
// engineering memory, the governance firewall, the execution worktree and the
// verification engine. This is the gate that must pass before the final-product
// expansion (Phases 14/15) begins.
func TestMVP3GateEndToEnd(t *testing.T) {
	root := gateFixture(t)

	// Production runtime: an alert for checkout, a recent deployment whose
	// commit touched the file the error events reference, and a metric/log.
	now := time.Now().Truncate(time.Second)
	st := runtime.NewStore()
	st.AddDeployment(domain.Deployment{Service: "checkout", CommitSHA: "abc123", Version: "v1.2.0", DeployedAt: now.Add(-15 * time.Minute)})
	st.AddCommit(runtime.Commit{SHA: "abc123", Message: "fix: tighten request validation", Author: "alice", Files: []string{"pkg/validate.go"}, CommittedAt: now.Add(-16 * time.Minute)})
	st.IngestAll([]runtime.Event{
		{ID: "e1", Type: runtime.EventError, Service: "checkout", Severity: "critical", Message: "panic in validator", Timestamp: now.Add(-1 * time.Minute), TraceID: "t1", Attributes: map[string]string{"file": "pkg/validate.go"}},
		{ID: "e2", Type: runtime.EventLog, Service: "checkout", Severity: "error", Message: "validation rejected", Timestamp: now.Add(-2 * time.Minute), TraceID: "t1"},
		{ID: "e3", Type: runtime.EventMetric, Service: "checkout", Message: "p99=200ms", Timestamp: now.Add(-2 * time.Minute)},
	})

	mem := memory.NewMemoryStore(t.TempDir())
	_, _ = mem.Add(domain.Memory{Type: domain.MemoryIncident, Content: "checkout 500s on the same date caused by a validation regression", Scope: "service:checkout", Tags: []string{"checkout"}})

	fw := governance.NewFirewall()
	fw.WithAgents(governance.NewAgent("sre", "SRE", "sre", []governance.Permission{{Resource: "prod", Action: "fix"}}))

	t.Log("[1/8] Indexing the fixture + building engine…")
	start := time.Now()
	eng, err := NewEngine(root, st, mem, fw)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	t.Logf("      indexed + graph built (%.1fs)", time.Since(start).Seconds())

	t.Log("[2/8] Alert → ingest incident…")
	inc := eng.IngestAlert(domain.Alert{ID: "al-1", Severity: domain.SeverityCritical, Message: "checkout is failing (500s)", Service: "checkout", OccurredAt: now})
	if inc.Status != domain.IncidentOpen {
		t.Fatal("incident not OPEN after ingest")
	}

	t.Log("[3/8] Investigate → correlate to affected service + runtime evidence…")
	eng.Correlate(inc)

	t.Log("[4/8] Root cause → hypotheses + evidence…")
	eng.RootCause(inc)

	t.Log("[5/8] Candidate fix → sandbox worktree, diff, verify…")
	diff, err := eng.ApplyAndVerifyFix(inc, func(workDir string) error {
		p := filepath.Join(workDir, "greet.go")
		return os.WriteFile(p, []byte("package main\n\nfunc Greet(name string) string { return \"hi \" + name }\n"), 0o644)
	})
	if err != nil {
		t.Fatalf("ApplyAndVerifyFix: %v", err)
	}
	fixVerified := inc.Verification != "" && strings.Contains(inc.Verification, "PASS")

	t.Log("[6/8] Human approval gate (production change)…")
	ap := eng.RequestApproval(inc, "sre", "apply verified fix to production")
	approved := false
	if ap.ID != "" {
		if _, err := eng.Approve(ap.ID, "oncall-human"); err == nil {
			approved = true
		}
	}

	t.Log("[7/8] Create PR…")
	prBody := eng.CreatePR(inc)

	t.Log("[8/8] === MVP3 GATE ACCEPTANCE CHECK ===")
	checks := []struct {
		name   string
		pass   bool
		detail string
	}{
		{"Affected service resolved", inc.AffectedService == "checkout", inc.AffectedService},
		{"Root cause found (deploy regression)", inc.RootCause != nil && inc.RootCause.Hypothesis.Source == "deploy", rootCauseSummary(inc)},
		{"Hypotheses produced", len(inc.Hypotheses) > 0, plural(len(inc.Hypotheses), "hypothesis")},
		{"Runtime evidence attached", hasRuntimeEvidence(inc), "runtime evidence"},
		{"Candidate fix diff generated (sandbox)", diff != "" && inc.FixDiff != "", "diff"},
		{"Fix verified (build pass)", fixVerified && inc.Verification != "", inc.Verification},
		{"Human approval granted", approved, "approval gate"},
		{"PR generated", prBody != "" && inc.Status == domain.IncidentPRCreated, string(inc.Status)},
	}
	allPass := true
	for _, c := range checks {
		status := "PASS"
		if !c.pass {
			status = "FAIL"
			allPass = false
		}
		t.Logf("  [%s] %-34s %s", status, c.name, c.detail)
	}
	if !allPass {
		t.Error("=== MVP3 GATE: SOME CHECKS FAILED ===")
	} else {
		t.Log("=== MVP3 GATE: ALL CHECKS PASSED ===")
	}
}

func gateFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"go.mod": "module gatefixture\n\ngo 1.20\n",
		"main.go": `package main

func helper() string { return "h" }

func main() { println(helper()) }
`,
		"main_test.go": `package main

import "testing"

func TestHelper(t *testing.T) {
	if helper() != "h" {
		t.Fail()
	}
}
`,
	}
	for rel, content := range files {
		p := filepath.Join(dir, rel)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}
	return dir
}

func hasRuntimeEvidence(inc *domain.Incident) bool {
	for _, e := range inc.Evidence {
		if e.Type == domain.EvidenceRuntime {
			return true
		}
	}
	return false
}

func rootCauseSummary(inc *domain.Incident) string {
	if inc.RootCause == nil {
		return "none"
	}
	return inc.RootCause.Summary
}

func plural(n int, unit string) string {
	switch unit {
	case "hypothesis":
		if n == 1 {
			return "1 hypothesis"
		}
		return fmt.Sprintf("%d hypotheses", n)
	}
	if n == 1 {
		return "1 " + unit
	}
	return strings.TrimSuffix(unit, "y") + "ies"
}
