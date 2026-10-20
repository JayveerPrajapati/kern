package approvalcli

import (
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/agent"
	"github.com/JayveerPrajapati/kern/internal/app"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/tasklife"
)

// TestApproveListEmpty locks the no-args branch on an empty store: it must
// print "no pending approvals", never a bare table header.
func TestApproveListEmpty(t *testing.T) {
	root := newRoot(t)
	out := captureStdout(t, func() { RunApprove([]string{"list", "--root", root}) })
	if !strings.Contains(out, "no pending approvals") {
		t.Fatalf("expected empty listing, got %q", out)
	}
}

// TestApproveListPending locks the table render: ID, TASK, REQUESTER, REASON
// columns with the seeded values.
func TestApproveListPending(t *testing.T) {
	root := newRoot(t)
	id := approvalFixture(t, root, "t-1", "alice", "deploy to prod")
	out := captureStdout(t, func() { RunApprove([]string{"list", "--root", root}) })
	for _, want := range []string{"ID", "TASK", "REQUESTER", "REASON", id, "t-1", "alice", "deploy to prod"} {
		if !strings.Contains(out, want) {
			t.Errorf("listing missing %q:\n%s", want, out)
		}
	}
}

// TestApproveDecision locks the approve path on a task-less approval: the
// decision is persisted (default approver cli-user) and the store no longer
// lists the approval as pending.
func TestApproveDecision(t *testing.T) {
	root := newRoot(t)
	id := approvalFixture(t, root, "", "alice", "deploy to prod")
	withScriptedTerminal(t, id)
	out := captureStdout(t, func() { RunApprove([]string{"--root", root, id}) })
	for _, want := range []string{"approved: " + id, "approver: cli-user"} {
		if !strings.Contains(out, want) {
			t.Errorf("approve output missing %q:\n%s", want, out)
		}
	}
	out = captureStdout(t, func() { RunApprove([]string{"list", "--root", root}) })
	if !strings.Contains(out, "no pending approvals") {
		t.Errorf("approval should be decided; listing:\n%s", out)
	}
}

// TestApproveReject locks the --reject --reason --approver flags on a
// task-less approval.
func TestApproveReject(t *testing.T) {
	root := newRoot(t)
	id := approvalFixture(t, root, "", "alice", "deploy to prod")
	out := captureStdout(t, func() {
		RunApprove([]string{"--root", root, "--reject", "--reason", "not now", "--approver", "senior", id})
	})
	for _, want := range []string{"rejected: " + id, "(by senior)"} {
		if !strings.Contains(out, want) {
			t.Errorf("reject output missing %q:\n%s", want, out)
		}
	}
}

// TestApproveGatedTaskAdvances covers the full command-level contract of the
// gated path: approving an approval bound to a task parked at
// WAITING_FOR_APPROVAL must advance the persisted task to APPROVED (the app
// layer's ResolveApprovalForTask is separately covered in internal/app).
func TestApproveGatedTaskAdvances(t *testing.T) {
	root := newRoot(t)
	p, err := app.New(root)
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	ts := tasklife.NewTaskService(p, nil).WithAgentID("test")
	task, err := ts.Create("deploy the release")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Walk the task to the approval gate and persist (the command's own
	// TaskService starts with an empty registry, so only the store matters).
	for _, st := range []domain.TaskState{domain.TaskAnalyzing, domain.TaskPlanning, domain.TaskWaitingApproval} {
		if err := task.Transition(st); err != nil {
			t.Fatalf("transition %s: %v", st, err)
		}
	}
	if _, err := agent.NewTaskStore(root).Save(*task); err != nil {
		t.Fatalf("persist: %v", err)
	}
	id := approvalFixture(t, root, task.ID, "alice", "deploy to prod")
	withScriptedTerminal(t, id)

	out := captureStdout(t, func() { RunApprove([]string{"--root", root, id}) })
	for _, want := range []string{"approved: " + id, "task: " + task.ID} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	got, err := agent.NewTaskStore(root).Get(task.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != domain.TaskApproved {
		t.Errorf("gated task state = %s, want APPROVED", got.State)
	}
}

// TestApproveUnknownID locks the error path: an unknown approval ID exits 1
// (the exported RunApprove returns the code; the internal fatal sentinel is
// recovered at the package boundary), not a silent no-op.
func TestApproveUnknownID(t *testing.T) {
	root := newRoot(t)
	// Script the terminal so the gate passes (typed == id) and the failure
	// is the genuine not-found path, not a no-terminal refusal.
	withScriptedTerminal(t, "t-nonexistent")
	expectCode(t, 1, func() int { return RunApprove([]string{"--root", root, "t-nonexistent"}) })
}

// TestApproveRefusedWithoutTerminal locks the fail-closed interactive gate:
// no interactive terminal (piped stdin, agent context) → approval NOT decided,
// exit 1, and the abort is recorded in the governance chain as "no-terminal" —
// an environmental failure, not a human refusal (B3).
func TestApproveRefusedWithoutTerminal(t *testing.T) {
	root := newRoot(t)
	id := approvalFixture(t, root, "", "alice", "deploy to prod")
	withNoTerminal(t)
	expectCode(t, 1, func() int { return RunApprove([]string{"--root", root, id}) })

	if e := auditEntryFor(t, root, "approval:"+id); e == nil || e.Result != "no-terminal" {
		t.Fatalf("expected no-terminal audit entry for %s, got %+v", id, e)
	}
	store := governance.NewFileStore(root)
	rec, err := store.Get(id)
	if err != nil || rec.DecidedAt != nil {
		t.Fatalf("approval must remain undecided after refusal: %+v err=%v", rec, err)
	}
}

// TestApproveRefusedWrongToken locks the typed-id intent signal: the human
// typed something other than the approval id → refused, undecided, audited.
func TestApproveRefusedWrongToken(t *testing.T) {
	root := newRoot(t)
	id := approvalFixture(t, root, "", "alice", "deploy to prod")
	withScriptedTerminal(t, "WRONG-TOKEN")
	expectCode(t, 1, func() int { return RunApprove([]string{"--root", root, id}) })

	if e := auditEntryFor(t, root, "approval:"+id); e == nil || e.Result != "refused" {
		t.Fatalf("expected refused audit entry for %s, got %+v", id, e)
	}
	store := governance.NewFileStore(root)
	rec, err := store.Get(id)
	if err != nil || rec.DecidedAt != nil {
		t.Fatalf("approval must remain undecided after refusal: %+v err=%v", rec, err)
	}
}

// TestApproveTerminalRequiresTTYStdin pins the no-fallback contract (B3):
// the production resolver must NOT reach for /dev/tty when stdin is not a
// terminal — a piped/scripted caller is non-interactive by definition, and
// the ctty fallback prompted on a surface the caller cannot see (and in
// job-control contexts the confirm read stopped the process past its own
// timeout — the QA 2026-10-15 foreground hang).
func TestApproveTerminalRequiresTTYStdin(t *testing.T) {
	// go test runs test binaries with stdin as /dev/null — never a TTY.
	_, err := approveTerminal()
	if err == nil {
		t.Fatalf("approveTerminal must fail when stdin is not a TTY, got nil")
	}
	if !strings.Contains(err.Error(), "stdin is not a terminal") {
		t.Fatalf("error should name the piped-stdin cause, got: %v", err)
	}
}

// TestApproveRefusedTimeout locks the fail-closed timeout: a hung terminal
// (no human input within the bound) aborts instead of blocking forever, and
// the audit chain records it as "confirm-timeout" — no human typed anything,
// so it is an aborted attempt, not a refusal (B3).
func TestApproveRefusedTimeout(t *testing.T) {
	root := newRoot(t)
	id := approvalFixture(t, root, "", "alice", "deploy to prod")
	withBlockingTerminal(t)
	expectCode(t, 1, func() int { return RunApprove([]string{"--root", root, id}) })

	if e := auditEntryFor(t, root, "approval:"+id); e == nil || e.Result != "confirm-timeout" {
		t.Fatalf("expected confirm-timeout audit entry for %s, got %+v", id, e)
	}
	store := governance.NewFileStore(root)
	rec, err := store.Get(id)
	if err != nil || rec.DecidedAt != nil {
		t.Fatalf("approval must remain undecided after timeout: %+v err=%v", rec, err)
	}
}

// TestApproveWaitNoticeOnStdout locks the visibility fix (B3): the
// confirmation prompt goes to the interactive terminal, which a
// non-interactive caller cannot see — so stdout must say WHY the command is
// waiting (the id, the confirm timeout, and the agent-side wait command)
// instead of reading as a silent hang.
func TestApproveWaitNoticeOnStdout(t *testing.T) {
	root := newRoot(t)
	id := approvalFixture(t, root, "", "alice", "deploy to prod")
	withScriptedTerminal(t, id)

	out := captureStdout(t, func() { RunApprove([]string{"--root", root, id}) })
	for _, want := range []string{
		"waiting for interactive confirmation of " + id,
		"approval NOT decided until then",
		"kern approval wait " + id,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout notice missing %q:\n%s", want, out)
		}
	}
}

// TestApproveBlueprintAlreadyDecidedSkipsGate locks the guard order (B3): a
// blueprint two-person-rule request that already carries a decision must be
// refused with rc=3 BEFORE the interactive gate runs — prompting a human to
// confirm an already-decided approval reads as a hang. withNoTerminal proves
// the gate never ran: if it had, the failure would be rc=1 (no-terminal).
func TestApproveAlreadyDecidedExits3(t *testing.T) {
	root := newRoot(t)
	store := governance.NewFileStore(root)
	if err := store.AddPending(domain.Approval{ID: "apr-test", Status: "pending", TaskID: ""}); err != nil {
		t.Fatalf("AddPending: %v", err)
	}
	if _, err := store.Decide("apr-test", "cli-user", true, ""); err != nil {
		t.Fatalf("Decide: %v", err)
	}
	expectCode(t, 3, func() int { return RunApprove([]string{"--root", root, "apr-test"}) })
}

// TestBlueprintDecisionWritesAuditChain verifies D8: a blueprint approve/
// reject decision (decideBlueprintApproval) is appended to the tamper-evident
// .kern/audit chain with the same entry shape governance.FileStore.recordAudit
// uses (Action approve/reject, Resource "approval:<id>", Policy "approval",
// Result approved/denied), the chain stays intact, and `kern audit` renders
// the decision exactly once — the render-layer synthesis in audit.go must
// dedupe against the chain entry rather than show a double row.
