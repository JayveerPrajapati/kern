package governance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

func TestNewApprovalWorkflowEmpty(t *testing.T) {
	w := NewApprovalWorkflow()
	if got := w.Pending(); len(got) != 0 {
		t.Errorf("Pending() = %d entries, want 0", len(got))
	}
}

func TestRequestCreatesPending(t *testing.T) {
	w := NewApprovalWorkflow()
	a, _ := w.Request("task-1", "coder-1", "deploy to prod")
	if a.ID == "" {
		t.Error("approval should have an ID")
	}
	if !strings.HasPrefix(a.ID, "appr-") {
		t.Errorf("ID = %q, want appr- prefix", a.ID)
	}
	if a.TaskID != "task-1" || a.Requester != "coder-1" {
		t.Errorf("TaskID/Requester = %q/%q", a.TaskID, a.Requester)
	}
	if a.Status != "pending" {
		t.Errorf("Status = %q, want pending", a.Status)
	}
	if a.Reason != "deploy to prod" {
		t.Errorf("Reason = %q, want deploy to prod", a.Reason)
	}
	if a.RequestedAt.IsZero() {
		t.Error("RequestedAt should be set")
	}
}

func TestRequestIDsUnique(t *testing.T) {
	w := NewApprovalWorkflow()
	a1, _ := w.Request("t1", "c", "x")
	a2, _ := w.Request("t2", "c", "y")
	if a1.ID == a2.ID {
		t.Errorf("request IDs should be unique, both = %q", a1.ID)
	}
}

func TestApprove(t *testing.T) {
	w := NewApprovalWorkflow()
	a, _ := w.Request("task-1", "coder-1", "deploy")
	got, err := w.Approve(a.ID, "human-1")
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if got.Status != "approved" {
		t.Errorf("Status = %q, want approved", got.Status)
	}
	if got.Approver != "human-1" {
		t.Errorf("Approver = %q, want human-1", got.Approver)
	}
	if got.DecidedAt == nil {
		t.Error("DecidedAt should be set")
	}
	// Persisted: Get returns the updated state.
	got2, err := w.Get(a.ID)
	if err != nil {
		t.Fatalf("Get after approve: %v", err)
	}
	if got2.Status != "approved" {
		t.Errorf("stored Status = %q, want approved", got2.Status)
	}
}

func TestReject(t *testing.T) {
	w := NewApprovalWorkflow()
	a, _ := w.Request("task-1", "coder-1", "deploy")
	got, err := w.Reject(a.ID, "human-1", "not ready")
	if err != nil {
		t.Fatalf("Reject: %v", err)
	}
	if got.Status != "rejected" {
		t.Errorf("Status = %q, want rejected", got.Status)
	}
	if got.Approver != "human-1" {
		t.Errorf("Approver = %q, want human-1", got.Approver)
	}
	if got.Reason != "not ready" {
		t.Errorf("Reason = %q, want not ready", got.Reason)
	}
	if got.DecidedAt == nil {
		t.Error("DecidedAt should be set on rejection")
	}
}

func TestDecideUnknownErrors(t *testing.T) {
	w := NewApprovalWorkflow()
	if _, err := w.Approve("nope", "human"); err == nil {
		t.Error("Approve unknown should error")
	}
	if _, err := w.Reject("nope", "human", "x"); err == nil {
		t.Error("Reject unknown should error")
	}
	if _, err := w.Get("nope"); err == nil {
		t.Error("Get unknown should error")
	}
}

func TestDecideNonPendingErrors(t *testing.T) {
	w := NewApprovalWorkflow()
	a, _ := w.Request("task", "coder", "deploy")
	if _, err := w.Approve(a.ID, "human"); err != nil {
		t.Fatalf("first approve: %v", err)
	}
	// Double-approve must fail: the approval is no longer pending.
	if _, err := w.Approve(a.ID, "human2"); err == nil {
		t.Error("second approve on decided approval should error")
	}
	// Reject after approve must fail too.
	if _, err := w.Reject(a.ID, "human3", "x"); err == nil {
		t.Error("reject on decided approval should error")
	}
}

func TestPendingReturnsOnlyPending(t *testing.T) {
	w := NewApprovalWorkflow()
	a1, _ := w.Request("t1", "coder", "x")
	a2, _ := w.Request("t2", "coder", "y")
	a3, _ := w.Request("t3", "coder", "z")
	if _, err := w.Approve(a1.ID, "human"); err != nil {
		t.Fatalf("approve a1: %v", err)
	}
	if _, err := w.Reject(a3.ID, "human", "no"); err != nil {
		t.Fatalf("reject a3: %v", err)
	}
	pending := w.Pending()
	if len(pending) != 1 {
		t.Fatalf("Pending() = %d, want 1", len(pending))
	}
	if pending[0].ID != a2.ID {
		t.Errorf("pending ID = %q, want %q", pending[0].ID, a2.ID)
	}
	if pending[0].Status != "pending" {
		t.Errorf("pending status = %q, want pending", pending[0].Status)
	}
}

func TestPendingSortedByID(t *testing.T) {
	w := NewApprovalWorkflow()
	// Request IDs are random (appr-<hex>). Ensure output is ordered by ID
	// regardless of insertion order by requesting in a scrambled set.
	ids := make(map[string]bool)
	for i := 0; i < 5; i++ {
		a, _ := w.Request("t", "c", "")
		ids[a.ID] = true
	}
	pending := w.Pending()
	if len(pending) != 5 {
		t.Fatalf("Pending() = %d, want 5", len(pending))
	}
	for i := 1; i < len(pending); i++ {
		if pending[i].ID < pending[i-1].ID {
			t.Errorf("Pending() not sorted: %q after %q", pending[i].ID, pending[i-1].ID)
		}
	}
}

func TestRequiresApproval(t *testing.T) {
	cases := []struct {
		level domain.RiskLevel
		want  bool
	}{
		{domain.RiskLow, false},
		{domain.RiskMedium, false},
		{domain.RiskHigh, true},
		{domain.RiskCritical, true},
	}
	for _, c := range cases {
		if got := RequiresApproval(c.level); got != c.want {
			t.Errorf("RequiresApproval(%s) = %v, want %v", c.level, got, c.want)
		}
	}
}

func TestApproveRejectUnknown(t *testing.T) {
	w := NewApprovalWorkflow()
	if _, err := w.Approve("nope", "human"); err == nil {
		t.Error("Approve unknown should error")
	}
	if _, err := w.Reject("nope", "human", "x"); err == nil {
		t.Error("Reject unknown should error")
	}
	if _, err := w.Get("nope"); err == nil {
		t.Error("Get unknown should error")
	}
}

func TestDoubleDecideRejected(t *testing.T) {
	w := NewApprovalWorkflow()
	a, _ := w.Request("task-1", "coder", "deploy")
	if _, err := w.Approve(a.ID, "human"); err != nil {
		t.Fatalf("first approve: %v", err)
	}
	if _, err := w.Approve(a.ID, "human"); err == nil {
		t.Error("second approve on decided approval should error")
	}
	if _, err := w.Reject(a.ID, "human", "x"); err == nil {
		t.Error("reject on decided approval should error")
	}
}

// TestFallbackApprovalIDUniqueAndFormatted: the non-crypto fallback must
// produce distinct, correctly-shaped IDs across sequences (never a constant).
func TestFallbackApprovalIDUniqueAndFormatted(t *testing.T) {
	seen := map[string]bool{}
	for i := uint64(1); i <= 1000; i++ {
		id := fallbackApprovalID(i)
		if !strings.HasPrefix(id, "appr-") || len(id) != len("appr-")+16 {
			t.Fatalf("fallbackApprovalID(%d) = %q, want appr-<16 hex>", i, id)
		}
		if seen[id] {
			t.Fatalf("fallbackApprovalID(%d) collides with an earlier ID", i)
		}
		seen[id] = true
	}
}

// TestApproveRollsBackOnPersistFailure: when the backing store cannot
// persist a decision, the in-memory approval must stay pending (resumable),
// not half-decided. A14.
func TestApproveRollsBackOnPersistFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	root := t.TempDir()
	w := NewPersistedApprovalWorkflow(root)
	a, _ := w.Request("task-1", "coder-1", "deploy")
	storeDir := filepath.Join(root, ".kern")
	if err := os.Chmod(storeDir, 0o500); err != nil {
		t.Skipf("chmod: %v", err)
	}
	defer func() { _ = os.Chmod(storeDir, 0o700) }()

	if _, err := w.Approve(a.ID, "human-1"); err == nil {
		t.Fatal("Approve() = nil with an unwritable store, want error")
	}
	got := w.pending[a.ID]
	if got.Status != "pending" {
		t.Errorf("after failed Approve, Status = %q, want pending (rolled back)", got.Status)
	}
	if got.Approver != "" || got.DecidedAt != nil {
		t.Errorf("after failed Approve, Approver/DecidedAt not rolled back: %+v", got)
	}

	// Same rollback for Reject: status pending again, reason restored.
	if _, err := w.Reject(a.ID, "human-2", "not now"); err == nil {
		t.Fatal("Reject() = nil with an unwritable store, want error")
	}
	got = w.pending[a.ID]
	if got.Status != "pending" || got.Reason != "deploy" {
		t.Errorf("after failed Reject, Status/Reason = %q/%q, want pending/deploy", got.Status, got.Reason)
	}

	// Restore write access: the approval must still be resumable.
	if err := os.Chmod(storeDir, 0o700); err != nil {
		t.Fatalf("chmod restore: %v", err)
	}
	if _, err := w.Reject(a.ID, "human-2", "not now"); err != nil {
		t.Fatalf("Reject after restore: %v", err)
	}
	if w.pending[a.ID].Status != "rejected" {
		t.Errorf("after successful Reject, Status = %q, want rejected", w.pending[a.ID].Status)
	}
}

// TestRequestSurfacesPersistFailure: when the backing store cannot persist a
// new approval, Request/RequestWithBinding must surface the failure (not
// log-and-continue) so the caller can fail closed instead of parking an
// un-reviewable gate that no other process can see or approve. A13.
func TestRequestSurfacesPersistFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	root := t.TempDir()
	w := NewPersistedApprovalWorkflow(root)
	storeDir := filepath.Join(root, ".kern")
	if err := os.Chmod(storeDir, 0o500); err != nil {
		t.Skipf("chmod: %v", err)
	}
	defer func() { _ = os.Chmod(storeDir, 0o700) }()

	if _, err := w.Request("task-1", "coder-1", "deploy"); err == nil {
		t.Fatal("Request() = nil with an unwritable store, want persist error")
	}
	if _, err := w.RequestWithBinding("task-2", "coder-2", "deploy", domain.RiskHigh, nil, nil, ""); err == nil {
		t.Fatal("RequestWithBinding() = nil with an unwritable store, want persist error")
	}

	// Restore write access: requests succeed again.
	if err := os.Chmod(storeDir, 0o700); err != nil {
		t.Fatalf("chmod back: %v", err)
	}
	if _, err := w.Request("task-3", "coder-3", "deploy"); err != nil {
		t.Errorf("Request() after restoring write access: %v", err)
	}
}
