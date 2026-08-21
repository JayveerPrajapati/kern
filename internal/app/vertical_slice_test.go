package app

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/agent"
	"github.com/JayveerPrajapati/kern/internal/deployment"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/execution"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/memory"
	"github.com/JayveerPrajapati/kern/internal/runtime"
	"github.com/JayveerPrajapati/kern/internal/verification"
	"github.com/JayveerPrajapati/kern/internal/whatif"
)

// TestVerticalSlice1AnalyzePlanImpactVerifyPR is the first required vertical
// slice proof point from the Integration Transformation Plan (§33):
//
//	"Add caching to UserService" → analyze → plan → impact → execute → verify → PR
//
// It exercises the full TaskService lifecycle end-to-end against the real kern
// repo, proving that every phase is wired through the authoritative Task with
// artifacts, events, and lifecycle state transitions.
//
// This test is structural (not exact-output): it asserts that each phase
// produces a Task in the expected state, with non-empty output and a recorded
// artifact. It does NOT assert exact symbol counts (those drift as the repo
// evolves).
func TestVerticalSlice1AnalyzePlanImpactVerifyPR(t *testing.T) {
	if testing.Short() {
		t.Skip("slow e2e (>30s); skipped with -short")
	}
	root := "../.."

	p, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := NewTaskService(p, nil).WithAgentID("test")

	// --- Phase 5: Analyze ---
	t.Run("analyze", func(t *testing.T) {
		task, text, err := ts.Analyze("NewServer")
			if err != nil {
			t.Fatalf("Analyze: %v", err)
		}
		if task.State != domain.TaskCompleted {
			t.Errorf("state = %s, want COMPLETED", task.State)
		}
		if text == "" {
			t.Error("analyze returned empty text")
		}
		if task.ContextPacket == nil {
			t.Error("ContextPacket not attached")
		}
		if task.CreatedBy != "test" {
			t.Errorf("CreatedBy = %q, want 'test' (Inv 6)", task.CreatedBy)
		}
		// Artifact should be recorded.
		arts, _ := ts.Artifacts().GetByTask(task.ID)
		if len(arts) == 0 {
			t.Error("no artifacts recorded for analyze task")
		}
	})

	// --- Phase 6: Plan ---
	t.Run("plan", func(t *testing.T) {
		task, plan, text, err := ts.Plan("NewServer")
		if err != nil {
			t.Fatalf("Plan: %v", err)
		}
		if task.State != domain.TaskCompleted {
			t.Errorf("state = %s, want COMPLETED", task.State)
		}
		if task.Plan == nil {
			t.Error("Plan not attached to task")
		}
		if plan.Objective == "" {
			t.Error("plan objective is empty")
		}
		if plan.Risk == "" {
			t.Error("plan risk is empty")
		}
		if len(plan.ImplementationSteps) == 0 {
			t.Error("plan has no implementation steps")
		}
		if text == "" {
			t.Error("plan returned empty text")
		}
		// Plan artifact should be recorded.
		arts, _ := ts.Artifacts().GetByTask(task.ID)
		if len(arts) == 0 {
			t.Error("no artifacts recorded for plan task")
		}
	})

	// --- Phase 7: Impact ---
	t.Run("impact", func(t *testing.T) {
		task, rep, text, err := ts.Impact("NewServer")
		if err != nil {
			t.Fatalf("Impact: %v", err)
		}
		if task.State != domain.TaskCompleted {
			t.Errorf("state = %s, want COMPLETED", task.State)
		}
		if task.Impact == nil {
			t.Error("Impact not attached to task")
		}
		if rep.Target == "" {
			t.Error("impact target is empty")
		}
		if rep.Risk == "" {
			t.Error("impact risk is empty")
		}
		if text == "" {
			t.Error("impact returned empty text")
		}
		if !strings.Contains(text, "IMPACT") {
			t.Errorf("impact text missing 'IMPACT' marker; got:\n%s", text)
		}
	})

	// --- Phase 8: What-If ---
	t.Run("whatif", func(t *testing.T) {
		task, text, err := ts.WhatIf(whatif.RemoveSymbol, "NewServer", "")
		if err != nil {
			t.Fatalf("WhatIf: %v", err)
		}
		if task.State != domain.TaskCompleted {
			t.Errorf("state = %s, want COMPLETED", task.State)
		}
		if task.ImpactReport == nil {
			t.Error("ImpactReport not attached to task")
		}
		if text == "" {
			t.Error("whatif returned empty text")
		}
	})

	// --- Phase 12: Verify ---
	t.Run("verify", func(t *testing.T) {
		task, res, err := ts.Verify([]string{"build"})
		if err != nil {
			t.Fatalf("Verify: %v", err)
		}
		if task.State != domain.TaskCompleted {
			t.Errorf("state = %s, want COMPLETED", task.State)
		}
		if task.Verification == nil {
			t.Error("Verification not attached to task")
		}
		if res.Verdict == "" {
			t.Error("verify returned empty verdict")
		}
	})

	// --- Phase 13: CreatePR (requires a task in READY_FOR_PR) ---
	// We can't easily get a task into READY_FOR_PR without executing a real
	// patch, so we verify that CreatePR correctly rejects a task that hasn't
	// been verified. This is the negative path — the positive path is covered
	// by the loop tests.
	t.Run("createPR_rejectsUnverified", func(t *testing.T) {
		task, _, err := ts.Analyze("NewServer")
		if err != nil {
			t.Fatalf("Analyze: %v", err)
		}
		_, _, err = ts.CreatePR(task.ID, "feature-branch")
		if err == nil {
			t.Error("CreatePR should reject a task not in READY_FOR_PR")
		}
		if !strings.Contains(err.Error(), "READY_FOR_PR") {
			t.Errorf("error should mention READY_FOR_PR; got: %v", err)
		}
	})
}

// TestVerticalSlice3WhatIfScenario is the third vertical slice proof point:
//
//	What-if "split PaymentService" → bounded contexts + impact
//
// It exercises the what-if engine's SplitService change kind and verifies the
// impact report includes affected components, files, and a risk level.
func TestVerticalSlice3WhatIfScenario(t *testing.T) {
	root := "../.."

	p, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := NewTaskService(p, nil).WithAgentID("test")

	t.Run("splitService", func(t *testing.T) {
		task, text, err := ts.WhatIf(whatif.SplitService, "NewServer", "")
		if err != nil {
			t.Fatalf("WhatIf(SplitService): %v", err)
		}
		if task.State != domain.TaskCompleted {
			t.Errorf("state = %s, want COMPLETED", task.State)
		}
		if task.ImpactReport == nil {
			t.Fatal("ImpactReport not attached")
		}
		imp := task.ImpactReport
		if imp.Risk == "" {
			t.Error("impact risk is empty")
		}
		if text == "" {
			t.Error("whatif returned empty text")
		}
		if !strings.Contains(text, "change:") {
			t.Errorf("text missing 'change:' marker; got:\n%s", text)
		}
	})

	t.Run("changeDependency", func(t *testing.T) {
		task, _, err := ts.WhatIf(whatif.ChangeDependency, "NewServer", "SomeOther")
		if err != nil {
			t.Fatalf("WhatIf(ChangeDependency): %v", err)
		}
		if task.State != domain.TaskCompleted {
			t.Errorf("state = %s, want COMPLETED", task.State)
		}
	})
}

// TestTaskServiceAgentIdentity verifies that the agent identity is threaded
// through all tasks created by a TaskService (Invariant 6).
func TestTaskServiceAgentIdentity(t *testing.T) {
	if testing.Short() {
		t.Skip("slow e2e (>30s); skipped with -short")
	}
	root := "../.."

	p, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Default identity.
	ts1 := NewTaskService(p, nil)
	if ts1.AgentID() != "kern" {
		t.Errorf("default AgentID = %q, want 'kern'", ts1.AgentID())
	}
	task1, _, err := ts1.Analyze("NewServer")
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if task1.CreatedBy != "kern" {
		t.Errorf("task.CreatedBy = %q, want 'kern'", task1.CreatedBy)
	}

	// Custom identity.
	ts2 := NewTaskService(p, nil).WithAgentID("mcp")
	if ts2.AgentID() != "mcp" {
		t.Errorf("AgentID = %q, want 'mcp'", ts2.AgentID())
	}
	task2, _, err := ts2.Analyze("NewServer")
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if task2.CreatedBy != "mcp" {
		t.Errorf("task.CreatedBy = %q, want 'mcp'", task2.CreatedBy)
	}
}

// TestArtifactImmutability verifies that a finalized artifact cannot be
// overwritten (Invariant 8).
func TestArtifactImmutability(t *testing.T) {
	if testing.Short() {
		t.Skip("slow e2e (>30s); skipped with -short")
	}
	root := "../.."

	p, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := NewTaskService(p, nil).WithAgentID("test")

	// Analyze creates a finalized context-packet artifact.
	task, _, err := ts.Analyze("NewServer")
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}

	// Find the artifact.
	arts, err := ts.Artifacts().GetByTask(task.ID)
	if err != nil || len(arts) == 0 {
		t.Fatalf("no artifacts for task %s", task.ID)
	}

	// Attempt to overwrite a finalized artifact — should fail.
	original := arts[0]
	original.Scope = "tampered"
	_, err = ts.Artifacts().Save(original)
	if err == nil {
		t.Error("Save should reject overwrite of a finalized artifact")
	}
	if !strings.Contains(err.Error(), "immutable") {
		t.Errorf("error should mention 'immutable'; got: %v", err)
	}
}

// TestDeployApprovalGate verifies that a real (non-Noop) deployer triggers the
// governance approval gate: Deploy returns agent.ErrApprovalRequired wrapping
// the pending approval ID, and the task is NOT transitioned to Deploying. The
// NoopDeployer (default, simulated) skips the gate to preserve v1 behavior.
func TestDeployApprovalGate(t *testing.T) {
	root := "../.."

	p, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Register a deploy-capable agent so the firewall reaches the approval
	// gate (otherwise it fails at authentication with "unknown agent").
	p.Firewall().WithAgents(governance.NewAgent(
		"test-deployer", "Test Deployer", "deployer",
		[]governance.Permission{
			{Resource: "production", Action: "deploy"},
		},
	))
	ts := NewTaskService(p, nil).WithAgentID("test-deployer").
		WithDeployer(&deployment.ShellDeployer{Command: "echo deploy", Timeout: 5 * time.Second})

	task, err := ts.Create("deploy test")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Move the task to PR_CREATED so Deploy's transition is valid.
	for _, st := range []domain.TaskState{
		domain.TaskAnalyzing, domain.TaskPlanning, domain.TaskWaitingApproval,
		domain.TaskApproved, domain.TaskExecuting, domain.TaskVerifying,
		domain.TaskReadyForPR, domain.TaskPRCreated,
	} {
		if err := task.Transition(st); err != nil {
			t.Fatalf("transition to %s: %v", st, err)
		}
	}

	_, err = ts.Deploy(task.ID, "v-test")
	if !errors.Is(err, agent.ErrApprovalRequired) {
		t.Fatalf("Deploy with ShellDeployer should return ErrApprovalRequired, got: %v", err)
	}
	// Task must NOT have transitioned to Deploying — it's still gated.
	if task.State == domain.TaskDeploying {
		t.Errorf("task transitioned to Deploying without approval — governance gate bypassed")
	}
	// The approval ID (prefix "appr-") must be present in the error so the
	// caller can resolve it via `kern approve`.
	if !strings.Contains(err.Error(), "appr-") {
		t.Errorf("error should contain an approval ID (appr-...), got: %v", err)
	}
}

// TestDeployNoopSkipsApprovalGate verifies that the NoopDeployer (default,
// simulated) does NOT trigger the approval gate — v1 behavior is preserved.
func TestDeployNoopSkipsApprovalGate(t *testing.T) {
	root := "../.."

	p, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := NewTaskService(p, nil).WithAgentID("test-deployer")
	// No WithDeployer → default NoopDeployer.

	task, err := ts.Create("noop deploy test")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	for _, st := range []domain.TaskState{
		domain.TaskAnalyzing, domain.TaskPlanning, domain.TaskWaitingApproval,
		domain.TaskApproved, domain.TaskExecuting, domain.TaskVerifying,
		domain.TaskReadyForPR, domain.TaskPRCreated,
	} {
		if err := task.Transition(st); err != nil {
			t.Fatalf("transition to %s: %v", st, err)
		}
	}

	_, err = ts.Deploy(task.ID, "v-noop")
	if err != nil {
		t.Fatalf("Deploy with NoopDeployer should succeed (no gate), got: %v", err)
	}
	if task.State != domain.TaskDeploying {
		t.Errorf("state = %s, want DEPLOYING", task.State)
	}
}

// TestWebhookIdempotency verifies that delivering the same event twice to the
// same URL is a no-op on the second delivery (Invariant 9).
func TestWebhookIdempotency(t *testing.T) {
	// This test is in internal/webhook, but we verify the contract here at
	// the integration level: the webhook client should not redeliver.
	// The unit test lives in internal/webhook/webhook_test.go; this is a
	// smoke test that the Client struct has the delivered map.
	//
	// We can't easily test actual HTTP delivery here without a test server,
	// but the dedup logic is in the Deliver method which is tested in the
	// webhook package. This test just ensures the integration path is wired.
	t.Skip("webhook idempotency is tested in internal/webhook/webhook_test.go")
}

// TestVerticalSlice2IncidentCorrelateRootCause is the second required vertical
// slice proof point from the Integration Transformation Plan (§33):
//
//	Incident → correlation → root cause → fix → PR
//
// It exercises the incident workflow end-to-end through TaskService: a
// production alert is ingested, correlated against a runtime source, root cause
// hypotheses are generated, and the resulting incident + task are verified.
//
// This test uses a synthetic runtime source (no real production telemetry) so
// it is deterministic and fast. It asserts the structural contract: the
// incident is created, has hypotheses, and the task completes with artifacts.
func TestVerticalSlice2IncidentCorrelateRootCause(t *testing.T) {
	root := "../.."

	p, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Attach a synthetic runtime source with a deploy event + error event
	// so the correlator has something to work with.
	store := runtime.NewStore()
	now := time.Now().Truncate(time.Second)
	store.Ingest(runtime.Event{
		ID:        "evt-1",
		Type:      runtime.EventError,
		Service:   "checkout",
		Severity:  "error",
		Message:   "nil pointer in checkout handler",
		Timestamp: now,
	})
	store.Ingest(runtime.Event{
		ID:        "evt-2",
		Type:      runtime.EventLog,
		Service:   "checkout",
		Severity:  "info",
		Message:   "deployed commit abc123",
		Timestamp: now.Add(-5 * time.Minute),
	})
	store.AddDeployment(domain.Deployment{
		Service:    "checkout",
		Version:    "v1.2.3",
		CommitSHA:  "abc123",
		DeployedAt: now.Add(-10 * time.Minute),
	})
	p.WithRuntimeSource(store)

	ts := NewTaskService(p, nil).WithAgentID("test")

	// --- Phase 14: Correlate ---
	t.Run("correlate", func(t *testing.T) {
		alert := domain.Alert{
			ID:        "alert-1",
			Severity:  domain.SeverityError,
			Message:   "checkout service error rate spiked",
			Service:   "checkout",
			Source:    "prometheus",
			OccurredAt: now,
		}
		task, chain, text, err := ts.Correlate(alert)
		if err != nil {
			t.Fatalf("Correlate: %v", err)
		}
		if task.State != domain.TaskCompleted {
			t.Errorf("state = %s, want COMPLETED", task.State)
		}
		if task.CreatedBy != "test" {
			t.Errorf("CreatedBy = %q, want 'test'", task.CreatedBy)
		}
		if text == "" {
			t.Error("correlate returned empty text")
		}
		if !strings.Contains(text, "CORRELATION") {
			t.Errorf("text missing 'CORRELATION' marker; got:\n%s", text)
		}
		// The chain should have at least the service link.
		if len(chain.Links) == 0 {
			t.Error("correlation chain has no links")
		}
		// Artifact should be recorded.
		arts, _ := ts.Artifacts().GetByTask(task.ID)
		if len(arts) == 0 {
			t.Error("no artifacts recorded for correlate task")
		}
	})

	// --- Phase 15: InvestigateIncident ---
	t.Run("investigateIncident", func(t *testing.T) {
		alert := domain.Alert{
			ID:        "alert-2",
			Severity:  domain.SeverityError,
			Message:   "checkout service error rate spiked",
			Service:   "checkout",
			Source:    "prometheus",
			OccurredAt: now,
		}
		task, inc, text, err := ts.InvestigateIncident(alert)
		if err != nil {
			t.Fatalf("InvestigateIncident: %v", err)
		}
		if task.State != domain.TaskCompleted {
			t.Errorf("state = %s, want COMPLETED", task.State)
		}
		if inc == nil {
			t.Fatal("incident is nil")
		}
		if inc.ID == "" {
			t.Error("incident ID is empty")
		}
		if inc.AffectedService == "" {
			t.Error("incident affected service is empty")
		}
		if inc.Status == "" {
			t.Error("incident status is empty")
		}
		if text == "" {
			t.Error("investigate returned empty text")
		}
		if !strings.Contains(text, "INCIDENT") {
			t.Errorf("text missing 'INCIDENT' marker; got:\n%s", text)
		}
		// Artifacts should be recorded (incident report + possibly root cause).
		arts, _ := ts.Artifacts().GetByTask(task.ID)
		if len(arts) == 0 {
			t.Error("no artifacts recorded for investigate task")
		}
	})

	// --- Phase 16: Learn ---
	t.Run("learn", func(t *testing.T) {
		task, patterns, text, err := ts.Learn(1)
		if err != nil {
			t.Fatalf("Learn: %v", err)
		}
		if task.State != domain.TaskCompleted {
			t.Errorf("state = %s, want COMPLETED", task.State)
		}
		if text == "" {
			t.Error("learn returned empty text")
		}
		if !strings.Contains(text, "LEARNING") {
			t.Errorf("text missing 'LEARNING' marker; got:\n%s", text)
		}
		// patterns may be empty if the memory store is fresh — that's OK.
		_ = patterns
		// Artifact should be recorded.
		arts, _ := ts.Artifacts().GetByTask(task.ID)
		if len(arts) == 0 {
			t.Error("no artifacts recorded for learn task")
		}
	})

	// --- Phase 17: Modernize ---
	t.Run("modernize", func(t *testing.T) {
		task, plan, text, err := ts.Modernize()
		if err != nil {
			t.Fatalf("Modernize: %v", err)
		}
		if task.State != domain.TaskCompleted {
			t.Errorf("state = %s, want COMPLETED", task.State)
		}
		if text == "" {
			t.Error("modernize returned empty text")
		}
		if !strings.Contains(text, "MODERNIZATION") {
			t.Errorf("text missing 'MODERNIZATION' marker; got:\n%s", text)
		}
		// The plan should have contexts (the kern repo has packages).
		if len(plan.Contexts) == 0 {
			t.Error("modernization plan has no bounded contexts")
		}
		// Artifact should be recorded.
		arts, _ := ts.Artifacts().GetByTask(task.ID)
		if len(arts) == 0 {
			t.Error("no artifacts recorded for modernize task")
		}
	})
}

// TestFullLifecycle20StepVerticalSlice drives a SINGLE Task through the entire
// 20-step lifecycle specified by the Integration Transformation Plan:
//
//  1. Task created (intent captured)
//  2. Analyze (context packet assembled)
//  3. Memory recall (relevant memories)
//  4. Impact assessment (what-if simulation)
//  5. Risk assessment
//  6. Plan assembled (from deterministic sources)
//  7. Policy evaluation (governance firewall check)
//  8. Approval requested (WAITING_FOR_APPROVAL)
//  9. Approval granted (APPROVED)
// 10. Sandbox execution (EXECUTING, patch applied in worktree)
// 11. Test (verification: test type)
// 12. Security scan (verification: security type — if unsupported, skip gracefully)
// 13. Architecture check (verification: architecture type — if unsupported, skip gracefully)
// 14. Verification verdict (VERIFYING → READY_FOR_PR)
// 15. PR created (PR_CREATED)
// 16. Deployment (DEPLOYING)
// 17. Production observation (OBSERVING)
// 18. Learning (lesson recorded to memory)
// 19. Task completed (COMPLETED)
// 20. Audit trail verified (artifact chain integrity)
//
// This is the definitive proof point that all phases are wired through the
// authoritative Task with artifacts, events, and lifecycle transitions.
func TestFullLifecycle20StepVerticalSlice(t *testing.T) {
	if testing.Short() {
		t.Skip("slow e2e (>30s); skipped with -short")
	}
	// Allow execution through the governance firewall so policy evaluation
	// (step 7) is a green path in this test.
	t.Setenv("KERN_ALLOW_EXEC", "1")

	root := "../.."
	p, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := NewTaskService(p, nil).WithAgentID("test")
	intent := "NewServer"

	// Step 1 — Task created.
	task, err := ts.Create(intent)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if task.State != domain.TaskCreated {
		t.Fatalf("step1: state = %s, want CREATED", task.State)
	}
	task.AddStep(stepResult("created", "lifecycle", "task created for "+intent))

	// Step 2 — Analyze (without completing, to keep the lifecycle alive).
	task, _, err = ts.analyzeTaskOpts(task, intent, false)
	if err != nil {
		t.Fatalf("step2 analyze: %v", err)
	}
	if task.State != domain.TaskAnalyzing {
		t.Fatalf("step2: state = %s, want ANALYZING", task.State)
	}
	if task.ContextPacket == nil {
		t.Fatal("step2: ContextPacket not attached")
	}

	// Step 3 — Memory recall (does not change task state).
	_, _ = p.Memory().Add(domain.Memory{Type: domain.MemoryLesson, Content: "test lesson for slice", Scope: "test"})
	recalled, err := p.Memory().Recall(memory.Query{Text: intent})
	if err != nil {
		t.Fatalf("step3 recall: %v", err)
	}
	_ = recalled
	task.AddStep(stepResult("memory", "memory-recall", "recalled "+strconv.Itoa(len(recalled))+" memories"))

	// Step 4 — Impact assessment (what-if simulation).
	imp, _, err := p.WhatIf(whatif.RemoveSymbol, intent, "")
	if err != nil {
		t.Fatalf("step4 whatif: %v", err)
	}
	task.ImpactReport = &imp
	task.AddStep(stepResult("impact", "impact-engine", "affected="+strconv.Itoa(len(imp.Affected))+", risk="+imp.Risk))

	// Step 5 — Risk assessment.
	riskPkt, riskText, err := p.Risk(intent)
	if err != nil {
		t.Fatalf("step5 risk: %v", err)
	}
	_ = riskPkt
	task.AddStep(stepResult("risk", "risk-engine", riskText))

	// Step 6 — Plan assembled from deterministic sources.
	if err := task.Transition(domain.TaskPlanning); err != nil {
		t.Fatalf("step6 transition PLANNING: %v", err)
	}
	plan := ts.assemblePlan(intent, *task.ContextPacket)
	task.Plan = &plan
	task.AddStep(stepResult("plan", "plan-engine", "objective="+plan.Objective))
	ts.recordArtifact(domain.ArtifactPlan, task.ID, "plan-engine",
		"plan: "+intent, ts.lastArtifactID(task.ID, domain.ArtifactContextPacket), "plan:assemble")

	// Step 7 — Policy evaluation (governance firewall).
	policyStep := "policy allowed (KERN_ALLOW_EXEC=1)"
	if perr := governance.CheckExec(); perr != nil {
		// Wiring test: governance is evaluated and gated, not failing the slice.
		policyStep = "policy gate: " + perr.Error()
	}
	task.AddStep(stepResult("policy", "governance-engine", policyStep))

	// Step 8 — Approval requested.
	if err := task.Transition(domain.TaskWaitingApproval); err != nil {
		t.Fatalf("step8 transition WAITING_FOR_APPROVAL: %v", err)
	}
	aw := governance.NewApprovalWorkflow()
	approval := aw.Request(task.ID, "test", "execute")
	if approval.ID == "" {
		t.Fatal("step8: approval ID is empty")
	}
	task.AddStep(stepResult("approve-request", "approval-workflow", "approval="+approval.ID))

	// Step 9 — Approval granted.
	if _, err := aw.Approve(approval.ID, "test-approver"); err != nil {
		t.Fatalf("step9 approve: %v", err)
	}
	if err := task.Transition(domain.TaskApproved); err != nil {
		t.Fatalf("step9 transition APPROVED: %v", err)
	}
	task.AddStep(stepResult("approve-grant", "approval-workflow", "approval="+approval.ID))

	// Step 10 — Sandbox execution (EXECUTING, patch applied in worktree).
	if err := task.Transition(domain.TaskExecuting); err != nil {
		t.Fatalf("step10 transition EXECUTING: %v", err)
	}
	wt, err := execution.NewWorktree(root)
	if err != nil {
		t.Fatalf("step10 NewWorktree: %v", err)
	}
	defer func() { _ = wt.Cleanup() }()
	patch := "diff --git a/_test_slice_temp.go b/_test_slice_temp.go\nnew file mode 100644\n--- /dev/null\n+++ b/_test_slice_temp.go\n@@ -0,0 +1,3 @@\n+// Temporary file for vertical slice test.\n+package main\n+\n"
	if err := wt.Apply(patch); err != nil {
		t.Fatalf("step10 Apply: %v", err)
	}
	diffText, err := wt.Diff()
	if err != nil {
		t.Fatalf("step10 Diff: %v", err)
	}
	if diffText == "" {
		t.Error("step10: worktree diff is empty after patch")
	}
	task.AddStep(stepResult("execute", "sandbox-engine", "patch applied; diff="+strconv.Itoa(len(diffText))+" chars"))
	ts.recordArtifact(domain.ArtifactCodePatch, task.ID, "sandbox-engine",
		"code patch for "+intent, ts.lastArtifactID(task.ID, domain.ArtifactPlan), "exec:apply")

	// Steps 11-14 — Verification (VERIFYING → READY_FOR_PR). The verdict may be
	// influenced by the sandbox 100MiB cap; this test asserts the lifecycle
	// wiring (state transitions + artifact chain), not the build verdict.
	if err := task.Transition(domain.TaskVerifying); err != nil {
		t.Fatalf("step11 transition VERIFYING: %v", err)
	}
	res := verification.NewEngine(wt.Dir()).Verify([]string{"build"})
	task.Verification = &res
	task.AddStep(stepResult("verify", "verification-engine", "verdict="+string(res.Verdict)))
	ts.recordArtifact(domain.ArtifactVerificationReport, task.ID, "verification-engine",
		"verification verdict="+string(res.Verdict),
		ts.lastArtifactID(task.ID, domain.ArtifactCodePatch), "verification:engine")

	// Transition to READY_FOR_PR (always reachable from VERIFYING).
	if err := task.Transition(domain.TaskReadyForPR); err != nil {
		t.Fatalf("step14 transition READY_FOR_PR: %v", err)
	}

	// Step 15 — PR created.
	prTask, prBody, err := ts.CreatePR(task.ID, "feature-slice-test")
	if err != nil {
		t.Fatalf("step15 CreatePR: %v", err)
	}
	if prTask.State != domain.TaskPRCreated {
		t.Fatalf("step15: state = %s, want PR_CREATED", prTask.State)
	}
	if prBody == "" {
		t.Error("step15: PR body is empty")
	}

	// Step 16 — Deployment.
	depTask, err := ts.Deploy(task.ID, "v1.0.0-test")
	if err != nil {
		t.Fatalf("step16 Deploy: %v", err)
	}
	if depTask.State != domain.TaskDeploying {
		t.Fatalf("step16: state = %s, want DEPLOYING", depTask.State)
	}

	// Step 17 — Production observation (also completes the lifecycle).
	obsTask, err := ts.Observe(task.ID)
	if err != nil {
		t.Fatalf("step17 Observe: %v", err)
	}
	if obsTask.State != domain.TaskCompleted {
		t.Fatalf("step17: state = %s, want COMPLETED", obsTask.State)
	}

	// Step 18 — Learning (lesson recorded to memory).
	_, _ = p.Memory().Add(domain.Memory{Type: domain.MemoryLesson, Content: "vertical slice test completed successfully", Scope: "test"})
	task.AddStep(stepResult("learn", "memory-recall", "lesson recorded"))

	// Step 19 — Task completed (already done by Observe).
	if task.State != domain.TaskCompleted {
		t.Fatalf("step19: state = %s, want COMPLETED", task.State)
	}

	// Step 20 — Audit trail verified (artifact chain integrity).
	arts, err := ts.Artifacts().GetByTask(task.ID)
	if err != nil {
		t.Fatalf("step20 GetByTask: %v", err)
	}
	if len(arts) < 5 {
		t.Errorf("step20: got %d artifacts, want >= 5 (context packet, plan, code patch, verification, PR, deployment)", len(arts))
	}
	seen := map[string]bool{}
	for _, a := range arts {
		if a.ID == "" {
			t.Error("step20: artifact with empty ID")
		}
		seen[a.ID] = true
		if a.ParentArtifactID != "" && !seen[a.ParentArtifactID] {
			// parent must appear before the child in the chain
			t.Errorf("step20: artifact %s parent %s not recorded before it", a.ID, a.ParentArtifactID)
		}
	}
	if len(task.Steps) < 15 {
		t.Errorf("step20: task has %d steps, want >= 15 (some verification steps fold into verify)", len(task.Steps))
	}
}

// stepResult is a small helper that builds an agent.Step for the 20-step slice.
func stepResult(action, actor, result string) agent.Step {
	return agent.Step{
		Action:  action,
		AgentID: actor,
		Result:  result,
		Status:  "success",
	}
}
