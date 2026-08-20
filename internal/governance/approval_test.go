package governance

import (
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
	if a.TaskID != "task-1" || a.Requester != "coder-1" {
		t.Errorf("TaskID/Requester = %q/%q", a.TaskID, a.Requester)
	}
	if a.Status != "pending" {
		t.Errorf("Status = %q, want pending", a.Status)
	}
	if a.RequestedAt.IsZero() {
		t.Error("RequestedAt should be set")
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
	if _, err := w.Get(a.ID); err != nil {
		t.Errorf("Get after approve: %v", err)
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
	if got.Reason != "not ready" {
		t.Errorf("Reason = %q, want not ready", got.Reason)
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
	a := w.Request("task-1", "coder", "deploy")
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

func TestPendingReturnsOnlyPending(t *testing.T) {
	w := NewApprovalWorkflow()
	a1 := w.Request("t1", "coder", "x")
	a2 := w.Request("t2", "coder", "y")
	if _, err := w.Approve(a1.ID, "human"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	pending := w.Pending()
	if len(pending) != 1 {
		t.Fatalf("Pending() = %d, want 1", len(pending))
	}
	if pending[0].ID != a2.ID {
		t.Errorf("pending ID = %q, want %q", pending[0].ID, a2.ID)
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
