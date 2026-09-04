package approval

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	return NewStore(t.TempDir())
}

func pendingRequest(id string) Request {
	return Request{
		ID:        id,
		RepoRoot:  "/tmp/repo",
		Intent:    "update boundaries",
		RiskLevel: "high",
		Requester: "agent-1",
		Files:     []string{".kern/boundaries.json"},
		CreatedAt: time.Now(),
	}
}

func TestCreateAndGet(t *testing.T) {
	s := newTestStore(t)
	if err := s.Create(pendingRequest("apr-1")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := s.Get("apr-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != StatusPending {
		t.Errorf("Status = %q, want pending", got.Status)
	}
	if got.Requester != "agent-1" || got.RiskLevel != "high" {
		t.Errorf("record fields lost: %+v", got)
	}
	// The log file exists under .blueprint/approvals/.
	if _, err := os.Stat(filepath.Join(filepath.Dir(s.Path()), "requests.jsonl")); err != nil {
		t.Errorf("requests.jsonl missing: %v", err)
	}
}

func TestCreateForcesPending(t *testing.T) {
	s := newTestStore(t)
	req := pendingRequest("apr-2")
	req.Status = StatusApproved // caller must not inject approval
	if err := s.Create(req); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, _ := s.Get("apr-2")
	if got.Status != StatusPending {
		t.Errorf("Status = %q, want forced pending", got.Status)
	}
}

func TestCreateEmptyID(t *testing.T) {
	s := newTestStore(t)
	if err := s.Create(pendingRequest("")); err == nil {
		t.Fatal("Create with empty id must error")
	}
}

func TestApproveLifecycle(t *testing.T) {
	s := newTestStore(t)
	if err := s.Create(pendingRequest("apr-3")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := s.Approve("apr-3", "alice@corp", "looks good"); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	got, err := s.Get("apr-3")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != StatusApproved {
		t.Errorf("Status = %q, want approved", got.Status)
	}
	if got.Approver != "alice@corp" {
		t.Errorf("Approver = %q, want alice@corp", got.Approver)
	}
	if got.DecidedAt == nil {
		t.Error("DecidedAt must be set")
	}
	if got.Reason != "looks good" {
		t.Errorf("Reason = %q, want looks good", got.Reason)
	}
}

func TestReject(t *testing.T) {
	s := newTestStore(t)
	if err := s.Create(pendingRequest("apr-4")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := s.Reject("apr-4", "bob@corp", "not now"); err != nil {
		t.Fatalf("Reject: %v", err)
	}
	got, _ := s.Get("apr-4")
	if got.Status != StatusRejected {
		t.Errorf("Status = %q, want rejected", got.Status)
	}
	if got.Approver != "bob@corp" {
		t.Errorf("Approver = %q, want bob@corp", got.Approver)
	}
}

func TestGetNotFound(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Get("nope"); err == nil {
		t.Fatal("Get for unknown id must error")
	}
}

func TestAlreadyDecided(t *testing.T) {
	s := newTestStore(t)
	if err := s.Create(pendingRequest("apr-5")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := s.Approve("apr-5", "alice@corp", ""); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if err := s.Approve("apr-5", "carol@corp", ""); err == nil {
		t.Fatal("second Approve on decided request must error")
	}
	if err := s.Reject("apr-5", "carol@corp", ""); err == nil {
		t.Fatal("Reject on approved request must error")
	}
}

func TestApproveUnknown(t *testing.T) {
	s := newTestStore(t)
	if err := s.Approve("ghost", "alice@corp", ""); err == nil {
		t.Fatal("Approve for unknown id must error")
	}
}

func TestApproveEmptyApprover(t *testing.T) {
	s := newTestStore(t)
	if err := s.Create(pendingRequest("apr-6")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := s.Approve("apr-6", "", ""); err == nil {
		t.Fatal("Approve without approver identity must error")
	}
}

func TestListFilters(t *testing.T) {
	s := newTestStore(t)
	if err := s.Create(pendingRequest("apr-a")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := s.Create(pendingRequest("apr-b")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := s.Create(pendingRequest("apr-c")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := s.Approve("apr-a", "alice@corp", ""); err != nil {
		t.Fatalf("Approve: %v", err)
	}

	all, err := s.List("")
	if err != nil {
		t.Fatalf("List(all): %v", err)
	}
	if len(all) != 3 {
		t.Errorf("List(all) len = %d, want 3", len(all))
	}

	approved, _ := s.List(StatusApproved)
	if len(approved) != 1 || approved[0].ID != "apr-a" {
		t.Errorf("List(approved) = %+v, want [apr-a]", approved)
	}
	pending, _ := s.List(StatusPending)
	if len(pending) != 2 {
		t.Errorf("List(pending) len = %d, want 2", len(pending))
	}
}

func TestListEmptyStore(t *testing.T) {
	s := newTestStore(t)
	all, err := s.List("")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 0 {
		t.Errorf("List on empty store = %d records, want 0", len(all))
	}
}

func TestLatestRecordWins(t *testing.T) {
	s := newTestStore(t)
	if err := s.Create(pendingRequest("apr-dup")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := s.Create(pendingRequest("apr-dup")); err != nil {
		t.Fatalf("Create (duplicate id): %v", err)
	}
	got, _ := s.Get("apr-dup")
	// The latest record wins, so both records are pending — the ID must still
	// resolve without error.
	if got.Status != StatusPending {
		t.Errorf("Status = %q, want pending", got.Status)
	}
}

func TestCorruptLineErrors(t *testing.T) {
	s := newTestStore(t)
	if err := s.Create(pendingRequest("apr-x")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Append a corrupt line directly.
	f, err := os.OpenFile(s.Path(), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := f.WriteString("{not-json}\n"); err != nil {
		t.Fatalf("write: %v", err)
	}
	f.Close()
	if _, err := s.Get("apr-x"); err == nil || !strings.Contains(err.Error(), s.Path()) {
		t.Fatalf("Get over corrupt log must surface the store error naming the log, got %v", err)
	}
}
