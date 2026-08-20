package approval

import (
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
	a := w.Request("task-1", "coder-1", "deploy to prod")
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
	a1 := w.Request("t1", "c", "x")
	a2 := w.Request("t2", "c", "y")
	if a1.ID == a2.ID {
		t.Errorf("request IDs should be unique, both = %q", a1.ID)
	}
}

func TestApprove(t *testing.T) {
	w := NewApprovalWorkflow()
	a := w.Request("task-1", "coder-1", "deploy")
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
	a := w.Request("task-1", "coder-1", "deploy")
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
	a := w.Request("task", "coder", "deploy")
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
	a1 := w.Request("t1", "coder", "x")
	a2 := w.Request("t2", "coder", "y")
	a3 := w.Request("t3", "coder", "z")
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
		ids[w.Request("t", "c", "").ID] = true
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