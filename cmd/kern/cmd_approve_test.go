package main

import (
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/agent"
	"github.com/JayveerPrajapati/kern/internal/app"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/gates"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/storage"
	"github.com/JayveerPrajapati/kern/internal/tasklife"
)

// newRoot returns an isolated project root for command tests: XDG_CACHE_HOME
// is redirected so stores keyed by project root stay private to this test.
func newRoot(t *testing.T) string {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	return t.TempDir()
}

// expectExit runs fn and asserts it panicked with the exitError sentinel (the
// house pattern for testing fatal/fatalUsage paths without killing the test
// binary).
func expectExit(t *testing.T, code int, fn func()) {
	t.Helper()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatalf("expected exitError{%d}, got no panic", code)
		}
		if e, ok := r.(exitError); !ok || e.code != code {
			t.Fatalf("expected exitError{%d}, got %v", code, r)
		}
	}()
	fn()
}

// approvalFixture seeds a persisted pending approval (root/.kern/approvals.json
// — the same store `kern approve` reads) and returns its ID.
func approvalFixture(t *testing.T, root, taskID, requester, reason string) string {
	t.Helper()
	aw := governance.NewPersistedApprovalWorkflow(root)
	a, err := aw.Request(taskID, requester, reason)
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	if a.ID == "" {
		t.Fatal("Request returned an empty approval ID")
	}
	return a.ID
}

// withScriptedTerminal replaces the interactive approval terminal with a
// scripted double whose read side is the human's typed confirmation and whose
// write side discards prompts (a real terminal's read and write paths are
// independent — unlike a bytes.Buffer, which would echo the prompt back as
// input). Restores the production resolver after.
func withScriptedTerminal(t *testing.T, typed string) {
	t.Helper()
	old := approveTerminal
	approveTerminal = func() (io.ReadWriter, error) {
		return terminal{r: strings.NewReader(typed + "\n"), w: io.Discard}, nil
	}
	t.Cleanup(func() { approveTerminal = old })
}

// terminal is a test double for an interactive terminal: reads come from the
// human's input source, writes (prompts) go to a sink.
type terminal struct {
	r io.Reader
	w io.Writer
}

func (t terminal) Read(p []byte) (int, error)  { return t.r.Read(p) }
func (t terminal) Write(p []byte) (int, error) { return t.w.Write(p) }

// withBlockingTerminal replaces the interactive approval terminal with one
// that never delivers input, and shrinks the confirmation timeout so the
// fail-closed timeout path is testable without a 15s wait.
func withBlockingTerminal(t *testing.T) {
	t.Helper()
	oldTerm := approveTerminal
	oldTimeout := approveConfirmTimeout
	pr, pw := io.Pipe()
	approveTerminal = func() (io.ReadWriter, error) { return blockingTerminal{r: pr}, nil }
	approveConfirmTimeout = 50 * time.Millisecond
	t.Cleanup(func() {
		approveTerminal = oldTerm
		approveConfirmTimeout = oldTimeout
		pw.Close()
		pr.Close()
	})
}

// blockingTerminal is an io.ReadWriter whose Read blocks until the pipe
// delivers or closes (nothing ever writes), so the approval gate's bounded
// read hits its timeout.
type blockingTerminal struct{ r *io.PipeReader }

func (b blockingTerminal) Read(p []byte) (int, error)  { return b.r.Read(p) }
func (b blockingTerminal) Write(p []byte) (int, error) { return len(p), nil }

// withNoTerminal replaces the interactive approval terminal with a resolver
// that always fails — the non-interactive (agent/pipe) case.
func withNoTerminal(t *testing.T) {
	t.Helper()
	old := approveTerminal
	approveTerminal = func() (io.ReadWriter, error) { return nil, errors.New("no terminal") }
	t.Cleanup(func() { approveTerminal = old })
}

// auditEntryFor returns the governance-chain entry for resource, or nil.
func auditEntryFor(t *testing.T, root, resource string) *governance.AuditEntry {
	t.Helper()
	auditDir := filepath.Join(root, ".kern", "audit")
	l := governance.NewAuditLog().
		WithStore(storage.NewLog(auditDir)).
		WithLockPath(filepath.Join(auditDir, ".lock"))
	if _, err := l.Replay(); err != nil {
		t.Fatalf("Replay: %v", err)
	}
	for i := range l.All() {
		e := &l.All()[i]
		if e.Resource == resource {
			return e
		}
	}
	return nil
}

// TestApproveListEmpty locks the no-args branch on an empty store: it must
// print "no pending approvals", never a bare table header.
func TestApproveListEmpty(t *testing.T) {
	root := newRoot(t)
	out := captureStdout(t, func() { runApprove([]string{"list", "--root", root}) })
	if !strings.Contains(out, "no pending approvals") {
		t.Fatalf("expected empty listing, got %q", out)
	}
}

// TestApproveListPending locks the table render: ID, TASK, REQUESTER, REASON
// columns with the seeded values.
func TestApproveListPending(t *testing.T) {
	root := newRoot(t)
	id := approvalFixture(t, root, "t-1", "alice", "deploy to prod")
	out := captureStdout(t, func() { runApprove([]string{"list", "--root", root}) })
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
	out := captureStdout(t, func() { runApprove([]string{"--root", root, id}) })
	for _, want := range []string{"approved: " + id, "approver: cli-user"} {
		if !strings.Contains(out, want) {
			t.Errorf("approve output missing %q:\n%s", want, out)
		}
	}
	out = captureStdout(t, func() { runApprove([]string{"list", "--root", root}) })
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
		runApprove([]string{"--root", root, "--reject", "--reason", "not now", "--approver", "senior", id})
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

	out := captureStdout(t, func() { runApprove([]string{"--root", root, id}) })
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
// with a fatal (sentinel panic), not a silent no-op.
func TestApproveUnknownID(t *testing.T) {
	root := newRoot(t)
	// Script the terminal so the gate passes (typed == id) and the failure
	// is the genuine not-found path, not a no-terminal refusal.
	withScriptedTerminal(t, "t-nonexistent")
	expectExit(t, 1, func() { runApprove([]string{"--root", root, "t-nonexistent"}) })
}

// TestApproveRefusedWithoutTerminal locks the fail-closed interactive gate:
// no interactive terminal (piped stdin, agent context) → approval NOT decided,
// exit 1, and the refusal is recorded in the governance chain.
func TestApproveRefusedWithoutTerminal(t *testing.T) {
	root := newRoot(t)
	id := approvalFixture(t, root, "", "alice", "deploy to prod")
	withNoTerminal(t)
	expectExit(t, 1, func() { runApprove([]string{"--root", root, id}) })

	if e := auditEntryFor(t, root, "approval:"+id); e == nil || e.Result != "refused" {
		t.Fatalf("expected refused audit entry for %s, got %+v", id, e)
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
	expectExit(t, 1, func() { runApprove([]string{"--root", root, id}) })

	if e := auditEntryFor(t, root, "approval:"+id); e == nil || e.Result != "refused" {
		t.Fatalf("expected refused audit entry for %s, got %+v", id, e)
	}
	store := governance.NewFileStore(root)
	rec, err := store.Get(id)
	if err != nil || rec.DecidedAt != nil {
		t.Fatalf("approval must remain undecided after refusal: %+v err=%v", rec, err)
	}
}

// TestApproveRefusedTimeout locks the fail-closed timeout: a hung terminal
// (no human input within the bound) refuses instead of blocking forever.
func TestApproveRefusedTimeout(t *testing.T) {
	root := newRoot(t)
	id := approvalFixture(t, root, "", "alice", "deploy to prod")
	withBlockingTerminal(t)
	expectExit(t, 1, func() { runApprove([]string{"--root", root, id}) })

	if e := auditEntryFor(t, root, "approval:"+id); e == nil || e.Result != "refused" {
		t.Fatalf("expected refused audit entry for %s, got %+v", id, e)
	}
	store := governance.NewFileStore(root)
	rec, err := store.Get(id)
	if err != nil || rec.DecidedAt != nil {
		t.Fatalf("approval must remain undecided after refusal: %+v err=%v", rec, err)
	}
}

// TestApproveAlreadyDecidedExits3 locks the decided-state guard: approving an
// approval that already has a decision is a policy outcome (rc=3), never a
// re-decision. The pre-check reads the governance store directly and must
// fire BEFORE app.New (the test root has no index, so reaching app.New would
// be a different failure).
func TestApproveAlreadyDecidedExits3(t *testing.T) {
	root := newRoot(t)
	store := governance.NewFileStore(root)
	if err := store.AddPending(domain.Approval{ID: "apr-test", Status: "pending", TaskID: ""}); err != nil {
		t.Fatalf("AddPending: %v", err)
	}
	if _, err := store.Decide("apr-test", "cli-user", true, ""); err != nil {
		t.Fatalf("Decide: %v", err)
	}
	expectExit(t, 3, func() { runApprove([]string{"--root", root, "apr-test"}) })
}

// TestBlueprintDecisionWritesAuditChain verifies D8: a blueprint approve/
// reject decision (decideBlueprintApproval) is appended to the tamper-evident
// .kern/audit chain with the same entry shape governance.FileStore.recordAudit
// uses (Action approve/reject, Resource "approval:<id>", Policy "approval",
// Result approved/denied), the chain stays intact, and `kern audit` renders
// the decision exactly once — the render-layer synthesis in cmd_audit.go must
// dedupe against the chain entry rather than show a double row.
func TestBlueprintDecisionWritesAuditChain(t *testing.T) {
	for _, tc := range []struct {
		name     string
		approve  bool
		wantAct  string
		wantRes  string
		wantAppr bool
	}{
		{"approve", true, "approve", "approved", true},
		{"reject", false, "reject", "denied", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := newRoot(t)
			id := "apr-d8-" + tc.name
			if err := gates.NewStore(root).Create(gates.Request{
				ID:        id,
				Intent:    "deploy the release",
				Requester: "alice",
				CreatedAt: time.Now(),
			}); err != nil {
				t.Fatalf("blueprint Create: %v", err)
			}

			decided, err := decideBlueprintApproval(root, id, "qa-bot", tc.approve, "reviewed on call")
			if err != nil {
				t.Fatalf("decideBlueprintApproval: %v", err)
			}
			wantStatus := gates.StatusRejected
			if tc.approve {
				wantStatus = gates.StatusApproved
			}
			if decided.Status != wantStatus {
				t.Fatalf("decided status = %s, want %s", decided.Status, wantStatus)
			}

			// The decision must be in the tamper-evident chain.
			auditDir := filepath.Join(root, ".kern", "audit")
			l := governance.NewAuditLog().
				WithStore(storage.NewLog(auditDir)).
				WithLockPath(filepath.Join(auditDir, ".lock"))
			if _, err := l.Replay(); err != nil {
				t.Fatalf("Replay: %v", err)
			}
			var found *governance.AuditEntry
			for i := range l.All() {
				e := &l.All()[i]
				if e.Resource == "approval:"+id {
					found = e
				}
			}
			if found == nil {
				t.Fatalf("audit chain has no entry for %s; entries: %+v", id, l.All())
			}
			if found.Action != tc.wantAct || found.Result != tc.wantRes || found.Approved != tc.wantAppr {
				t.Errorf("chain entry = action=%s result=%s approved=%t, want %s/%s/%t",
					found.Action, found.Result, found.Approved, tc.wantAct, tc.wantRes, tc.wantAppr)
			}
			if found.AgentID != "qa-bot" || found.Policy != "approval" {
				t.Errorf("chain entry agent=%s policy=%s, want qa-bot/approval", found.AgentID, found.Policy)
			}
			if found.Timestamp.IsZero() {
				t.Error("chain entry has zero timestamp")
			}
			if brk, verified := l.VerifyChainReport(); verified != 1 || brk >= 0 {
				t.Errorf("chain integrity: verified=%d firstBroken=%d, want 1/-1", verified, brk)
			}

			// `kern audit` must show the decision exactly once (dedupe).
			out := captureStdout(t, func() { runAudit([]string{"--root", root}) })
			if n := strings.Count(out, "approval:"+id); n != 1 {
				t.Errorf("kern audit shows %q %d time(s), want exactly 1:\n%s", "approval:"+id, n, out)
			}
		})
	}
}

// TestJoinVerbPositionals (verb-trap fix, F-IM1/F-WI1/F-PL1 family): a
// leading change-verb across multiple positionals is an unquoted sentence —
// `kern impact remove WriteFileAtomic` previously parsed change="remove" and
// fuzzy-resolved the WRONG symbol. Quoted/explicit-kind forms pass through.
func TestJoinVerbPositionals(t *testing.T) {
	cases := []struct {
		in, want []string
	}{
		{[]string{"remove", "WriteFileAtomic"}, []string{"remove WriteFileAtomic"}},
		{[]string{"add", "a", "new", "caching", "layer", "to", "Fit"}, []string{"add a new caching layer to Fit"}},
		{[]string{"remove"}, []string{"remove"}},
		{[]string{"WriteFileAtomic"}, []string{"WriteFileAtomic"}},
		{[]string{"Client.Remove", "change_dependency", "NewTarget"}, nil},
		{[]string{"change", "the", "signature", "of", "Fit"}, []string{"change the signature of Fit"}},
	}
	for _, c := range cases {
		got := joinVerbPositionals(c.in)
		if c.want == nil {
			if len(got) != len(c.in) {
				t.Fatalf("joinVerbPositionals(%v) = %v, want untouched", c.in, got)
			}
			continue
		}
		if len(got) != 1 || got[0] != c.want[0] {
			t.Fatalf("joinVerbPositionals(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}
