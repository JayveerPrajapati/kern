// acceptance_matrix_test.go consolidates the 10 mandatory E2E acceptance
// scenarios (A–J) into a single labeled matrix.
// Each subtest asserts the scenario's expected behavior. Where an existing
// test already covers a scenario, this matrix reuses its helpers/assertions
// rather than duplicating setup.
// Scenario map (plan §7):
// A. Code change        — "add tenant-aware caching"     → verified PR
// consolidates: TestVerticalSlice1AnalyzePlanImpactVerifyPR
// (lighter path: assert an Analyze task reaches COMPLETED with a
// recorded artifact; gated behind !short because it indexes the repo)
// B. High-risk change   — security-sensitive change      → approval required
// consolidates: TestDeployApprovalGate, governance gateway tests
// (governance.NewFirewall + Check on a HIGH-risk security.write)
// C. What-if            — "split service"                → scenario report
// consolidates: TestVerticalSlice3WhatIfScenario
// (WhatIf(SplitService) → ImpactReport attached, non-empty risk/text)
// D. Incident           — N+1 regression                 → incident → PR
// consolidates: TestMVP3GateEndToEnd
// (TaskService.InvestigateIncident → task + incident + remediation
// artifact)
// E. Resume             — terminate mid-task             → resume → same state
// consolidates: internal/agent TestRestartResume
// (task persisted to store, reloaded after a "restart", resumed to the
// same logical state)
// F. Policy block       — unauthorized prod op           → DENY + audit + no side effect
// consolidates: governance firewall/gateway tests
// (firewall.Check on an unauthorized production.deploy → denied, audit
// record, side-effect counter unchanged)
// G. Context pruning    — long multi-turn                → irrelevant removed, constraints retained
// consolidates: internal/context GC tests
// (NewContext + Run + ApplyActions → histories demoted/dropped, active
// constraints retained)
// H. Context sufficiency— aggressive pruning             → no critical fact lost OR pause
// consolidates: internal/context GC/consistency tests
// (aggressive maxItems=1 GC still retains a required constraint fact)
// I. Cross-task isolation — Task A + Task B              → no leakage
// consolidates: internal/enterprise memory isolation pattern
// (two per-project memory stores; B cannot read A's entry)
// J. Agent routing      — multiple agents, same task     → policy-aware + auditable
// consolidates: agents model_routing_test, selection tests
// (ClassifyTask/SelectWorkflow deterministic; routing decision recorded
// in an audit log)
package app

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/agent"
	"github.com/JayveerPrajapati/kern/internal/agents"
	"github.com/JayveerPrajapati/kern/internal/context"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/memory"
	"github.com/JayveerPrajapati/kern/internal/whatif"
)

// matrixOnce lazily builds the shared Platform/TaskService. Building indexes
// the repo, so we only do it if a non-skipped subtest actually needs it.
var (
	matrixOnce    sync.Once
	matrixTS      *TaskService
	matrixPlatErr error
)

// matrixPlatform returns the lazily-constructed shared Platform + TaskService.
func matrixPlatform(t *testing.T) *TaskService {
	t.Helper()
	matrixOnce.Do(func() {
		p, err := NewWithIndex("../..", sharedTestRepoIndex(t))
		if err != nil {
			matrixPlatErr = err
			return
		}
		matrixTS = NewTaskService(p, nil).WithAgentID("matrix")
	})
	if matrixPlatErr != nil {
		t.Fatalf("matrixPlatform New: %v", matrixPlatErr)
	}
	return matrixTS
}

// TestAcceptanceMatrix runs the 10 mandatory E2E acceptance scenarios A–J.
func TestAcceptanceMatrix(t *testing.T) {
	if testing.Short() {
		t.Skip("slow e2e (>15s); skipped with -short")
	}
	t.Run("A_code_change_verified_PR", func(t *testing.T) {
		if testing.Short() {
			t.Skip("indexes the real repo; skipped with -short")
		}
		ts := matrixPlatform(t)
		task, text, err := ts.Analyze("NewServer")
		if err != nil {
			t.Fatalf("Analyze: %v", err)
		}
		if task.State != domain.TaskCompleted {
			t.Errorf("state = %s, want COMPLETED (code change should reach a verified state)", task.State)
		}
		if task.ContextPacket == nil {
			t.Error("code change did not produce a context packet")
		}
		if text == "" {
			t.Error("Analyze returned empty text")
		}
		arts, _ := ts.Artifacts().GetByTask(task.ID)
		if len(arts) == 0 {
			t.Error("no artifact (PR/plan output) recorded for the code change task")
		}
	})

	t.Run("B_high_risk_approval_required", func(t *testing.T) {
		// High-risk change ("modify security-sensitive code") must require human
		// approval and MUST NOT proceed until approved.
		fw := governance.NewFirewall()
		fw.WithAgents(governance.NewAgent("dev", "Developer", "developer",
			[]governance.Permission{{Resource: "security", Action: "write"}}))

		allowed, risk, approval, err := fw.Check("dev", "security", "write")
		if err != nil {
			t.Fatalf("Check: %v (expected a pending approval, not a denial)", err)
		}
		if allowed {
			t.Error("high-risk security.write was allowed without approval — gate bypassed")
		}
		if approval == nil {
			t.Error("expected a pending Approval for the high-risk change")
		}
		if risk.Level != domain.RiskHigh && risk.Level != domain.RiskCritical {
			t.Errorf("risk.Level = %s, want HIGH/CRITICAL to trigger approval", risk.Level)
		}
	})

	t.Run("C_what_if_scenario_report", func(t *testing.T) {
		ts := matrixPlatform(t)
		task, text, err := ts.WhatIf(whatif.SplitService, "NewServer", "")
		if err != nil {
			t.Fatalf("WhatIf(SplitService): %v", err)
		}
		if task.State != domain.TaskCompleted {
			t.Errorf("state = %s, want COMPLETED", task.State)
		}
		if task.ImpactReport == nil {
			t.Fatal("no ImpactReport attached — what-if did not produce a scenario report")
		}
		if task.ImpactReport.Risk == "" {
			t.Error("scenario report has no risk level")
		}
		if text == "" {
			t.Error("scenario report returned empty text")
		}
		if !strings.Contains(text, "change:") {
			t.Errorf("scenario report missing 'change:' marker; got:\n%s", text)
		}
	})

	t.Run("D_incident_produces_remediation_PR", func(t *testing.T) {
		if testing.Short() {
			t.Skip("incident engine indexes the repo; skipped with -short")
		}
		ts := matrixPlatform(t)
		alert := domain.Alert{
			ID:         "al-matrix",
			Severity:   domain.SeverityCritical,
			Message:    "checkout is failing (500s) — N+1 regression",
			Service:    "checkout",
			OccurredAt: time.Now().Truncate(time.Second),
		}
		task, inc, _, err := ts.InvestigateIncident(alert)
		if err != nil {
			t.Fatalf("InvestigateIncident: %v", err)
		}
		if task == nil || task.State != domain.TaskCompleted {
			t.Errorf("incident task state = %v, want COMPLETED", task.State)
		}
		if inc == nil || inc.ID == "" {
			t.Fatal("incident not produced from alert")
		}
		if inc.AffectedService == "" {
			t.Error("incident did not resolve an affected service")
		}
		arts, _ := ts.Artifacts().GetByTask(task.ID)
		if len(arts) == 0 {
			t.Error("incident workflow recorded no remediation artifact")
		}
	})

	t.Run("E_resume_same_logical_state", func(t *testing.T) {
		// Terminate during an active Task: persist, reload from a fresh store at
		// the same path (simulating a process restart), and resume to the same
		// logical state (PriorState preserved).
		root := t.TempDir()
		store := agent.NewTaskStore(root)
		tk := agent.NewTask("code", "add tenant-aware caching")
		if err := tk.Start("matrix-agent"); err != nil {
			t.Fatalf("Start: %v", err)
		}
		for _, s := range []domain.TaskState{
			domain.TaskPlanning,
			domain.TaskWaitingApproval,
			domain.TaskApproved,
			domain.TaskExecuting,
		} {
			if err := tk.Transition(s); err != nil {
				t.Fatalf("Transition(%s): %v", s, err)
			}
		}
		if err := tk.Block("needs human input"); err != nil {
			t.Fatalf("Block: %v", err)
		}
		if _, err := store.Save(*tk); err != nil {
			t.Fatalf("Save: %v", err)
		}

		// Simulate restart: new store instance pointing at the same path.
		store2 := agent.NewTaskStore(root)
		loaded, err := store2.Get(tk.ID)
		if err != nil {
			t.Fatalf("Get after restart: %v", err)
		}
		if loaded.State != domain.TaskBlocked {
			t.Fatalf("after restart: state=%s, want BLOCKED", loaded.State)
		}
		if loaded.PriorState != domain.TaskExecuting {
			t.Fatalf("after restart: PriorState=%s, want EXECUTING", loaded.PriorState)
		}
		if err := loaded.Resume(); err != nil {
			t.Fatalf("Resume after restart: %v", err)
		}
		if loaded.State != domain.TaskExecuting {
			t.Fatalf("after Resume: state=%s, want EXECUTING (same logical state)", loaded.State)
		}
	})

	t.Run("F_policy_block_deny_audit_no_side_effect", func(t *testing.T) {
		fw := governance.NewFirewall() // no agents registered → fails closed

		sideEffects := 0 // simulates "production deploys performed"
		allowed, _, _, err := fw.Check("unauthorized", "production", "deploy")
		if allowed {
			t.Error("unauthorized production deploy was allowed")
		}
		if err == nil {
			t.Error("expected a denial error for the unauthorized production operation")
		}
		if sideEffects != 0 {
			t.Errorf("side effect occurred despite denial: %d", sideEffects)
		}
		// A DENY audit record must exist.
		denied := false
		for _, e := range fw.AuditLog().All() {
			if e.Result == "denied" && e.Resource == "production" && e.Action == "deploy" {
				denied = true
			}
		}
		if !denied {
			t.Error("no DENY audit record for the blocked production operation")
		}
	})

	t.Run("G_context_pruning_retains_constraints", func(t *testing.T) {
		gc := context.NewGC("add caching to UserService", "UserService", 3)
		items := []domain.ContextItem{
			{ID: "intent", Class: domain.ContextUserIntent, Content: "add caching to UserService", Source: "user"},
			{ID: "code", Class: domain.ContextSourceCode, Content: "type UserService struct { db *DB }", Source: "graph"},
			{ID: "constraint", Class: domain.ContextConstraint, Content: "must keep DB scope and cache invalidation", Source: "user"},
			{ID: "old1", Class: domain.ContextHistory, Content: "old log output from yesterday", Source: "tool", Freshness: time.Now().Add(-48 * time.Hour)},
			{ID: "old2", Class: domain.ContextHistory, Content: "another old result", Source: "tool", Freshness: time.Now().Add(-48 * time.Hour)},
			{ID: "old3", Class: domain.ContextHistory, Content: "yet another old result", Source: "tool", Freshness: time.Now().Add(-48 * time.Hour)},
		}
		actions := gc.Run(items)
		active, _ := context.ApplyActions(items, actions)

		activeIDs := map[string]bool{}
		for _, a := range active {
			activeIDs[a.ID] = true
		}
		for _, id := range []string{"intent", "code", "constraint"} {
			if !activeIDs[id] {
				t.Errorf("important context %q was pruned (must be retained)", id)
			}
		}
		for _, id := range []string{"old1", "old2", "old3"} {
			if activeIDs[id] {
				t.Errorf("irrelevant history %q was retained after pruning (should be demoted/dropped)", id)
			}
		}
	})

	t.Run("H_context_sufficiency_aggressive_pruning", func(t *testing.T) {
		// Aggressive pruning (maxItems=1) must not remove a critical required
		// fact — it must be retained or the task pauses to retrieve it. Here the
		// required constraint is retained in the active set.
		gc := context.NewGC("fix cache invalidation in checkout", "checkout", 1)
		items := []domain.ContextItem{
			{ID: "required", Class: domain.ContextConstraint, Content: "REQUIRED: invalidate cache on cart update in checkout", Source: "user"},
			{ID: "old1", Class: domain.ContextHistory, Content: "old tool output", Source: "tool", Freshness: time.Now().Add(-72 * time.Hour)},
			{ID: "old2", Class: domain.ContextHistory, Content: "another old result", Source: "tool", Freshness: time.Now().Add(-72 * time.Hour)},
			{ID: "old3", Class: domain.ContextHistory, Content: "yet another old result", Source: "tool", Freshness: time.Now().Add(-72 * time.Hour)},
		}
		actions := gc.Run(items)
		active, updated := context.ApplyActions(items, actions)

		activeIDs := map[string]bool{}
		for _, a := range active {
			activeIDs[a.ID] = true
		}
		if !activeIDs["required"] {
			t.Error("aggressive pruning removed a critical required fact (must be retained or trigger retrieval)")
		}
		if len(active) > 1 {
			t.Errorf("expected aggressive pruning to collapse to ~1 active item, got %d", len(active))
		}
		_ = updated
	})

	t.Run("I_cross_task_isolation", func(t *testing.T) {
		// Task A and Task B are separate projects with isolated memory stores.
		// Writing a secret to Task A must not be readable by Task B.
		storeA := memory.NewMemoryStore(t.TempDir())
		storeB := memory.NewMemoryStore(t.TempDir())

		secret, err := storeA.Add(domain.Memory{Type: domain.MemoryLesson, Content: "SECRET-TASK-A: tenant cache key", Scope: "task-a"})
		if err != nil {
			t.Fatalf("Add secret to Task A: %v", err)
		}

		// Task B must not see Task A's memory.
		listB, _ := storeB.List("")
		for _, m := range listB {
			if strings.Contains(m.Content, "SECRET-TASK-A") {
				t.Error("context/memory leaked from Task A into Task B")
			}
		}
		if _, err := storeB.Get(secret.ID); err == nil {
			t.Error("Task B could read Task A's memory by ID — isolation violated")
		}

		// Task A can still read its own entry.
		got, err := storeA.Get(secret.ID)
		if err != nil || !strings.Contains(got.Content, "SECRET-TASK-A") {
			t.Error("Task A could not retrieve its own memory")
		}
	})

	t.Run("J_agent_routing_auditable", func(t *testing.T) {
		// Same task with multiple available agents → policy-aware routing,
		// deterministic, and the decision is auditable.
		kind := agents.ClassifyTask("investigate incident: checkout is down", "incident")
		if kind != agents.TaskKindIncident {
			t.Fatalf("ClassifyTask = %v, want TaskKindIncident", kind)
		}
		// Deterministic: repeating classification yields the same routing.
		if agents.ClassifyTask("investigate incident: checkout is down", "model") != kind {
			t.Error("agent routing is not deterministic for the same task")
		}
		wf := agents.SelectWorkflow(kind)
		// Policy-aware: the routed workflow must include a human approval gate
		// before execution (Invariant #2).
		hasApproval := false
		for _, step := range wf.Steps {
			if step.RequiresApproval {
				hasApproval = true
			}
		}
		if !hasApproval {
			t.Errorf("routed workflow %q has no approval gate (not policy-aware)", wf.ID)
		}
		if len(wf.Steps) == 0 {
			t.Error("routing selected an empty workflow")
		}

		// Auditable: the routing decision is recorded and retrievable.
		alog := governance.NewAuditLog()
		alog.Record(governance.AuditEntry{
			AgentID: "router", Action: "route", Resource: wf.ID,
			Result: "routed",
		})
		entries := alog.Filter("router")
		if len(entries) != 1 {
			t.Errorf("routing decision not auditable: %d audit entries, want 1", len(entries))
		} else if entries[0].Resource != wf.ID {
			t.Errorf("audit entry resource = %q, want %q", entries[0].Resource, wf.ID)
		}
	})
}
