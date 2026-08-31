package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/execution"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/memory"
	"github.com/JayveerPrajapati/kern/internal/verification"
	"github.com/JayveerPrajapati/kern/internal/whatif"
)

// safeChangeFixture copies the fixture repository (UserService,
// UserRepository, CacheService, TenantContext, tests, architecture rule) into
// a fresh temp dir so sandbox/worktree patches never touch the repo.
func safeChangeFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	fixture := "testdata/safechange"
	entries, err := os.ReadDir(fixture)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		b, err := os.ReadFile(filepath.Join(fixture, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		if err := os.WriteFile(filepath.Join(root, e.Name()), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, ".kern"), 0o755); err != nil {
		t.Fatal(err)
	}
	// The constitution lives inside the fixture dir (in-repo .kern), so copy it
	// explicitly from the fixture's .kern subdirectory.
	if _, err := os.Stat(filepath.Join(fixture, ".kern", "constitution.yaml")); err == nil {
		b, err := os.ReadFile(filepath.Join(fixture, ".kern", "constitution.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, ".kern", "constitution.yaml"), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// runSafeChangeSlice drives the full workflow once against the
// fixture: intent → Task → context → memory → evidence → impact → risk →
// policy → plan → approval → agent → sandbox → code → verification →
// artifacts → PR → audit. It returns the produced artifact kinds.
func runSafeChangeSlice(t *testing.T, root, intent string, iter int) map[string]bool {
	t.Helper()
	p, err := New(root)
	if err != nil {
		t.Fatalf("iter %d New: %v", iter, err)
	}
	ts := NewTaskService(p, nil).WithAgentID("test")
	label := "iter " + strconv.Itoa(iter)

	// 1 — Task created.
	task, err := ts.Create(intent)
	if err != nil {
		t.Fatalf("%s Create: %v", label, err)
	}
	if task.State != domain.TaskCreated {
		t.Fatalf("%s: state = %s, want CREATED", label, task.State)
	}

	// 2 — Context (analyze, without completing to keep the lifecycle alive).
	task, _, err = ts.analyzeTaskOpts(task, intent, false)
	if err != nil {
		t.Fatalf("%s analyze: %v", label, err)
	}
	if task.ContextPacket == nil {
		t.Fatal(label + ": ContextPacket not attached")
	}
	if len(task.Evidence) == 0 {
		t.Fatal(label + ": evidence claims not attached")
	}

	// 3 — Memory (recall informed by the intent).
	_, _ = p.Memory().Add(domain.Memory{Type: domain.MemoryLesson, Content: "tenant-aware caching pattern for " + intent, Scope: "test"})
	if _, err := p.Memory().Recall(memory.Query{Text: "tenant caching"}); err != nil {
		t.Fatalf("%s memory recall: %v", label, err)
	}

	// 4 — Impact (what-if simulation) → impact_report + risk_report artifacts.
	imp, _, err := p.WhatIf(whatif.RemoveSymbol, intent, "")
	if err != nil {
		t.Fatalf("%s whatif: %v", label, err)
	}
	task.ImpactReport = &imp
	ts.recordArtifact(domain.ArtifactImpactReport, task.ID, "whatif-engine",
		fmt.Sprintf("impact: %d affected, risk=%s", len(imp.Affected), imp.Risk),
		ts.lastArtifactID(task.ID, domain.ArtifactContextPacket), "whatif:simulate")
	ts.recordArtifact(domain.ArtifactRiskReport, task.ID, "whatif-engine",
		"risk="+imp.Risk,
		ts.lastArtifactID(task.ID, domain.ArtifactImpactReport), "whatif:risk")

	// 5 — Risk.
	_, riskText, err := p.Risk(intent)
	if err != nil {
		t.Fatalf("%s risk: %v", label, err)
	}
	_ = riskText

	// 6 — Plan (transition PLANNING, assemble, validate against the fixture's
	// architecture rule — constitution gate; the happy path must pass).
	if err := task.Transition(domain.TaskPlanning); err != nil {
		t.Fatalf("%s transition PLANNING: %v", label, err)
	}
	plan := ts.assemblePlan(intent, *task.ContextPacket)
	task.Plan = &plan
	ts.recordArtifact(domain.ArtifactPlan, task.ID, "plan-engine",
		"plan: "+intent, ts.lastArtifactID(task.ID, domain.ArtifactContextPacket), "plan:assemble")
	constitution, err := governance.LoadConstitution(root)
	if err != nil {
		t.Fatalf("%s LoadConstitution: %v", label, err)
	}
	validation := governance.ValidatePlan(plan, constitution)
	if !validation.Passed {
		t.Fatalf("%s: plan blocked by fixture architecture rule: %+v", label, validation.Violations)
	}

	// 7 — Policy (governance firewall evaluated and gated, no bypass).
	if err := governance.CheckExec(); err != nil {
		t.Fatalf("%s: governance exec gate denied the slice (KERN_ALLOW_EXEC must be set): %v", label, err)
	}

	// 8-9 — Approval requested and granted (human-in-the-loop, Invariant #2).
	if err := task.Transition(domain.TaskWaitingApproval); err != nil {
		t.Fatalf("%s transition WAITING_FOR_APPROVAL: %v", label, err)
	}
	aw := governance.NewApprovalWorkflow()
	approval := aw.Request(task.ID, "test", "execute change")
	if approval.ID == "" {
		t.Fatal(label + ": approval ID empty")
	}
	if _, err := aw.Approve(approval.ID, "test-approver"); err != nil {
		t.Fatalf("%s approve: %v", label, err)
	}
	if err := task.Transition(domain.TaskApproved); err != nil {
		t.Fatalf("%s transition APPROVED: %v", label, err)
	}

	// 10 — Sandbox execution (worktree patch → code + diff artifacts).
	if err := task.Transition(domain.TaskExecuting); err != nil {
		t.Fatalf("%s transition EXECUTING: %v", label, err)
	}
	wt, err := execution.NewWorktree(root)
	if err != nil {
		t.Fatalf("%s NewWorktree: %v", label, err)
	}
	defer func() { _ = wt.Cleanup() }()
	patch := "diff --git a/tenant_cache.go b/tenant_cache.go\nnew file mode 100644\n--- /dev/null\n+++ b/tenant_cache.go\n@@ -0,0 +1,7 @@\n+// tenant_cache.go adds tenant-aware caching for UserService (vertical slice).\n+package main\n+\n+// warmCache primes the tenant-scoped cache for a user.\n+func (s *UserService) warmCache(ctx TenantContext, u User) {\n+\ts.cache.Put(ctx, u)\n+}\n"
	if err := wt.Apply(patch); err != nil {
		t.Fatalf("%s Apply: %v", label, err)
	}
	diffText, err := wt.Diff()
	if err != nil {
		t.Fatalf("%s Diff: %v", label, err)
	}
	if diffText == "" {
		t.Error(label + ": worktree diff empty after patch")
	}
	ts.recordArtifact(domain.ArtifactCodePatch, task.ID, "sandbox-engine",
		"code patch for "+intent, ts.lastArtifactID(task.ID, domain.ArtifactPlan), "exec:apply")
	ts.recordArtifact(domain.ArtifactDiff, task.ID, "execution-engine",
		"diff for "+intent, ts.lastArtifactID(task.ID, domain.ArtifactCodePatch), "exec:diff")

	// 11-14 — Verification (VERIFYING → READY_FOR_PR) + verification_report.
	if err := task.Transition(domain.TaskVerifying); err != nil {
		t.Fatalf("%s transition VERIFYING: %v", label, err)
	}
	res := verification.NewEngine(wt.Dir()).Verify([]string{"build"})
	task.Verification = &res
	ts.recordArtifact(domain.ArtifactVerificationReport, task.ID, "verification-engine",
		"verification verdict="+string(res.Verdict),
		ts.lastArtifactID(task.ID, domain.ArtifactCodePatch), "verification:engine")
	if err := task.Transition(domain.TaskReadyForPR); err != nil {
		t.Fatalf("%s transition READY_FOR_PR: %v", label, err)
	}

	// 15 — PR.
	prTask, prBody, err := ts.CreatePR(task.ID, "feature-tenant-cache")
	if err != nil {
		t.Fatalf("%s CreatePR: %v", label, err)
	}
	if prTask.State != domain.TaskPRCreated || prBody == "" {
		t.Fatalf("%s: PR state=%s body empty=%v", label, prTask.State, prBody == "")
	}

	// 16-17 — Deploy + observe (completes the lifecycle).
	if _, err := ts.Deploy(task.ID, "v0.1.0-slice"); err != nil {
		t.Fatalf("%s Deploy: %v", label, err)
	}
	obsTask, err := ts.Observe(task.ID)
	if err != nil {
		t.Fatalf("%s Observe: %v", label, err)
	}
	if obsTask.State != domain.TaskCompleted {
		t.Fatalf("%s: state = %s, want COMPLETED", label, obsTask.State)
	}

	// 18-19 — Learning + audit finalize (the traceable audit artifact).
	ts.recordAuditArtifact(task.ID, intent)

	// 20 — Audit: verify the full traceable artifact chain.
	arts, err := ts.Artifacts().GetByTask(task.ID)
	if err != nil {
		t.Fatalf("%s GetByTask: %v", label, err)
	}
	kindSet := map[string]bool{}
	seen := map[string]bool{}
	for _, a := range arts {
		kindSet[string(a.Kind)] = true
		seen[a.ID] = true
		if a.ParentArtifactID != "" && !seen[a.ParentArtifactID] {
			t.Errorf("%s: artifact %s parent %s not recorded before it", label, a.ID, a.ParentArtifactID)
		}
	}
	return kindSet
}

// TestSafeChangeVerticalSlice is the flagship: the ENTIRE workflow
// (kern_run intent → Task → context → memory → evidence → impact → risk →
// policy → plan → approval → agent → sandbox → code → verification → artifacts
// → PR → audit) must pass REPEATEDLY against the fixture repository with NO
// governance bypass ( exit gate).
func TestSafeChangeVerticalSlice(t *testing.T) {
	if testing.Short() {
		t.Skip("slow e2e (>30s); skipped with -short")
	}
	// Governance gate: the slice is exercised with execution allowed so policy
	// evaluation is a green path; the gate itself is asserted at step 7.
	t.Setenv("KERN_ALLOW_EXEC", "1")

	const intent = "Add tenant-aware caching to UserService"
	required := []string{
		"context_packet", "analysis_report", "impact_report", "risk_report",
		"plan", "code_patch", "verification_report", "diff", "pull_request", "audit",
	}

	// The exit gate: the workflow must pass REPEATEDLY — two full runs against
	// two fresh fixture copies.
	for iter := 0; iter < 2; iter++ {
		root := safeChangeFixture(t)
		kinds := runSafeChangeSlice(t, root, intent, iter)
		for _, k := range required {
			if !kinds[k] {
				t.Errorf("iter %d (P10.4): %s artifact not produced in the full workflow (got %v)", iter, k, keysOf(kinds))
			}
		}
	}
}
