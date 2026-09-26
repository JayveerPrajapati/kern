package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/agent"
	"github.com/JayveerPrajapati/kern/internal/deployment"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/orgapprovals"
)

// newDeployService builds a TaskService with a REAL (Shell) deployer and a
// deploy-capable agent over an EMPTY index, so the deploy-gate tests run fast
// (the testfixture-based deploy tests in vertical_slice_test.go skip under
// -short; these do not).
func newDeployService(t *testing.T) *TaskService {
	t.Helper()
	root := t.TempDir()
	ix := index.New(root)
	p, err := NewWithIndex(root, ix)
	if err != nil {
		t.Fatalf("NewWithIndex: %v", err)
	}
	p.Firewall().WithAgents(governance.NewAgent(
		"test-deployer", "Test Deployer", "deployer",
		[]governance.Permission{{Resource: "production", Action: "deploy"}},
	))
	return NewTaskService(p, nil).WithAgentID("test-deployer").
		WithDeployer(&deployment.ShellDeployer{Command: "echo deploy", Timeout: 5 * time.Second})
}

// deployTaskToPRCreated creates a task and walks it to PR_CREATED so Deploy's
// transition is valid (same sequence as TestDeployApprovalGate).
func deployTaskToPRCreated(t *testing.T, ts *TaskService) *agent.Task {
	t.Helper()
	task, err := ts.Create("deploy org approval test")
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
	return task
}

// TestDeployHonorsOrgApproval is the Stage 3 proof point: with an org root
// configured and a matching APPROVED org approval in the store, Deploy
// consumes it and clears the governance gate — no ErrApprovalRequired, the
// task transitions to DEPLOYING, and the approval is single-use (marked
// used) and audited.
func TestDeployHonorsOrgApproval(t *testing.T) {
	t.Setenv("KERN_ALLOW_DEPLOY", "1") // let the ShellDeployer actually deploy
	orgRoot := t.TempDir()
	t.Setenv("KERN_ORG_ROOT", orgRoot)

	// Capture org audit events: the deploy-gate consume must land on the
	// org audit trail with the approval ID + granted-by.
	var mu sync.Mutex
	var audit []governance.AuditEntry
	orgapprovals.SetAuditHook(func(e governance.AuditEntry) { mu.Lock(); audit = append(audit, e); mu.Unlock() })
	t.Cleanup(func() { orgapprovals.SetAuditHook(nil) })

	// Seed an approved org approval for deploy/production.
	seed, err := orgapprovals.Create(orgRoot, "deploy", "production", "org-admin", "pre-approve multi-project deploy")
	if err != nil {
		t.Fatalf("orgapprovals.Create: %v", err)
	}
	if _, err := orgapprovals.Approve(orgRoot, seed.ID, "org-admin"); err != nil {
		t.Fatalf("orgapprovals.Approve: %v", err)
	}

	ts := newDeployService(t)
	task := deployTaskToPRCreated(t, ts)

	_, err = ts.Deploy(task.ID, "v-org-approval")
	if err != nil {
		t.Fatalf("Deploy with a matching org approval should clear the gate, got: %v", err)
	}
	if task.State != domain.TaskDeploying {
		t.Errorf("state = %s, want DEPLOYING (org approval cleared the gate)", task.State)
	}

	// Single-use: the org approval was consumed and is marked used.
	approvals := orgapprovals.List(orgRoot)
	if len(approvals) != 1 || approvals[0].Status != orgapprovals.StatusUsed {
		t.Fatalf("org approvals after deploy = %+v, want the seeded approval marked used", approvals)
	}

	// Audited: a consume entry carrying the approval ID + granted-by.
	mu.Lock()
	defer mu.Unlock()
	var consumed bool
	for _, e := range audit {
		if e.Action == "consume" && e.Resource == "approval" &&
			strings.Contains(e.Reason, seed.ID) && strings.Contains(e.Reason, "org-admin") {
			consumed = true
		}
	}
	if !consumed {
		t.Errorf("no org audit consume entry for %s (events: %+v)", seed.ID, audit)
	}
}

// TestDeployWithoutOrgApprovalStillGated asserts the per-project approval
// flow stays authoritative when an org root IS configured but no matching
// org approval exists: Deploy still returns ErrApprovalRequired.
func TestDeployWithoutOrgApprovalStillGated(t *testing.T) {
	orgRoot := t.TempDir()
	t.Setenv("KERN_ORG_ROOT", orgRoot)
	// No org approval seeded.

	ts := newDeployService(t)
	task := deployTaskToPRCreated(t, ts)

	_, err := ts.Deploy(task.ID, "v-no-org-approval")
	if !errors.Is(err, agent.ErrApprovalRequired) {
		t.Fatalf("Deploy without a matching org approval should still require the per-project approval, got: %v", err)
	}
	if task.State == domain.TaskDeploying {
		t.Errorf("task transitioned to Deploying without any approval — gate bypassed")
	}
}

// TestDeployNoOrgRootUnchanged is the backward-compat gate: with no
// KERN_ORG_ROOT the org approval package is inert and Deploy behaves
// byte-for-byte as before (ErrApprovalRequired, task parked).
func TestDeployNoOrgRootUnchanged(t *testing.T) {
	// No KERN_ORG_ROOT set.
	ts := newDeployService(t)
	task := deployTaskToPRCreated(t, ts)

	_, err := ts.Deploy(task.ID, "v-no-org-root")
	if !errors.Is(err, agent.ErrApprovalRequired) {
		t.Fatalf("Deploy without an org root must surface ErrApprovalRequired, got: %v", err)
	}
	if !strings.Contains(err.Error(), "appr-") {
		t.Errorf("error should carry the per-project approval ID (appr-...), got: %v", err)
	}
}

// TestDeployCorruptOrgStoreFailsClosed asserts a corrupt org approval store
// never clears the gate: Deploy surfaces the per-project ErrApprovalRequired
// exactly as if no org approval matched (an unreadable org override must not
// be trusted, and must not hard-deny the per-project path either).
func TestDeployCorruptOrgStoreFailsClosed(t *testing.T) {
	orgRoot := t.TempDir()
	t.Setenv("KERN_ORG_ROOT", orgRoot)
	if err := os.MkdirAll(filepath.Join(orgRoot, ".kern"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(orgapprovals.OrgApprovalsPath(orgRoot), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	ts := newDeployService(t)
	task := deployTaskToPRCreated(t, ts)

	_, err := ts.Deploy(task.ID, "v-corrupt-org-store")
	if !errors.Is(err, agent.ErrApprovalRequired) {
		t.Fatalf("Deploy with a corrupt org store must surface ErrApprovalRequired (per-project path), got: %v", err)
	}
	if task.State == domain.TaskDeploying {
		t.Errorf("task transitioned to Deploying despite a corrupt org store — gate bypassed")
	}
}
