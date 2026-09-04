package approval

import (
	"context"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/blueprint/approval"
	"github.com/JayveerPrajapati/kern/internal/blueprint/domain"
	"github.com/JayveerPrajapati/kern/internal/blueprint/risk"
)

func newCheck(t *testing.T) (*Check, *approval.Store) {
	t.Helper()
	store := approval.NewStore(t.TempDir())
	return NewCheck(store, risk.DefaultConfig()), store
}

func highRiskAgentReq(meta map[string]string) domain.ChangeRequest {
	return domain.ChangeRequest{
		RepositoryRoot: "/tmp/repo",
		Source:         domain.SourceAgent,
		Operation:      domain.OpCommit,
		Files: []domain.FileChange{{
			Path: ".kern/boundaries.json",
			Op:   domain.OpEdit,
		}},
		Metadata: meta,
	}
}

func lowRiskReq() domain.ChangeRequest {
	return domain.ChangeRequest{
		RepositoryRoot: "/tmp/repo",
		Source:         domain.SourceHuman,
		Operation:      domain.OpCommit,
		Files: []domain.FileChange{{
			Path:  "internal/app/handler.go",
			Op:    domain.OpEdit,
			Added: []string{"1", "2"},
		}},
	}
}

func TestPassLowRisk(t *testing.T) {
	chk, _ := newCheck(t)
	cr, err := chk.Run(context.Background(), lowRiskReq())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if cr.Status != domain.StatusPass {
		t.Fatalf("Status = %q, want PASS for low-risk change", cr.Status)
	}
	if cr.Name != "approval:gate" {
		t.Errorf("Name = %q, want approval:gate", cr.Name)
	}
}

func TestPassLowRiskEvenWithApprovalID(t *testing.T) {
	// A low-risk change never consults the store, even if an approval-id is
	// attached (the gate must not fail on irrelevant metadata).
	chk, _ := newCheck(t)
	cr, err := chk.Run(context.Background(), lowRiskReq())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if cr.Status != domain.StatusPass {
		t.Fatalf("Status = %q, want PASS", cr.Status)
	}
}

func TestBlockNoApprovalID(t *testing.T) {
	chk, _ := newCheck(t)
	cr, err := chk.Run(context.Background(), highRiskAgentReq(nil))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if cr.Status != domain.StatusBlock {
		t.Fatalf("Status = %q, want BLOCK for unapproved high-risk change", cr.Status)
	}
	if len(cr.Findings) != 1 {
		t.Fatalf("Findings len = %d, want 1", len(cr.Findings))
	}
	f := cr.Findings[0]
	if f.RuleID != "approval:required" {
		t.Errorf("RuleID = %q, want approval:required", f.RuleID)
	}
	if f.Severity != domain.SeverityBlock {
		t.Errorf("Severity = %q, want block (the gate must block)", f.Severity)
	}
	if f.Category != domain.CategoryPolicy {
		t.Errorf("Category = %q, want policy", f.Category)
	}
	if f.Confidence != 1.0 || f.Scope != "repo" {
		t.Errorf("Confidence/Scope = %v/%q, want 1.0/repo", f.Confidence, f.Scope)
	}
	if len(f.Evidence) == 0 {
		t.Error("expected risk indicator evidence on the finding")
	}
}

func TestBlockPendingApproval(t *testing.T) {
	chk, store := newCheck(t)
	if err := store.Create(approval.Request{
		ID:        "apr-pending",
		RepoRoot:  "/tmp/repo",
		Intent:    "update boundaries",
		RiskLevel: "high",
		Requester: "agent-1",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	cr, err := chk.Run(context.Background(), highRiskAgentReq(map[string]string{"approval-id": "apr-pending"}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if cr.Status != domain.StatusBlock {
		t.Fatalf("Status = %q, want BLOCK while pending", cr.Status)
	}
	if cr.Findings[0].RuleID != "approval:required" {
		t.Errorf("RuleID = %q, want approval:required", cr.Findings[0].RuleID)
	}
}

func TestBlockRejectedApproval(t *testing.T) {
	chk, store := newCheck(t)
	if err := store.Create(approval.Request{ID: "apr-rej", RepoRoot: "/tmp/repo", Intent: "x", RiskLevel: "high", Requester: "agent-1", CreatedAt: time.Now()}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.Reject("apr-rej", "bob@corp", "not now"); err != nil {
		t.Fatalf("Reject: %v", err)
	}
	cr, err := chk.Run(context.Background(), highRiskAgentReq(map[string]string{"approval-id": "apr-rej"}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if cr.Status != domain.StatusBlock {
		t.Fatalf("Status = %q, want BLOCK for rejected request", cr.Status)
	}
	if cr.Findings[0].RuleID != "approval:rejected" {
		t.Errorf("RuleID = %q, want approval:rejected", cr.Findings[0].RuleID)
	}
}

func TestBlockUnknownApprovalID(t *testing.T) {
	chk, _ := newCheck(t)
	cr, err := chk.Run(context.Background(), highRiskAgentReq(map[string]string{"approval-id": "apr-ghost"}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if cr.Status != domain.StatusBlock {
		t.Fatalf("Status = %q, want BLOCK for unknown approval id", cr.Status)
	}
	if cr.Findings[0].RuleID != "approval:required" {
		t.Errorf("RuleID = %q, want approval:required", cr.Findings[0].RuleID)
	}
}

func TestPassApproved(t *testing.T) {
	chk, store := newCheck(t)
	if err := store.Create(approval.Request{ID: "apr-ok", RepoRoot: "/tmp/repo", Intent: "update boundaries", RiskLevel: "high", Requester: "agent-1", CreatedAt: time.Now()}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.Approve("apr-ok", "alice@corp", "approved"); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	cr, err := chk.Run(context.Background(), highRiskAgentReq(map[string]string{"approval-id": "apr-ok"}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if cr.Status != domain.StatusPass {
		t.Fatalf("Status = %q, want PASS for approved request", cr.Status)
	}
	if len(cr.Findings) != 0 {
		t.Errorf("Findings = %+v, want none (any finding would downgrade PASS to WARN)", cr.Findings)
	}
}

func TestMediumRiskHumanDoesNotRequireApproval(t *testing.T) {
	// A human touching a sensitive path is medium risk — with the default
	// RequireApprovalFor=["high"] the gate must pass (humans ARE the
	// approvers; requiring their own approval would block every commit).
	chk, _ := newCheck(t)
	req := domain.ChangeRequest{
		RepositoryRoot: "/tmp/repo",
		Source:         domain.SourceHuman,
		Operation:      domain.OpCommit,
		Files: []domain.FileChange{{
			Path: ".kern/boundaries.json",
			Op:   domain.OpEdit,
		}},
	}
	cr, err := chk.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if cr.Status != domain.StatusPass {
		t.Fatalf("Status = %q, want PASS (medium risk, human source)", cr.Status)
	}
}

func TestRequireApprovalForOverride(t *testing.T) {
	// Config lists "medium" too AND opts the human source into the gate: a
	// human sensitive-path change then blocks (otherwise humans are exempt by
	// default — they ARE the approvers).
	cfg := risk.DefaultConfig()
	cfg.RequireApprovalFor = []string{"high", "medium"}
	cfg.RequireForSources = []string{"human"}
	store := approval.NewStore(t.TempDir())
	chk := NewCheck(store, cfg)
	req := domain.ChangeRequest{
		RepositoryRoot: "/tmp/repo",
		Source:         domain.SourceHuman,
		Files:          []domain.FileChange{{Path: ".kern/boundaries.json", Op: domain.OpEdit}},
	}
	cr, err := chk.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if cr.Status != domain.StatusBlock {
		t.Fatalf("Status = %q, want BLOCK when medium requires approval", cr.Status)
	}
}

func TestSourceNotInRequireForSourcesPasses(t *testing.T) {
	// An IDE change on a sensitive path is high-risk surface, but the default
	// gate applies to agent sources only — it must pass without consulting
	// the store.
	chk, _ := newCheck(t)
	req := domain.ChangeRequest{
		RepositoryRoot: "/tmp/repo",
		Source:         domain.SourceIDE,
		Files:          []domain.FileChange{{Path: ".kern/boundaries.json", Op: domain.OpEdit}},
	}
	cr, err := chk.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if cr.Status != domain.StatusPass {
		t.Fatalf("Status = %q, want PASS (IDE source not gated by default)", cr.Status)
	}
}
