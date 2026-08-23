package incident

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/memory"
	"github.com/JayveerPrajapati/kern/internal/prprovider"
	"github.com/JayveerPrajapati/kern/internal/runtime"
)

// fakePRProvider is a deterministic in-memory PR provider that always "creates"
// a PR with a fixed number/URL. It lets tests assert the PR wiring without
// hitting the network.
type fakePRProvider struct {
	num int
	url string
}

func (f fakePRProvider) CreatePR(req prprovider.Request) (*prprovider.Result, error) {
	return &prprovider.Result{Number: f.num, URL: f.url, State: "open"}, nil
}

// TestEngineCreateFixPRRequiresVerified verifies that CreateFixPR refuses to
// create a PR when the incident is not in FIX_VERIFIED state, and that no PR
// fields are stamped on the incident.
func TestEngineCreateFixPRRequiresVerified(t *testing.T) {
	root := fixtureRoot(t)
	eng, err := NewEngine(root, runtime.NewStore(), memory.NewMemoryStore(t.TempDir()), governance.NewFirewall())
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	eng.WithPRProvider(fakePRProvider{num: 3, url: "https://example.com/pr/3"})

	inc := &domain.Incident{ID: "inc-unverified", Title: "checkout flapping", Status: domain.IncidentFixProposed}
	if _, err := eng.CreateFixPR(inc, "fix/inc-unverified"); err == nil {
		t.Fatal("expected an error when the incident is not FIX_VERIFIED")
	}
	if inc.PRURL != "" {
		t.Errorf("PRURL should not be set for an unverified incident, got %q", inc.PRURL)
	}
	if inc.PRNumber != 0 {
		t.Errorf("PRNumber should not be set for an unverified incident, got %d", inc.PRNumber)
	}
	if inc.Status != domain.IncidentFixProposed {
		t.Errorf("status = %q, want unchanged FIX_PROPOSED", inc.Status)
	}
}

// TestEngineFixAndVerifyCreatesPR drives the PR step in isolation: a verified
// incident plus a fake provider produces a PR whose number/URL are stamped on
// the incident and whose status transitions to PR_CREATED.
func TestEngineFixAndVerifyCreatesPR(t *testing.T) {
	root := fixtureRoot(t)
	eng, err := NewEngine(root, runtime.NewStore(), memory.NewMemoryStore(t.TempDir()), governance.NewFirewall())
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	const wantURL = "https://example.com/org/repo/pull/42"
	eng.WithPRProvider(fakePRProvider{num: 42, url: wantURL})

	inc := &domain.Incident{
		ID:              "inc-verified",
		Title:           "payments latency",
		AffectedService: "checkout",
		Status:          domain.IncidentFixVerified,
		Verification:    "build passed",
		FixDiff:         "-a\n+b",
	}

	res, err := eng.CreateFixPR(inc, "fix/inc-verified")
	if err != nil {
		t.Fatalf("CreateFixPR: %v", err)
	}
	if res.Number != 42 {
		t.Errorf("result number = %d, want 42", res.Number)
	}
	if res.URL != wantURL {
		t.Errorf("result url = %q, want %q", res.URL, wantURL)
	}
	if inc.PRNumber != 42 {
		t.Errorf("incident PRNumber = %d, want 42", inc.PRNumber)
	}
	if inc.PRURL != wantURL {
		t.Errorf("incident PRURL = %q, want %q", inc.PRURL, wantURL)
	}
	if inc.PRBody == "" {
		t.Error("expected a rendered PR body on the incident")
	}
	if inc.Status != domain.IncidentPRCreated {
		t.Errorf("status = %q, want PR_CREATED", inc.Status)
	}
}

// TestFixAndVerifyCreatesPR proves the full risk → sandbox → verify → PR flow
// completes in one call: a candidate fix applied in the sandbox is verified and
// then turned into a PR via FixAndPR.
func TestFixAndVerifyCreatesPR(t *testing.T) {
	root := fixtureRoot(t)
	mem := memory.NewMemoryStore(t.TempDir())
	fw := governance.NewFirewall()
	// Give the fixer a "fix" permission so the governance risk step does not
	// block the sandboxed candidate fix (same shape as TestEnginePipeline).
	fw.WithAgents(governance.NewAgent("sre", "SRE", "sre", []governance.Permission{{Resource: "prod", Action: "fix"}}))

	const wantURL = "https://example.com/org/repo/pull/7"
	eng, alert, err := InjectRegression(context.Background(), root, "checkout", "pkg/validate.go", mem, fw)
	if err != nil {
		t.Fatalf("InjectRegression: %v", err)
	}
	eng.WithPRProvider(fakePRProvider{num: 7, url: wantURL})

	inc := eng.IngestAlert(alert)
	eng.Correlate(inc)
	eng.RootCause(inc)

	diff, err := eng.FixAndPR(inc, func(workDir string) error {
		p := filepath.Join(workDir, "fix.go")
		return os.WriteFile(p, []byte("package main\n\nfunc Greet(name string) string { return \"hi \" + name }\n"), 0o644)
	}, "fix/"+inc.ID)
	if err != nil {
		t.Fatalf("FixAndPR: %v", err)
	}
	if diff == "" {
		t.Fatal("expected a non-empty diff from the sandboxed fix")
	}
	if inc.PRURL != wantURL {
		t.Errorf("incident PRURL = %q, want %q", inc.PRURL, wantURL)
	}
	if inc.PRNumber != 7 {
		t.Errorf("incident PRNumber = %d, want 7", inc.PRNumber)
	}
	if inc.Status != domain.IncidentPRCreated {
		t.Errorf("status = %q, want PR_CREATED", inc.Status)
	}
}
