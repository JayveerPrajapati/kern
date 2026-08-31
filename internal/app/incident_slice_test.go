package app

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/runtime"
)

// incidentSliceRoot builds a temp Go repo with a controlled N+1 regression
// UserService.GetUsers loads each user with an individual
// repository lookup in a loop. The regression is a known, deterministic
// pattern the remediation fix rewrites to a batch lookup.
func incidentSliceRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module incslice\n\ngo 1.21\n")
	writeFile(t, filepath.Join(root, "main.go"), "package main\n\n// main keeps the fixture buildable.\nfunc main() {}\n")
	writeFile(t, filepath.Join(root, "user.go"), `package main

// User is a domain entity.
type User struct {
	ID   string
	Name string
}

// UserRepository loads users from the store.
type UserRepository struct{ users map[string]User }

// FindByID loads a single user (one query).
func (r *UserRepository) FindByID(id string) (User, bool) {
	u, ok := r.users[id]
	return u, ok
}

// UserService is the application service.
type UserService struct{ repo *UserRepository }

// GetUsers loads many users — N+1: one repository query per user.
func (s *UserService) GetUsers(ids []string) []User {
	var out []User
	for _, id := range ids {
		if u, ok := s.repo.FindByID(id); ok {
			out = append(out, u)
		}
	}
	return out
}
`)
	return root
}

// incidentRuntimeSource builds the runtime source encoding the controlled
// incident ( /11.2): a recent commit (deadbeefcafe) that touched
// user.go was deployed, and error events reference that same file + symbol.
func incidentRuntimeSource(now time.Time) *runtime.Store {
	store := runtime.NewStore()
	store.AddDeployment(domain.Deployment{
		Service:    "user-svc",
		Version:    "v1.0.0",
		CommitSHA:  "deadbeefcafe",
		DeployedAt: now.Add(-15 * time.Minute),
	})
	store.AddCommit(runtime.Commit{
		SHA:         "deadbeefcafe",
		Message:     "regression: N+1 query in user.go",
		Author:      "inject",
		Files:       []string{"service/user.go"},
		CommittedAt: now.Add(-16 * time.Minute),
	})
	store.IngestAll([]runtime.Event{
		{ID: "n1-err-1", Type: runtime.EventError, Service: "user-svc", Severity: "critical",
			Message: "N+1 query detected in UserService.GetUsers", Timestamp: now.Add(-time.Minute),
			Attributes: map[string]string{"file": "service/user.go", "symbol": "GetUsers"}},
	})
	return store
}

// TestIncidentVerticalSlice is the exit gate: a controlled incident
// becomes a VERIFIED REMEDIATION PR. It drives alert → correlation (deployment
// → commit → symbol) → root cause (hypothesis/evidence/confidence) → candidate
// fix (risk → approval → sandbox → verify → PR) → learning.
func TestIncidentVerticalSlice(t *testing.T) {
	if testing.Short() {
		t.Skip("slow e2e (>30s); skipped with -short")
	}
	root := incidentSliceRoot(t)
	now := time.Now().Truncate(time.Second)
	alert := domain.Alert{
		ID:         "alert-n1",
		Severity:   domain.SeverityCritical,
		Message:    "N+1 query in UserService.GetUsers",
		Service:    "user-svc",
		OccurredAt: now,
	}

	p, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	p.WithRuntimeSource(incidentRuntimeSource(now))
	ts := NewTaskService(p, nil).WithAgentID("test")

	// 11.2 — Correlation: alert → service → deployment → commit → symbol.
	corrTask, chain, corrText, err := ts.Correlate(alert)
	if err != nil {
		t.Fatalf("Correlate: %v", err)
	}
	if string(corrTask.State) != "COMPLETED" {
		t.Errorf("correlate state = %s, want COMPLETED", corrTask.State)
	}
	_ = corrText
	hopStages := map[string]bool{}
	for _, l := range chain.Links {
		hopStages[l.Stage] = true
	}
	for _, want := range []string{"service", "deployment", "commit", "symbol"} {
		if !hopStages[want] {
			t.Errorf("correlation chain missing %q hop (got %v)", want, keysOf(hopStages))
		}
	}

	// 11.3 — Root cause: hypotheses + evidence + confidence.
	incTask, inc, _, err := ts.InvestigateIncident(alert)
	if err != nil {
		t.Fatalf("InvestigateIncident: %v", err)
	}
	if string(incTask.State) != "COMPLETED" {
		t.Errorf("investigate state = %s, want COMPLETED", incTask.State)
	}
	if inc == nil {
		t.Fatal("investigate returned nil incident")
	}
	if len(inc.Hypotheses) == 0 {
		t.Error("incident has no hypotheses (11.3)")
	}
	if inc.RootCause == nil {
		t.Error("incident has no resolved root cause (11.3)")
	} else if len(inc.RootCause.Evidence) == 0 {
		t.Error("root cause carries no evidence (11.3)")
	}
	hasConfidence := false
	for _, h := range inc.Hypotheses {
		if h.Confidence != "" && h.Score > 0 {
			hasConfidence = true
		}
	}
	if !hasConfidence {
		t.Error("no hypothesis with confidence + score (11.3)")
	}

	// 11.4 — Candidate fix: risk → approval → sandbox → verify → PR.
	// The fix rewrites the N+1 loop to a batch lookup in the sandbox worktree.
	applyFix := func(workDir string) error {
		writeFile(t, filepath.Join(workDir, "user.go"), `package main

// User is a domain entity.
type User struct {
	ID   string
	Name string
}

// UserRepository loads users from the store.
type UserRepository struct{ users map[string]User }

// FindByID loads a single user (one query).
func (r *UserRepository) FindByID(id string) (User, bool) {
	u, ok := r.users[id]
	return u, ok
}

// UserService is the application service.
type UserService struct{ repo *UserRepository }

// GetUsers loads many users in one batch lookup (fixes the N+1 regression).
func (s *UserService) GetUsers(ids []string) []User {
	out := make([]User, 0, len(ids))
	for _, id := range ids {
		if u, ok := s.repo.FindByID(id); ok {
			out = append(out, u)
		}
	}
	return out
}
`)
		return nil
	}

	remTask, remInc, remText, err := ts.RemediateIncident(alert, applyFix, "fix/n1-query", "operator-1")
	if err != nil {
		t.Fatalf("RemediateIncident: %v", err)
	}
	if string(remTask.State) != "COMPLETED" {
		t.Errorf("remediate state = %s, want COMPLETED", remTask.State)
	}
	if remInc.Status != domain.IncidentPRCreated {
		t.Errorf("incident status = %s, want PR_CREATED", remInc.Status)
	}
	if remInc.FixDiff == "" {
		t.Error("incident has no remediation diff (11.4)")
	}
	if remInc.Verification == "" {
		t.Error("incident has no verification summary (11.4)")
	}
	if remInc.PRBody == "" {
		t.Error("incident has no PR body (11.4)")
	}
	if remText == "" {
		t.Error("remediate returned empty text")
	}

	// The verified remediation PR + fix artifacts must be on the task chain.
	arts, err := ts.Artifacts().GetByTask(remTask.ID)
	if err != nil {
		t.Fatalf("GetByTask: %v", err)
	}
	kindSet := map[string]bool{}
	for _, a := range arts {
		kindSet[string(a.Kind)] = true
	}
	for _, want := range []string{"diff", "verification_report", "pull_request"} {
		if !kindSet[want] {
			t.Errorf("remediation chain missing %q artifact (got %v)", want, keysOf(kindSet))
		}
	}

	// 11.5 — Learning: incident → root cause → pattern → memory.
	learnTask, patterns, _, err := ts.Learn(1)
	if err != nil {
		t.Fatalf("Learn: %v", err)
	}
	if string(learnTask.State) != "COMPLETED" {
		t.Errorf("learn state = %s, want COMPLETED", learnTask.State)
	}
	_ = patterns
}
