package approvalcli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	kctx "github.com/JayveerPrajapati/kern/internal/context"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/storage"
)

// Shared test helpers for the approval/governance family, moved from the
// cmd/kern test files with the implementation. The exported Run* entrypoints
// recover the package's internal exitError sentinel and return a plain int,
// so exit-code assertions go through expectCode (the bpcli/cli contract the
// dispatch table sees) rather than the cmd/kern panic-based expectExit.

// newRoot returns an isolated project root for command tests: XDG_CACHE_HOME
// is redirected so stores keyed by project root stay private to this test.
func newRoot(t *testing.T) string {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	return t.TempDir()
}

// expectCode asserts the exit code returned by an exported Run* entrypoint.
func expectCode(t *testing.T, want int, fn func() int) {
	t.Helper()
	if got := fn(); got != want {
		t.Fatalf("exit = %d, want %d", got, want)
	}
}

// captureStdout runs fn with os.Stdout redirected to a pipe and returns the
// captured output, so Run* helpers can be asserted on without polluting
// test output.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	defer func() { os.Stdout = old }()
	fn()
	_ = w.Close()
	os.Stdout = old
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// captureStderr runs fn with os.Stderr redirected to a pipe and returns the
// captured output (stdlib log writes there by default).
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	defer func() { os.Stderr = old }()
	fn()
	_ = w.Close()
	os.Stderr = old
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// captureStderrCode runs a Run* entrypoint with os.Stderr redirected to a
// pipe, returning the captured stderr and the returned exit code — the
// code-returning sibling of the cmd/kern captureStderrExit helper.
func captureStderrCode(t *testing.T, fn func() int) (string, int) {
	t.Helper()
	var code int
	stderr := captureStderr(t, func() { code = fn() })
	return stderr, code
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

// seedPendingApproval writes a pending approval into <root>/.kern/approvals.json
// — the store `kern approval wait` polls.
func seedPendingApproval(t *testing.T, root, id string) {
	t.Helper()
	if err := governance.NewFileStore(root).AddPending(domain.Approval{
		ID:     id,
		TaskID: "t-wait",
		Status: "pending",
	}); err != nil {
		t.Fatalf("AddPending: %v", err)
	}
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
// fail-closed timeout path is testable without a 30s wait.
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

// assertAuditEntry verifies the tamper-evident .kern/audit chain carries a
// governance-shaped decision entry for the given approval (same shape
// FileStore.recordAudit uses).
func assertAuditEntry(t *testing.T, root, id, wantAct, wantRes string, wantAppr bool, wantAgent string) {
	t.Helper()
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
	if found.Action != wantAct || found.Result != wantRes || found.Approved != wantAppr {
		t.Errorf("chain entry = action=%s result=%s approved=%t, want %s/%s/%t",
			found.Action, found.Result, found.Approved, wantAct, wantRes, wantAppr)
	}
	if found.AgentID != wantAgent || found.Policy != "approval" {
		t.Errorf("chain entry agent=%s policy=%s, want %s/approval", found.AgentID, found.Policy, wantAgent)
	}
	if found.Timestamp.IsZero() {
		t.Error("chain entry has zero timestamp")
	}
	if brk, verified := l.VerifyChainReport(); verified != 1 || brk >= 0 {
		t.Errorf("chain integrity: verified=%d firstBroken=%d, want 1/-1", verified, brk)
	}
}

// appendAuditEntryJSON writes the entry map as an AuditEntry JSON file and
// runs `audit append --root <root> --file <path>` in-process, capturing
// stdout and stderr.
func appendAuditEntryJSON(t *testing.T, root string, entry map[string]any) (string, string) {
	t.Helper()
	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "entry.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr string
	stdout = captureStdout(t, func() {
		stderr = captureStderr(t, func() {
			runAuditAppend([]string{"--root", root, "--file", path})
		})
	})
	return stdout, stderr
}

// stubAuditInvalidate replaces the auditInvalidate seam with a recording
// stub and returns pointers to what was observed plus the restore function.
// called reports whether the seam was invoked at all.
func stubAuditInvalidate() (entities *[]string, reason, source *string, at *time.Time, called *bool, restore func()) {
	var es []string
	var r, s string
	var t time.Time
	var invoked bool
	orig := auditInvalidate
	auditInvalidate = func(in []string, reasonIn, sourceIn string, atIn time.Time) []kctx.InvalidationMarker {
		invoked = true
		es, r, s, t = in, reasonIn, sourceIn, atIn
		return make([]kctx.InvalidationMarker, len(in))
	}
	return &es, &r, &s, &t, &invoked, func() { auditInvalidate = orig }
}

// corruptAuditHash rewrites a stored entry with a bogus hash to simulate a
// broken chain link.
func corruptAuditHash(t *testing.T, store storage.Store, key string) error {
	t.Helper()
	raw, err := store.Get(context.Background(), key)
	if err != nil {
		return err
	}
	var ent governance.AuditEntry
	if err := json.Unmarshal(raw, &ent); err != nil {
		return err
	}
	ent.Hash = strings.Repeat("0", 64)
	data, err := json.Marshal(ent)
	if err != nil {
		return err
	}
	return store.Put(context.Background(), key, data)
}

// policyFile writes the given policy-set JSON (array or envelope) to a temp
// file and returns its path.
func policyFile(t *testing.T, policiesJSON string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "policies.json")
	if err := os.WriteFile(p, []byte(policiesJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}
