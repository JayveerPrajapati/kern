package governance

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

func TestFileStoreAddPendingAndLoad(t *testing.T) {
	root := t.TempDir()
	s := NewFileStore(root)

	a := domain.Approval{
		ID:          "appr-1",
		TaskID:      "task-1",
		Requester:   "agent-a",
		Status:      "pending",
		Reason:      "high risk write",
		RequestedAt: time.Now(),
	}
	if err := s.AddPending(a); err != nil {
		t.Fatalf("AddPending: %v", err)
	}

	got, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 approval, got %d", len(got))
	}
	if got[0].ID != "appr-1" || got[0].TaskID != "task-1" {
		t.Fatalf("unexpected approval: %+v", got[0])
	}
}

func TestFileStoreDecideApprove(t *testing.T) {
	root := t.TempDir()
	s := NewFileStore(root)
	a := domain.Approval{ID: "appr-2", TaskID: "task-2", Status: "pending", RequestedAt: time.Now()}
	if err := s.AddPending(a); err != nil {
		t.Fatalf("AddPending: %v", err)
	}

	got, err := s.Decide("appr-2", "human-1", true, "")
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if got.Status != "approved" {
		t.Fatalf("want approved, got %q", got.Status)
	}
	if got.Approver != "human-1" {
		t.Fatalf("want approver human-1, got %q", got.Approver)
	}
	if got.DecidedAt == nil {
		t.Fatal("DecidedAt should be set")
	}

	// Persisted across reload.
	reloaded, err := s.Load()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(reloaded) != 1 || reloaded[0].Status != "approved" {
		t.Fatalf("reload mismatch: %+v", reloaded)
	}
}

func TestFileStoreDecideReject(t *testing.T) {
	root := t.TempDir()
	s := NewFileStore(root)
	a := domain.Approval{ID: "appr-3", TaskID: "task-3", Status: "pending", RequestedAt: time.Now()}
	if err := s.AddPending(a); err != nil {
		t.Fatalf("AddPending: %v", err)
	}

	got, err := s.Decide("appr-3", "human-2", false, "not approved")
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if got.Status != "rejected" {
		t.Fatalf("want rejected, got %q", got.Status)
	}
	if got.Reason != "not approved" {
		t.Fatalf("want reason carried through, got %q", got.Reason)
	}
}

func TestFileStorePending(t *testing.T) {
	root := t.TempDir()
	s := NewFileStore(root)

	old := domain.Approval{ID: "appr-old", TaskID: "t1", Status: "pending", RequestedAt: time.Now().Add(-time.Hour)}
	new := domain.Approval{ID: "appr-new", TaskID: "t2", Status: "pending", RequestedAt: time.Now()}
	done := domain.Approval{ID: "appr-done", TaskID: "t3", Status: "approved", RequestedAt: time.Now()}
	for _, a := range []domain.Approval{new, done, old} {
		if err := s.AddPending(a); err != nil {
			t.Fatalf("AddPending: %v", err)
		}
	}

	pending, err := s.Pending()
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if len(pending) != 2 {
		t.Fatalf("want 2 pending, got %d", len(pending))
	}
	// Sorted by RequestedAt ascending.
	if pending[0].ID != "appr-old" || pending[1].ID != "appr-new" {
		t.Fatalf("unexpected order: %+v", pending)
	}
}

func TestFileStoreGetNotFound(t *testing.T) {
	root := t.TempDir()
	s := NewFileStore(root)
	if _, err := s.Get("appr-nope"); err == nil {
		t.Fatal("want error for missing approval")
	}
}

// TestFileStoreDecisions covers the Decisions accessor the policy-signal
// learner reads: it returns only decided (approved/rejected) approvals,
// ordered deterministically by decision time then ID, and never pending ones.
func TestFileStoreDecisions(t *testing.T) {
	root := t.TempDir()
	s := NewFileStore(root)

	mustAdd := func(id string) {
		t.Helper()
		if err := s.AddPending(domain.Approval{ID: id, TaskID: "t", Status: "pending", RequestedAt: time.Now()}); err != nil {
			t.Fatalf("AddPending %s: %v", id, err)
		}
	}
	mustAdd("appr-pending")
	mustAdd("appr-early")
	mustAdd("appr-late")
	mustAdd("appr-mid")
	if _, err := s.Decide("appr-early", "human", true, ""); err != nil {
		t.Fatalf("Decide appr-early: %v", err)
	}
	if _, err := s.Decide("appr-mid", "human", false, "no"); err != nil {
		t.Fatalf("Decide appr-mid: %v", err)
	}
	if _, err := s.Decide("appr-late", "human", true, ""); err != nil {
		t.Fatalf("Decide appr-late: %v", err)
	}

	got, err := s.Decisions()
	if err != nil {
		t.Fatalf("Decisions: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("Decisions = %d, want 3 (pending excluded)", len(got))
	}
	// Decision-time ascending; the ID fallback on equal timestamps yields the
	// same expected order (early < late < mid lexically too).
	if got[0].ID != "appr-early" || got[1].ID != "appr-mid" || got[2].ID != "appr-late" {
		t.Errorf("Decisions order = %s,%s,%s, want early,mid,late", got[0].ID, got[1].ID, got[2].ID)
	}
	for _, a := range got {
		if a.Status != "approved" && a.Status != "rejected" {
			t.Errorf("Decisions returned %q status %q", a.ID, a.Status)
		}
	}
}

func TestFileStoreLoadMissingFile(t *testing.T) {
	root := filepath.Join(t.TempDir(), "does-not-exist")
	s := NewFileStore(root)
	got, err := s.Load()
	if err != nil {
		t.Fatalf("Load on missing file: %v", err)
	}
	if got != nil {
		t.Fatalf("want nil slice, got %+v", got)
	}

	// Ensure the dir is created even when loading.
	if _, err := os.Stat(filepath.Join(root, ".kern")); err != nil {
		t.Fatalf("expected .kern dir to be created: %v", err)
	}
}

// TestPersistedWorkflowRestoresPendingAfterRestart is the core restart
// scenario: an approval requested before a "restart" is still present and
// resolvable on a fresh workflow backed by the same file, and a decision made
// after the restart is visible to the next process too.
func TestPersistedWorkflowRestoresPendingAfterRestart(t *testing.T) {
	root := t.TempDir()
	w := NewPersistedApprovalWorkflow(root)
	a, err := w.RequestWithBinding("task-1", "agent-1", "deploy to prod",
		domain.RiskHigh, []string{"policy-1"}, []string{"ev-1"}, "art-1")
	if err != nil {
		t.Fatalf("RequestWithBinding: %v", err)
	}

	// A plain FileStore on the same root (what `kern approve` / the web UI
	// reads) must observe the workflow's write immediately.
	if _, err := NewFileStore(root).Get(a.ID); err != nil {
		t.Fatalf("FileStore.Get(%s) after Request: %v", a.ID, err)
	}

	// Simulate a restart: drop the workflow and build a fresh one on the same root.
	w2 := NewPersistedApprovalWorkflow(root)
	got, err := w2.Get(a.ID)
	if err != nil {
		t.Fatalf("Get after restart: %v", err)
	}
	if got.Status != "pending" {
		t.Errorf("status after restart = %q, want pending", got.Status)
	}
	if got.TaskID != "task-1" || got.Requester != "agent-1" {
		t.Errorf("TaskID/Requester after restart = %q/%q", got.TaskID, got.Requester)
	}
	// The full binding context must survive the restart too.
	if got.RiskLevel != domain.RiskHigh {
		t.Errorf("RiskLevel after restart = %q, want high", got.RiskLevel)
	}
	if len(got.PolicyIDs) != 1 || got.PolicyIDs[0] != "policy-1" {
		t.Errorf("PolicyIDs after restart = %v, want [policy-1]", got.PolicyIDs)
	}
	if got.ArtifactID != "art-1" {
		t.Errorf("ArtifactID after restart = %q, want art-1", got.ArtifactID)
	}

	// The pending approval is resolvable after restart.
	if _, err := w2.Approve(a.ID, "human-1"); err != nil {
		t.Fatalf("Approve after restart: %v", err)
	}
	// And the decision survives the next restart too.
	w3 := NewPersistedApprovalWorkflow(root)
	got3, err := w3.Get(a.ID)
	if err != nil {
		t.Fatalf("Get after approve+restart: %v", err)
	}
	if got3.Status != "approved" || got3.Approver != "human-1" {
		t.Errorf("after approve+restart = %+v, want approved by human-1", got3)
	}
}

// TestFileStoreRestoresAfterRestart covers the store-level restart: an
// approval written by one store instance is present and resolvable on a fresh
// instance pointing at the same root.
func TestFileStoreRestoresAfterRestart(t *testing.T) {
	root := t.TempDir()
	s := NewFileStore(root)
	a := domain.Approval{ID: "appr-restart", TaskID: "task-9", Requester: "agent", Status: "pending", Reason: "gate", RequestedAt: time.Now()}
	if err := s.AddPending(a); err != nil {
		t.Fatalf("AddPending: %v", err)
	}

	s2 := NewFileStore(root) // simulate restart
	got, err := s2.Get("appr-restart")
	if err != nil {
		t.Fatalf("Get after restart: %v", err)
	}
	if got.TaskID != "task-9" || got.Status != "pending" {
		t.Errorf("restored approval = %+v", got)
	}
	pending, err := s2.Pending()
	if err != nil {
		t.Fatalf("Pending after restart: %v", err)
	}
	if len(pending) != 1 || pending[0].ID != "appr-restart" {
		t.Errorf("pending after restart = %+v", pending)
	}
}

// TestFileStoreConcurrentWrites hammers one store from many goroutines: no
// writes may be lost and the file must remain valid JSON.
func TestFileStoreConcurrentWrites(t *testing.T) {
	root := t.TempDir()
	s := NewFileStore(root)
	const n = 60
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			a := domain.Approval{ID: fmt.Sprintf("appr-%03d", i), TaskID: "t", Status: "pending", RequestedAt: time.Now()}
			if err := s.AddPending(a); err != nil {
				t.Errorf("AddPending %s: %v", a.ID, err)
			}
		}(i)
	}
	wg.Wait()

	got, err := s.Load()
	if err != nil {
		t.Fatalf("Load after concurrent writes: %v", err)
	}
	if len(got) != n {
		t.Errorf("Load() = %d approvals, want %d (lost writes)", len(got), n)
	}
	seen := map[string]bool{}
	for _, a := range got {
		seen[a.ID] = true
	}
	for i := 0; i < n; i++ {
		if !seen[fmt.Sprintf("appr-%03d", i)] {
			t.Errorf("missing approval appr-%03d", i)
		}
	}
}

// TestFileStoreTwoInstancesNoLostWrites mirrors the taskstore cross-process
// pattern: two store instances pointing at the same file (as a server, CLI and
// MCP server would) must serialize their read-modify-write critical sections
// and keep every write and decision.
func TestFileStoreTwoInstancesNoLostWrites(t *testing.T) {
	root := t.TempDir()
	s1 := NewFileStore(root)
	s2 := NewFileStore(root)

	mustAdd := func(s *FileStore, id string) {
		t.Helper()
		if err := s.AddPending(domain.Approval{ID: id, TaskID: "t", Status: "pending", RequestedAt: time.Now()}); err != nil {
			t.Fatalf("AddPending %s: %v", id, err)
		}
	}
	// Interleave writes and decisions across the two instances.
	mustAdd(s1, "appr-a")
	mustAdd(s2, "appr-b")
	if _, err := s1.Decide("appr-a", "human", true, ""); err != nil {
		t.Fatalf("Decide appr-a: %v", err)
	}
	mustAdd(s1, "appr-c")
	mustAdd(s2, "appr-d")
	if _, err := s2.Decide("appr-b", "human", false, "nope"); err != nil {
		t.Fatalf("Decide appr-b: %v", err)
	}

	for _, id := range []string{"appr-a", "appr-b", "appr-c", "appr-d"} {
		if _, err := s1.Get(id); err != nil {
			t.Errorf("s1.Get(%s): %v (lost write)", id, err)
		}
		if _, err := s2.Get(id); err != nil {
			t.Errorf("s2.Get(%s): %v (lost write)", id, err)
		}
	}
	// Decisions written by one instance are observed by the other.
	got, err := s2.Get("appr-a")
	if err != nil || got.Status != "approved" {
		t.Errorf("appr-a decision not visible via s2: %+v, err=%v", got, err)
	}
	got, err = s1.Get("appr-b")
	if err != nil || got.Status != "rejected" {
		t.Errorf("appr-b decision not visible via s1: %+v, err=%v", got, err)
	}
}

// TestFileStorePrunesResolvedHistory verifies the file stays bounded: resolved
// approvals beyond maxResolvedApprovals are dropped on save, while pending
// approvals are never pruned.
func TestFileStorePrunesResolvedHistory(t *testing.T) {
	root := t.TempDir()
	s := NewFileStore(root)

	// Resolve far more approvals than the bound.
	for i := 0; i < maxResolvedApprovals+50; i++ {
		id := fmt.Sprintf("appr-%03d", i)
		if err := s.AddPending(domain.Approval{ID: id, TaskID: "t", Status: "pending", RequestedAt: time.Now()}); err != nil {
			t.Fatalf("AddPending %s: %v", id, err)
		}
		if _, err := s.Decide(id, "human", true, ""); err != nil {
			t.Fatalf("Decide %s: %v", id, err)
		}
	}
	got, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) > maxResolvedApprovals {
		t.Errorf("file holds %d resolved approvals, want <= %d (bounded history)", len(got), maxResolvedApprovals)
	}

	// Pending approvals are never pruned, even after many decisions.
	for _, id := range []string{"appr-pending-1", "appr-pending-2"} {
		if err := s.AddPending(domain.Approval{ID: id, TaskID: "t", Status: "pending", RequestedAt: time.Now()}); err != nil {
			t.Fatalf("AddPending %s: %v", id, err)
		}
	}
	got, err = s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	pending := 0
	for _, a := range got {
		if a.Status == "pending" {
			pending++
		}
	}
	if pending != 2 {
		t.Errorf("pending = %d, want 2 (pending must never be pruned)", pending)
	}
	if len(got) > maxResolvedApprovals+2 {
		t.Errorf("file holds %d approvals, want <= %d", len(got), maxResolvedApprovals+2)
	}
}

// TestPersistedWorkflowPrunesOnMutation ensures workflow-level mutations keep
// the file bounded too: resolving approvals through the workflow (not just the
// store) prunes old resolved history.
func TestPersistedWorkflowPrunesOnMutation(t *testing.T) {
	root := t.TempDir()
	w := NewPersistedApprovalWorkflow(root)

	var id string
	for i := 0; i < maxResolvedApprovals+20; i++ {
		a, _ := w.Request(fmt.Sprintf("task-%03d", i), "agent", "deploy")
		id = a.ID
		if _, err := w.Approve(a.ID, "human"); err != nil {
			t.Fatalf("Approve %s: %v", a.ID, err)
		}
	}
	// One more pending approval that must survive.
	p, _ := w.Request("task-pending", "agent", "deploy")

	got, err := NewFileStore(root).Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) > maxResolvedApprovals+1 {
		t.Errorf("file holds %d approvals, want <= %d", len(got), maxResolvedApprovals+1)
	}
	// The pending approval and the most recent decision are still present.
	if _, err := w.Get(p.ID); err != nil {
		t.Errorf("pending approval lost: %v", err)
	}
	if _, err := w.Get(id); err != nil {
		t.Errorf("most recent resolved approval lost: %v", err)
	}
}

// TestNewFileStoreCorruptFileFailsClosed guards the A1 fix: a corrupt
// approvals.json must NOT be silently treated as an empty store (which a
// later save would overwrite, destroying the pending approvals it holds).
// The construction-time load error is surfaced via LoadError and every
// operation fails closed until the file is repaired.
func TestNewFileStoreCorruptFileFailsClosed(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".kern", "approvals.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"not":"an array"`), 0o644); err != nil {
		t.Fatal(err)
	}

	s := NewFileStore(root)
	if err := s.LoadError(); err == nil {
		t.Fatal("want LoadError to report the corrupt file")
	}
	// Reads must fail closed, not return an empty list.
	if _, err := s.Load(); err == nil {
		t.Fatal("want Load to fail on a corrupt file")
	}
	if _, err := s.Pending(); err == nil {
		t.Fatal("want Pending to fail on a corrupt file")
	}
	// Writes must fail closed so the corrupt file is not overwritten.
	if err := s.AddPending(domain.Approval{ID: "appr-x"}); err == nil {
		t.Fatal("want AddPending to fail on a corrupt file")
	}

	// Repairing the file restores operation (the error is advisory, every op
	// re-reads the file).
	if err := os.WriteFile(path, []byte(`[]`), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load()
	if err != nil {
		t.Fatalf("Load after repair: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want empty store after repair, got %+v", got)
	}
}

// TestFileStoreSavePerms locks audit A2: the approvals file must be written
// owner-only (0o600) and the approvals directory created 0o700, so other
// local users cannot read pending approvals, gated task keys, or approvers.
func TestFileStoreSavePerms(t *testing.T) {
	root := t.TempDir()
	s := NewFileStore(root)
	if err := s.AddPending(domain.Approval{ID: "appr-perm", TaskID: "task-perm", Status: "pending", RequestedAt: time.Now()}); err != nil {
		t.Fatalf("AddPending: %v", err)
	}

	fi, err := os.Stat(filepath.Join(root, ".kern", "approvals.json"))
	if err != nil {
		t.Fatalf("stat approvals.json: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("approvals.json mode = %o, want 0600", perm)
	}
	di, err := os.Stat(filepath.Join(root, ".kern"))
	if err != nil {
		t.Fatalf("stat .kern dir: %v", err)
	}
	if perm := di.Mode().Perm(); perm != 0o700 {
		t.Errorf(".kern dir mode = %o, want 0700", perm)
	}
}
