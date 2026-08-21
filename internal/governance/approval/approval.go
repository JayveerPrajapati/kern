// Package approval provides the human-in-the-loop approval workflow used to
// gate HIGH/CRITICAL risk actions.
package approval

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// ApprovalWorkflow manages human-in-the-loop approvals for high-risk actions.
// Approvals live in memory; no persistence needed. The mutex guards the
// pending map against concurrent web requests (approve/reject/pending handlers
// each run in their own goroutine).
type ApprovalWorkflow struct {
	mu      sync.Mutex
	pending map[string]domain.Approval
}

// NewApprovalWorkflow creates a new approval workflow.
func NewApprovalWorkflow() *ApprovalWorkflow {
	return &ApprovalWorkflow{pending: map[string]domain.Approval{}}
}

// Request creates a pending approval for a task. The approval ID is a
// cryptographically random hex string so it cannot be guessed or enumerated,
// and the TaskID is carried through so callers can correlate decisions back to
// the originating action.
func (w *ApprovalWorkflow) Request(taskID, requester, reason string) domain.Approval {
	a := domain.Approval{
		ID:          randomApprovalID(),
		TaskID:      taskID,
		Requester:   requester,
		Status:      "pending",
		Reason:      reason,
		RequestedAt: time.Now(),
	}
	w.mu.Lock()
	w.pending[a.ID] = a
	w.mu.Unlock()
	return a
}

// RequestWithBinding creates a pending approval with the full Phase 9 binding
// context: the risk level that triggered the gate, the policy IDs that
// evaluated to that risk, evidence references supporting the assessment, and
// the artifact (e.g. ImpactReport) backing the approval. This makes the
// approval self-describing for audit — an auditor can reconstruct WHY the
// approval was requested and WHAT it authorized, not just that it was.
func (w *ApprovalWorkflow) RequestWithBinding(taskID, requester, reason string, riskLevel domain.RiskLevel, policyIDs, evidenceRefs []string, artifactID string) domain.Approval {
	a := w.Request(taskID, requester, reason)
	a.RiskLevel = riskLevel
	a.PolicyIDs = policyIDs
	a.EvidenceRefs = evidenceRefs
	a.ArtifactID = artifactID
	w.mu.Lock()
	w.pending[a.ID] = a
	w.mu.Unlock()
	return a
}

// Approve marks an approval as approved. It returns an error if the approval
// is unknown or not currently pending.
func (w *ApprovalWorkflow) Approve(approvalID, approver string) (domain.Approval, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	a, ok := w.pending[approvalID]
	if !ok {
		return domain.Approval{}, fmt.Errorf("governance: approval %q not found", approvalID)
	}
	if a.Status != "pending" {
		return a, fmt.Errorf("governance: approval %q is %s, not pending", approvalID, a.Status)
	}
	a.Status = "approved"
	a.Approver = approver
	now := time.Now()
	a.DecidedAt = &now
	w.pending[approvalID] = a
	return a, nil
}

// Reject marks an approval as rejected. It returns an error if the approval is
// unknown or not currently pending.
func (w *ApprovalWorkflow) Reject(approvalID, approver, reason string) (domain.Approval, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	a, ok := w.pending[approvalID]
	if !ok {
		return domain.Approval{}, fmt.Errorf("governance: approval %q not found", approvalID)
	}
	if a.Status != "pending" {
		return a, fmt.Errorf("governance: approval %q is %s, not pending", approvalID, a.Status)
	}
	a.Status = "rejected"
	a.Approver = approver
	a.Reason = reason
	now := time.Now()
	a.DecidedAt = &now
	w.pending[approvalID] = a
	return a, nil
}

// Get retrieves an approval by ID. It returns an error if the approval is
// unknown.
func (w *ApprovalWorkflow) Get(approvalID string) (domain.Approval, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	a, ok := w.pending[approvalID]
	if !ok {
		return domain.Approval{}, errors.New("governance: approval not found")
	}
	return a, nil
}

// Pending returns all approvals still in the "pending" state, ordered by ID.
func (w *ApprovalWorkflow) Pending() []domain.Approval {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []domain.Approval
	for id, a := range w.pending {
		if a.Status == "pending" {
			_ = id
			out = append(out, a)
		}
	}
	sortApprovalsByID(out)
	return out
}

// randomApprovalID returns a cryptographically random approval ID of the form
// "appr-<hex>", using 8 random bytes (16 hex chars). crypto/rand guarantees the
// ID is unpredictable, so pending approvals cannot be guessed or enumerated.
func randomApprovalID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand should never fail on supported platforms; fall back to
		// zero bytes (still a valid, unique-enough ID shape) rather than panic.
		return "appr-0000000000000000"
	}
	return "appr-" + hex.EncodeToString(b[:])
}

// RequiresApproval returns true if the given risk level needs human approval.
// HIGH and CRITICAL require approval; LOW and MEDIUM do not.
func RequiresApproval(level domain.RiskLevel) bool {
	switch level {
	case domain.RiskHigh, domain.RiskCritical:
		return true
	default:
		return false
	}
}

// sortApprovalsByID sorts approvals ascending by ID for deterministic output.
func sortApprovalsByID(apprs []domain.Approval) {
	for i := 1; i < len(apprs); i++ {
		for j := i; j > 0 && apprs[j].ID < apprs[j-1].ID; j-- {
			apprs[j], apprs[j-1] = apprs[j-1], apprs[j]
		}
	}
}
