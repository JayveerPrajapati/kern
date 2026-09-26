package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/agent"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/prprovider"
	"github.com/JayveerPrajapati/kern/internal/verification"
)

// TestAutoPRProviderNoopWithoutToken pins the fallback: with no
// KERN_GITHUB_TOKEN the factory must return the no-op provider (the default
// that never touches the network), not fail.
func TestAutoPRProviderNoopWithoutToken(t *testing.T) {
	t.Setenv("KERN_GITHUB_TOKEN", "")
	if _, ok := AutoPRProvider().(prprovider.NoopProvider); !ok {
		t.Fatalf("AutoPRProvider() without token = %T, want prprovider.NoopProvider", AutoPRProvider())
	}
}

// TestAutoPRProviderGitHubWithToken pins the upgrade path: once a token is
// present the factory returns the real GitHub provider.
func TestAutoPRProviderGitHubWithToken(t *testing.T) {
	t.Setenv("KERN_GITHUB_TOKEN", "ghp_test_token")
	p := AutoPRProvider()
	if _, ok := p.(*prprovider.GitHubProvider); !ok {
		t.Fatalf("AutoPRProvider() with token = %T, want *prprovider.GitHubProvider", p)
	}
}

// recordingProvider is a test double for prprovider.Provider that records
// CreatePR and CommentPR calls so the comment wiring is observable.
type recordingProvider struct {
	createCalls  int
	commentCalls []prprovider.CommentRequest
	createErr    error
	commentErr   error
	number       int
}

func (r *recordingProvider) CreatePR(req prprovider.Request) (*prprovider.Result, error) {
	r.createCalls++
	if r.createErr != nil {
		return nil, r.createErr
	}
	return &prprovider.Result{Number: r.number, URL: "https://github.com/o/r/pull/1", State: "open"}, nil
}

func (r *recordingProvider) CommentPR(ctx context.Context, req prprovider.CommentRequest) error {
	r.commentCalls = append(r.commentCalls, req)
	return r.commentErr
}

// findingsVerification returns a verification result carrying the review
// findings the comment wiring should surface.
func findingsVerification() *verification.VerificationResult {
	return &verification.VerificationResult{
		Verdict: verification.VerdictPassWithWarning,
		Summary: "passed with warnings",
		Security: &verification.SecurityResult{
			Findings: []verification.Finding{
				{File: "main.go", Line: 10, Rule: "G101", Severity: "medium", Message: "hardcoded credential"},
				{File: "main.go", Line: 3, Rule: "G404", Severity: "low", Message: "weak random source"},
			},
			OK: false, Count: 2, Medium: 1, Low: 1,
		},
		Architecture: &verification.ArchitectureResult{
			Violations: []string{"internal/app must not import internal/reviewpack"},
			OK:         false,
		},
	}
}

// prReadyService builds a TaskService over a minimal fixture root with the
// given provider and a task parked in READY_FOR_PR (verification attached when
// verify is non-nil). Returns the service and the task.
func prReadyService(t *testing.T, prov prprovider.Provider, verify *verification.VerificationResult) (*TaskService, *agent.Task) {
	t.Helper()
	root := workflowFixtureRoot(t)
	p, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := NewTaskService(p, nil).WithAgentID("test").WithPRProvider(prov)
	task, err := ts.Create("add caching to NewServer")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := task.Transition(domain.TaskVerifying); err != nil {
		t.Fatalf("transition VERIFYING: %v", err)
	}
	if verify != nil {
		task.Verification = verify
	}
	if err := task.Transition(domain.TaskReadyForPR); err != nil {
		t.Fatalf("transition READY_FOR_PR: %v", err)
	}
	return ts, task
}

// TestCreatePRPostsCommentWhenFindingsExist pins the inline hook: a real PR
// (Number > 0) with review findings on the task posts one comment whose body
// carries the findings as bullets.
func TestCreatePRPostsCommentWhenFindingsExist(t *testing.T) {
	prov := &recordingProvider{number: 7}
	ts, task := prReadyService(t, prov, findingsVerification())

	prTask, _, err := ts.CreatePR(task.ID, "feature-branch")
	if err != nil {
		t.Fatalf("CreatePR: %v", err)
	}
	if prTask.PRNumber != 7 {
		t.Errorf("PRNumber = %d, want 7", prTask.PRNumber)
	}
	if len(prov.commentCalls) != 1 {
		t.Fatalf("CommentPR calls = %d, want 1", len(prov.commentCalls))
	}
	call := prov.commentCalls[0]
	if call.Number != 7 {
		t.Errorf("comment PR number = %d, want 7", call.Number)
	}
	if !strings.Contains(call.Body, "## Review findings") {
		t.Errorf("comment missing header:\n%s", call.Body)
	}
	if !strings.Contains(call.Body, "- [security/medium] main.go:10 hardcoded credential") {
		t.Errorf("comment missing security finding:\n%s", call.Body)
	}
	if !strings.Contains(call.Body, "- [security/low] main.go:3 weak random source") {
		t.Errorf("comment missing second security finding:\n%s", call.Body)
	}
	if !strings.Contains(call.Body, "- [architecture] internal/app must not import internal/reviewpack") {
		t.Errorf("comment missing architecture finding:\n%s", call.Body)
	}
}

// TestCreatePRCommentFailureDoesNotFailPR pins the best-effort contract: a
// comment error is logged and swallowed — PR creation still succeeds and the
// task reaches PR_CREATED.
func TestCreatePRCommentFailureDoesNotFailPR(t *testing.T) {
	prov := &recordingProvider{number: 7, commentErr: errors.New("network down")}
	ts, task := prReadyService(t, prov, findingsVerification())

	prTask, _, err := ts.CreatePR(task.ID, "feature-branch")
	if err != nil {
		t.Fatalf("CreatePR must not fail when the comment fails: %v", err)
	}
	if prTask.State != domain.TaskPRCreated {
		t.Errorf("state = %s, want PR_CREATED", prTask.State)
	}
	if len(prov.commentCalls) != 1 {
		t.Errorf("CommentPR calls = %d, want 1 (attempted despite error)", len(prov.commentCalls))
	}
}

// TestCreatePRNoFindingsNoComment pins the skip rule: no verification result,
// or a verification result with zero findings, posts no comment.
func TestCreatePRNoFindingsNoComment(t *testing.T) {
	// No verification result at all.
	prov := &recordingProvider{number: 7}
	ts, task := prReadyService(t, prov, nil)
	if _, _, err := ts.CreatePR(task.ID, "feature-branch"); err != nil {
		t.Fatalf("CreatePR (no verification): %v", err)
	}
	if len(prov.commentCalls) != 0 {
		t.Errorf("CommentPR calls = %d, want 0 (no findings)", len(prov.commentCalls))
	}

	// Verification result present but no findings (clean PASS).
	prov2 := &recordingProvider{number: 8}
	ts2, task2 := prReadyService(t, prov2, &verification.VerificationResult{
		Verdict: verification.VerdictPass,
		Summary: "all checks passed",
	})
	if _, _, err := ts2.CreatePR(task2.ID, "feature-branch"); err != nil {
		t.Fatalf("CreatePR (clean pass): %v", err)
	}
	if len(prov2.commentCalls) != 0 {
		t.Errorf("CommentPR calls = %d, want 0 (no findings)", len(prov2.commentCalls))
	}
}

// TestCreatePRNoopProviderIsNoop pins the default: with the NoopProvider the
// PR number is 0, so no comment path runs and PR creation still succeeds.
func TestCreatePRNoopProviderIsNoop(t *testing.T) {
	// Default provider (NoopProvider) — no WithPRProvider wiring.
	root := workflowFixtureRoot(t)
	p, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := NewTaskService(p, nil).WithAgentID("test")
	task, err := ts.Create("add caching to NewServer")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := task.Transition(domain.TaskVerifying); err != nil {
		t.Fatalf("transition VERIFYING: %v", err)
	}
	task.Verification = findingsVerification()
	if err := task.Transition(domain.TaskReadyForPR); err != nil {
		t.Fatalf("transition READY_FOR_PR: %v", err)
	}
	prTask, _, err := ts.CreatePR(task.ID, "feature-branch")
	if err != nil {
		t.Fatalf("CreatePR (NoopProvider): %v", err)
	}
	if prTask.State != domain.TaskPRCreated {
		t.Errorf("state = %s, want PR_CREATED", prTask.State)
	}
	if prTask.PRNumber != 0 {
		t.Errorf("PRNumber = %d, want 0 (noop)", prTask.PRNumber)
	}
}

// TestCreatePRCommentCaps pins the comment-body limits: at most 30 findings,
// each bullet clipped to 300 chars, with a trailing "more" note when capped.
func TestCreatePRCommentCaps(t *testing.T) {
	prov := &recordingProvider{number: 7}
	verify := &verification.VerificationResult{
		Verdict: verification.VerdictPassWithWarning,
		Security: &verification.SecurityResult{
			OK: false,
		},
		StaticAnalysis: &verification.StaticAnalysisResult{
			Tool: "golangci-lint",
			OK:   false,
		},
	}
	// 35 static findings + 1 oversized security finding.
	for i := 0; i < 35; i++ {
		verify.StaticAnalysis.Findings = append(verify.StaticAnalysis.Findings, "issue #"+strings.Repeat("0", 20))
	}
	verify.Security.Findings = []verification.Finding{
		{File: "main.go", Rule: "G101", Severity: "high", Message: strings.Repeat("y", 500)},
	}

	ts, task := prReadyService(t, prov, verify)
	if _, _, err := ts.CreatePR(task.ID, "feature-branch"); err != nil {
		t.Fatalf("CreatePR: %v", err)
	}
	if len(prov.commentCalls) != 1 {
		t.Fatalf("CommentPR calls = %d, want 1", len(prov.commentCalls))
	}
	body := prov.commentCalls[0].Body
	// Count real bullets only (the "… and N more finding(s)" note also starts
	// with "- " but must not count toward the cap).
	bullets := 0
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "- ") && !strings.HasPrefix(line, "- _") {
			bullets++
		}
	}
	if bullets != maxPRCommentFindings {
		t.Errorf("bullet count = %d, want %d", bullets, maxPRCommentFindings)
	}
	if !strings.Contains(body, "and 6 more finding(s)") {
		t.Errorf("missing capped-count note:\n%s", body)
	}
	// The 500-char finding must be clipped to 300.
	for _, line := range strings.Split(body, "\n") {
		if len(line) > maxPRFindingChars+len("- [security/high] main.go G101 ") {
			t.Errorf("bullet exceeds clip budget: %d chars: %.80s…", len(line), line)
		}
	}
}
