package orgapprovals

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/governance"
)

// seedApproval creates + approves an org approval for the given scope and
// returns it.
func seedApproval(t *testing.T, root, action, resource string) Approval {
	t.Helper()
	a, err := Create(root, action, resource, "org-admin", "pre-approve "+action)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	approved, err := Approve(root, a.ID, "org-admin")
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	return approved
}

// TestStoreRoundTrip exercises the full lifecycle: create → list → approve →
// consume → used, and asserts the document persisted on disk at
// <org-root>/.kern/org-approvals.json.
func TestStoreRoundTrip(t *testing.T) {
	root := t.TempDir()

	a, err := Create(root, "deploy", "production", "org-admin", "multi-project deploy")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if a.ID == "" || a.Status != StatusPending || a.GrantedBy != "org-admin" {
		t.Fatalf("created approval = %+v, want pending orgappr-... by org-admin", a)
	}
	if !strings.HasPrefix(a.ID, "orgappr-") {
		t.Errorf("approval ID %q missing orgappr- prefix", a.ID)
	}

	if got := List(root); len(got) != 1 || got[0].ID != a.ID || got[0].Status != StatusPending {
		t.Fatalf("List after create = %+v, want the pending approval", got)
	}

	approved, err := Approve(root, a.ID, "org-admin")
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if approved.Status != StatusApproved || approved.DecidedAt == nil {
		t.Fatalf("approved = %+v, want approved with DecidedAt", approved)
	}

	consumed, ok, err := Consume(root, "deploy", "production")
	if err != nil || !ok {
		t.Fatalf("Consume: ok=%v err=%v", ok, err)
	}
	if consumed.ID != a.ID || consumed.Status != StatusUsed || consumed.ConsumedAt == nil {
		t.Fatalf("consumed = %+v, want the approved approval marked used", consumed)
	}

	// Single-use: a second consume finds nothing.
	if _, ok, err := Consume(root, "deploy", "production"); err != nil || ok {
		t.Fatalf("second Consume = ok=%v err=%v, want none", ok, err)
	}

	// Persisted on disk at the org root with the full lifecycle visible.
	data, err := os.ReadFile(OrgApprovalsPath(root))
	if err != nil {
		t.Fatalf("read store: %v", err)
	}
	body := string(data)
	for _, want := range []string{a.ID, StatusUsed, "deploy", "production", "org-admin"} {
		if !strings.Contains(body, want) {
			t.Errorf("persisted store missing %q", want)
		}
	}
}

// TestStoreFileMode0600 asserts the persisted org approvals store is
// owner-only, like the org policy and rbac stores.
func TestStoreFileMode0600(t *testing.T) {
	root := t.TempDir()
	if _, err := Create(root, "deploy", "production", "org-admin", "mode check"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	info, err := os.Stat(OrgApprovalsPath(root))
	if err != nil {
		t.Fatalf("stat store: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("store mode = %o, want 600", perm)
	}
}

// TestCorruptStoreFailsClosed asserts a corrupt store never yields a partial
// approval set: the display surface returns empty and every mutating path
// (including Consume) fails closed with an error instead of trusting or
// clobbering the unreadable file.
func TestCorruptStoreFailsClosed(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".kern"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(OrgApprovalsPath(root), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	if got := List(root); len(got) != 0 {
		t.Errorf("List on corrupt store = %+v, want empty (fail closed)", got)
	}
	if _, err := Create(root, "deploy", "production", "org-admin", "x"); err == nil {
		t.Error("Create on corrupt store should error (fail closed)")
	}
	if _, _, err := Consume(root, "deploy", "production"); err == nil {
		t.Error("Consume on corrupt store should error (fail closed)")
	}
	if _, err := Approve(root, "orgappr-whatever", "org-admin"); err == nil {
		t.Error("Approve on corrupt store should error (fail closed)")
	}
}

// TestAtomicSingleUseConsume races N concurrent consumers against one
// approved org approval and asserts EXACTLY ONE wins (the store's flock +
// per-path lock serialize the read-mark-persist critical section across
// goroutines; the race detector validates the memory model).
func TestAtomicSingleUseConsume(t *testing.T) {
	root := t.TempDir()
	seedApproval(t, root, "deploy", "production")

	const consumers = 16
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins := 0
	for i := 0; i < consumers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, ok, err := Consume(root, "deploy", "production"); err != nil {
				t.Errorf("Consume: %v", err)
			} else if ok {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("concurrent consumers: %d wins, want exactly 1 (single-use)", wins)
	}
	// And the consumed approval is observable as used on disk.
	approvals := List(root)
	if len(approvals) != 1 || approvals[0].Status != StatusUsed {
		t.Fatalf("after race: %+v, want the single approval marked used", approvals)
	}
}

// TestConsumeScopeMatching asserts Consume only matches approved approvals
// whose action AND resource match, and picks the OLDEST valid match.
func TestConsumeScopeMatching(t *testing.T) {
	root := t.TempDir()

	// A pending approval for the scope must NOT be consumable.
	if _, err := Create(root, "deploy", "production", "org-admin", "still pending"); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := Consume(root, "deploy", "production"); err != nil || ok {
		t.Fatalf("Consume matched a pending approval: ok=%v err=%v", ok, err)
	}

	// Wrong action/resource must not match.
	seedApproval(t, root, "deploy", "staging")
	seedApproval(t, root, "rollback", "production")
	if _, ok, err := Consume(root, "deploy", "production"); err != nil || ok {
		t.Fatalf("Consume matched a non-matching scope: ok=%v err=%v", ok, err)
	}

	// Oldest valid wins: create two for the same scope; consume must return
	// the earlier one.
	first := seedApproval(t, root, "deploy", "production")
	second := seedApproval(t, root, "deploy", "production")
	if !first.CreatedAt.Before(second.CreatedAt) {
		t.Fatalf("seed ordering broken: first %v >= second %v", first.CreatedAt, second.CreatedAt)
	}
	consumed, ok, err := Consume(root, "deploy", "production")
	if err != nil || !ok {
		t.Fatalf("Consume: ok=%v err=%v", ok, err)
	}
	if consumed.ID != first.ID {
		t.Errorf("Consume returned %s, want the oldest %s", consumed.ID, first.ID)
	}
	// The second remains consumable.
	if _, ok, err := Consume(root, "deploy", "production"); err != nil || !ok {
		t.Fatalf("second valid approval should still be consumable: ok=%v err=%v", ok, err)
	}
}

// TestNoOrgRootInert asserts the whole package is inert with no org root:
// Consume returns none with NO error (so the per-project approval path is
// byte-for-byte untouched), List is empty, and the write APIs refuse.
func TestNoOrgRootInert(t *testing.T) {
	if _, ok, err := Consume("", "deploy", "production"); ok || err != nil {
		t.Fatalf("Consume with no org root = ok=%v err=%v, want none with no error", ok, err)
	}
	if got := List(""); len(got) != 0 {
		t.Errorf("List with no org root = %+v, want empty", got)
	}
	if _, err := Create("", "deploy", "production", "org-admin", "x"); err == nil {
		t.Error("Create with no org root should refuse")
	}
	if _, err := Approve("", "orgappr-1", "org-admin"); err == nil {
		t.Error("Approve with no org root should refuse")
	}
	if _, err := Reject("", "orgappr-1", "org-admin", "x"); err == nil {
		t.Error("Reject with no org root should refuse")
	}
}

// TestAuditHookWired asserts SetAuditHook routes org approval events (create,
// approve, reject, consume) to the registered org audit writer.
func TestAuditHookWired(t *testing.T) {
	root := t.TempDir()
	var mu sync.Mutex
	var events []string
	SetAuditHook(func(e governance.AuditEntry) { mu.Lock(); events = append(events, e.Action); mu.Unlock() })
	t.Cleanup(func() { SetAuditHook(nil) })

	a, err := Create(root, "deploy", "production", "org-admin", "audited")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Approve(root, a.ID, "org-admin"); err != nil {
		t.Fatal(err)
	}
	rejected, err := Create(root, "deploy", "production", "org-admin", "will reject")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Reject(root, rejected.ID, "org-admin", "changed mind"); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := Consume(root, "deploy", "production"); err != nil || !ok {
		t.Fatalf("Consume of the approved approval: ok=%v err=%v, want a match", ok, err)
	}

	mu.Lock()
	defer mu.Unlock()
	want := []string{"create", "approve", "create", "reject", "consume"}
	if len(events) != len(want) {
		t.Fatalf("audit events = %v, want %v", events, want)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Errorf("audit event[%d] = %q, want %q", i, events[i], want[i])
		}
	}
}
