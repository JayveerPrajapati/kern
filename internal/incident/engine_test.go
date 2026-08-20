package incident

import (
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

// fixtureRoot writes a tiny standalone Go module and returns its root.
func fixtureRoot(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"go.mod": "module incidentfix\n\ngo 1.20\n",
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

// incidentFixture builds a runtime source describing a checkout regression and a
// memory store with a related historical incident.
func incidentFixture(t *testing.T) (*runtime.Store, *memory.MemoryStore) {
	t.Helper()
	now := time.Now().Truncate(time.Second)
	st := runtime.NewStore()
	st.AddDeployment(domain.Deployment{Service: "checkout", CommitSHA: "abc123", Version: "v1.2.0", DeployedAt: now.Add(-15 * time.Minute)})
	st.AddCommit(runtime.Commit{SHA: "abc123", Message: "fix: tighten request validation", Author: "alice", Files: []string{"pkg/validate.go"}, CommittedAt: now.Add(-16 * time.Minute)})
	st.IngestAll([]runtime.Event{
		{ID: "e1", Type: runtime.EventError, Service: "checkout", Severity: "critical", Message: "panic in validator", Timestamp: now.Add(-1 * time.Minute), Attributes: map[string]string{"file": "pkg/validate.go"}},
	})

	m := memory.NewMemoryStore(t.TempDir())
	_, _ = m.Add(domain.Memory{Type: domain.MemoryIncident, Content: "checkout 500s on 2026-07-01 caused by a validation regression", Scope: "service:checkout"})
	return st, m
}

func TestEnginePipeline(t *testing.T) {
	root := fixtureRoot(t)
	st, mem := incidentFixture(t)
	fw := governance.NewFirewall()
	fw.WithAgents(governance.NewAgent("sre", "SRE", "sre", []governance.Permission{{Resource: "prod", Action: "fix"}}))

	eng, err := NewEngine(root, st, mem, fw)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	alert := domain.Alert{ID: "a1", Severity: domain.SeverityCritical, Message: "checkout is failing", Service: "checkout", OccurredAt: time.Now()}
	inc := eng.IngestAlert(alert)
	if inc.Status != domain.IncidentOpen {
		t.Fatalf("status = %q, want OPEN", inc.Status)
	}

	eng.Correlate(inc)
	if inc.AffectedService != "checkout" {
		t.Fatalf("affected service = %q", inc.AffectedService)
	}
	if len(inc.RelatedDeployments) == 0 {
		t.Fatal("no related deployments")
	}

	eng.RootCause(inc)
	if len(inc.Hypotheses) == 0 {
		t.Fatal("no hypotheses produced")
	}
	if inc.RootCause == nil {
		t.Fatal("no root cause selected")
	}
	if inc.Status != domain.IncidentRootCauseFound {
		t.Fatalf("status = %q, want ROOT_CAUSE_FOUND", inc.Status)
	}

	// Candidate fix applied in the sandbox and verified (build passes).
	diff, err := eng.ApplyAndVerifyFix(inc, func(workDir string) error {
		p := filepath.Join(workDir, "greet.go")
		return os.WriteFile(p, []byte("package main\n\nfunc Greet(name string) string { return \"hi \" + name }\n"), 0o644)
	})
	if err != nil {
		t.Fatalf("ApplyAndVerifyFix: %v", err)
	}
	if diff == "" {
		t.Fatal("expected a non-empty diff")
	}
	if inc.Status != domain.IncidentFixVerified {
		t.Fatalf("status = %q, want FIX_VERIFIED", inc.Status)
	}
	if inc.Verification == "" {
		t.Fatal("expected verification summary")
	}

	// Human approval gate for the production change.
	ap := eng.RequestApproval(inc, "sre", "apply fix to production")
	if ap.ID == "" {
		t.Fatal("expected a pending approval")
	}
	_, err = eng.Approve(ap.ID, "human-oncall")
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}

	pr := eng.CreatePR(inc)
	if pr == "" {
		t.Fatal("expected a PR body")
	}
	if inc.Status != domain.IncidentPRCreated {
		t.Fatalf("status = %q, want PR_CREATED", inc.Status)
	}
	if len(inc.Evidence) == 0 {
		t.Fatal("expected evidence attached to the incident")
	}
}

func TestRootCausePrefersChangedFileRegression(t *testing.T) {
	root := fixtureRoot(t)
	st, mem := incidentFixture(t)
	eng, err := NewEngine(root, st, mem, governance.NewFirewall())
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	inc := eng.IngestAlert(domain.Alert{ID: "a2", Severity: domain.SeverityError, Service: "checkout", OccurredAt: time.Now()})
	eng.Correlate(inc)
	eng.RootCause(inc)

	top := inc.Hypotheses[0]
	if top.Source != "deploy" {
		t.Fatalf("top hypothesis source = %q, want deploy", top.Source)
	}
	if !contains(top.Statement, "pkg/validate.go") {
		t.Fatalf("top hypothesis should reference changed file: %q", top.Statement)
	}
}
func contains(s, sub string) bool {
	return strings.Contains(s, sub)
}
