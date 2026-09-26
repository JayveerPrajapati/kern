// Package orgapprovals implements Stage 3 of the org governance hardening
// (P13): org-wide, single-use approvals. Where the per-project approval flow
// (governance.FileStore at <root>/.kern/approvals.json) scopes an approval to
// one project root, an org approval pre-approves an action/resource scope
// (e.g. "deploy"/"production") across every project under an org root — the
// deliberate central override an org admin issues for a deployment that spans
// projects. The store persists at <org-root>/.kern/org-approvals.json with
// the same atomic discipline as the org policy and org rbac stores (flock +
// per-path lock + temp-file + atomic rename, owner-only 0o600, fail-closed
// corrupt read, missing = empty).
//
// The package is INERT without an org root: with no KERN_ORG_ROOT configured
// every API is a no-op/fail-closed (Consume returns none with no error, List
// returns empty, the write APIs refuse) and never interferes with the
// per-project approval path.
package orgapprovals

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync/atomic"
	"time"

	"github.com/JayveerPrajapati/kern/internal/cache"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/governance"
)

// orgApprovalsVersion is the current org approvals document schema version.
const orgApprovalsVersion = 1

// Org approval lifecycle statuses. A created approval is pending until an
// admin approves (making it consumable) or rejects it. Consume marks an
// approved approval used — single-use: it can never be consumed again.
const (
	StatusPending  = "pending"
	StatusApproved = "approved"
	StatusRejected = "rejected"
	StatusUsed     = "used"
)

// Approval is one org-wide approval: a deliberate, single-use central
// override for an action/resource scope (e.g. "deploy"/"production").
// GrantedBy is the org admin who issued it; Reason is the recorded
// justification; CreatedAt is the issue time. Status walks
// pending → approved → used (or rejected); DecidedAt/ConsumedAt stamp the
// transitions.
type Approval struct {
	ID         string     `json:"id"`
	Action     string     `json:"action"`
	Resource   string     `json:"resource"`
	GrantedBy  string     `json:"granted_by"`
	Reason     string     `json:"reason"`
	Status     string     `json:"status"`
	CreatedAt  time.Time  `json:"created_at"`
	DecidedAt  *time.Time `json:"decided_at,omitempty"`
	ConsumedAt *time.Time `json:"consumed_at,omitempty"`
}

// OrgApprovalsPath returns the org approvals store path at
// <org-root>/.kern/org-approvals.json — next to the org policy and org rbac
// stores under the standard "kern generated" gitignore section.
func OrgApprovalsPath(root string) string {
	return filepath.Join(root, ".kern", "org-approvals.json")
}

// doc is the persisted document shape (versioned so a future schema change
// can be detected and failed closed).
type doc struct {
	Version   int        `json:"version"`
	Approvals []Approval `json:"approvals"`
}

// loadLocked reads the store. A missing store or empty file is not an error
// (no org approvals yet); a CORRUPT store fails closed with an error instead
// of returning a partial list — a caller must never consume or display a
// half-read org approval set. Caller must hold the store locks when calling
// from a mutating path (a lock-free snapshot read is safe for List because
// the atomic rename guarantees a consistent file).
func loadLocked(root string) ([]Approval, error) {
	data, err := os.ReadFile(OrgApprovalsPath(root))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("orgapprovals: read org approvals store: %w", err)
	}
	if len(data) == 0 {
		return nil, nil
	}
	var d doc
	if err := json.Unmarshal(data, &d); err != nil {
		return nil, fmt.Errorf("orgapprovals: decode org approvals store: %w", err)
	}
	return d.Approvals, nil
}

// saveLocked is the lock-free write core. Caller must hold the store locks.
// It writes a unique temp file then renames it into place atomically,
// owner-only (0o600): org approvals reveal which actions an org admin
// pre-authorized org-wide; other local users must not read or modify them
// (same chmod discipline as the org policy and rbac stores).
func saveLocked(root string, approvals []Approval) error {
	path := OrgApprovalsPath(root)
	if approvals == nil {
		approvals = []Approval{}
	}
	data, err := json.MarshalIndent(doc{Version: orgApprovalsVersion, Approvals: approvals}, "", "  ")
	if err != nil {
		return fmt.Errorf("orgapprovals: encode org approvals store: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("orgapprovals: create org approvals store dir: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".org-approvals-tmp-*")
	if err != nil {
		return fmt.Errorf("orgapprovals: create org approvals store temp: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("orgapprovals: write org approvals store temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("orgapprovals: close org approvals store temp: %w", err)
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("orgapprovals: chmod org approvals store: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("orgapprovals: rename org approvals store: %w", err)
	}
	return nil
}

// withLocked runs fn under the org approvals store's process-wide per-path
// lock (internal/cache/keylock) AND cross-process flock (internal/cache/
// filelock) — the same write discipline as the org policy and rbac stores —
// so read-modify-write critical sections never interleave across goroutines
// or across kern processes (server, CLI, MCP) working on the same org root.
// fn receives the current approvals and returns the new set to persist; the
// write always runs (same policy as the sibling stores).
func withLocked(root string, fn func([]Approval) ([]Approval, error)) error {
	path := OrgApprovalsPath(root)
	fl, err := cache.LockFile(path)
	if err != nil {
		return fmt.Errorf("orgapprovals: lock org approvals store: %w", err)
	}
	defer fl.Unlock()
	pl := cache.PathLock(path)
	pl.Lock()
	defer pl.Unlock()

	approvals, err := loadLocked(root)
	if err != nil {
		return err
	}
	next, err := fn(approvals)
	if err != nil {
		return err
	}
	return saveLocked(root, next)
}

// Create records a new PENDING org approval for the action/resource scope,
// granted by grantedBy with the given justification. The approval only
// authorizes anything after an admin Approve makes it consumable — creating
// one is never auto-approving. An org root is required (org scope is
// strictly opt-in); with no org root the write is refused.
func Create(root, action, resource, grantedBy, reason string) (Approval, error) {
	if root == "" {
		return Approval{}, fmt.Errorf("orgapprovals: no org root configured (set %s)", governance.OrgRootEnv)
	}
	if action == "" || grantedBy == "" {
		return Approval{}, fmt.Errorf("orgapprovals: action and granted_by are required")
	}
	a := Approval{
		ID:        randomApprovalID(),
		Action:    action,
		Resource:  resource,
		GrantedBy: grantedBy,
		Reason:    reason,
		Status:    StatusPending,
		CreatedAt: time.Now().UTC(),
	}
	if err := withLocked(root, func(cur []Approval) ([]Approval, error) {
		return append(cur, a), nil
	}); err != nil {
		return Approval{}, err
	}
	recordAudit(governance.AuditEntry{
		AgentID:  grantedBy,
		Action:   "create",
		Resource: "approval",
		Risk:     domain.Risk{Level: domain.RiskMedium, Score: 0.5, Factors: []string{"org approval created"}},
		Approved: true,
		Result:   "allowed",
		Policy:   "org-approval",
		Reason:   fmt.Sprintf("org approval %s created for %s/%s: %s", a.ID, action, resource, reason),
	})
	return a, nil
}

// List returns the org approvals for display (oldest first). A missing or
// empty store is an empty list; a corrupt store fails closed to empty for
// display too — no approvals are shown, mirroring ListOrgRBACRoles — while
// every mutating path (Create/Approve/Reject/Consume) still fails closed
// with an error. With no org root the org scope is inactive: empty list.
func List(root string) []Approval {
	if root == "" {
		return []Approval{}
	}
	approvals, err := loadLocked(root)
	if err != nil {
		return []Approval{}
	}
	if approvals == nil {
		return []Approval{}
	}
	return approvals
}

// Approve makes a pending org approval consumable (status approved). Only an
// approved approval can be consumed by a deploy gate; an approval can only
// be decided once. With no org root the decision is refused.
func Approve(root, id, by string) (Approval, error) {
	if root == "" {
		return Approval{}, fmt.Errorf("orgapprovals: no org root configured (set %s)", governance.OrgRootEnv)
	}
	if id == "" || by == "" {
		return Approval{}, fmt.Errorf("orgapprovals: id and granted_by are required")
	}
	var decided Approval
	err := withLocked(root, func(cur []Approval) ([]Approval, error) {
		for i := range cur {
			if cur[i].ID != id {
				continue
			}
			if cur[i].Status != StatusPending {
				return nil, fmt.Errorf("orgapprovals: approval %s is %s, not pending", id, cur[i].Status)
			}
			now := time.Now().UTC()
			cur[i].Status = StatusApproved
			cur[i].DecidedAt = &now
			decided = cur[i]
			return cur, nil
		}
		return nil, fmt.Errorf("orgapprovals: approval %s not found", id)
	})
	if err != nil {
		return Approval{}, err
	}
	recordAudit(governance.AuditEntry{
		AgentID:  by,
		Action:   "approve",
		Resource: "approval",
		Risk:     domain.Risk{Level: domain.RiskMedium, Score: 0.5, Factors: []string{"org approval approved"}},
		Approved: true,
		Result:   "allowed",
		Policy:   "org-approval",
		Reason:   fmt.Sprintf("org approval %s approved by %s (%s/%s)", id, by, decided.Action, decided.Resource),
	})
	return decided, nil
}

// Reject denies a pending org approval (status rejected); a rejected
// approval can never be consumed. With no org root the decision is refused.
func Reject(root, id, by, reason string) (Approval, error) {
	if root == "" {
		return Approval{}, fmt.Errorf("orgapprovals: no org root configured (set %s)", governance.OrgRootEnv)
	}
	if id == "" || by == "" {
		return Approval{}, fmt.Errorf("orgapprovals: id and granted_by are required")
	}
	var decided Approval
	err := withLocked(root, func(cur []Approval) ([]Approval, error) {
		for i := range cur {
			if cur[i].ID != id {
				continue
			}
			if cur[i].Status != StatusPending {
				return nil, fmt.Errorf("orgapprovals: approval %s is %s, not pending", id, cur[i].Status)
			}
			now := time.Now().UTC()
			cur[i].Status = StatusRejected
			cur[i].DecidedAt = &now
			decided = cur[i]
			return cur, nil
		}
		return nil, fmt.Errorf("orgapprovals: approval %s not found", id)
	})
	if err != nil {
		return Approval{}, err
	}
	recordAudit(governance.AuditEntry{
		AgentID:  by,
		Action:   "reject",
		Resource: "approval",
		Risk:     domain.Risk{Level: domain.RiskMedium, Score: 0.5, Factors: []string{"org approval rejected"}},
		Approved: false,
		Result:   "denied",
		Policy:   "org-approval",
		Reason:   fmt.Sprintf("org approval %s rejected by %s: %s", id, by, reason),
	})
	return decided, nil
}

// Consume atomically consumes the oldest VALID org approval matching the
// action/resource scope: under the store's flock and per-path lock it finds
// the oldest approved, unused, matching approval, marks it used, persists,
// and returns it — so two concurrent deploys (across goroutines AND across
// processes) can never double-spend one org approval. Zero matches return
// none with no error. With no org root the package is inert: none, no error
// — the per-project approval path is never disturbed. A corrupt store fails
// closed WITH an error: the caller keeps the per-project gate authoritative
// rather than trusting an unreadable org override.
func Consume(root, action, resource string) (Approval, bool, error) {
	if root == "" {
		return Approval{}, false, nil
	}
	var (
		consumed Approval
		found    bool
	)
	err := withLocked(root, func(cur []Approval) ([]Approval, error) {
		var matches []int
		for i := range cur {
			if cur[i].Status == StatusApproved && cur[i].Action == action && cur[i].Resource == resource {
				matches = append(matches, i)
			}
		}
		if len(matches) == 0 {
			return cur, nil
		}
		// Oldest valid first (CreatedAt is set server-side and never
		// mutated, so ties are impossible in practice; the sort still
		// breaks them deterministically).
		sort.Slice(matches, func(i, j int) bool {
			return cur[matches[i]].CreatedAt.Before(cur[matches[j]].CreatedAt)
		})
		old := matches[0]
		now := time.Now().UTC()
		cur[old].Status = StatusUsed
		cur[old].ConsumedAt = &now
		consumed = cur[old]
		found = true
		return cur, nil
	})
	if err != nil {
		return Approval{}, false, err
	}
	if !found {
		return Approval{}, false, nil
	}
	recordAudit(governance.AuditEntry{
		AgentID:  consumed.GrantedBy,
		Action:   "consume",
		Resource: "approval",
		Risk:     domain.Risk{Level: domain.RiskMedium, Score: 0.5, Factors: []string{"org approval consumed"}},
		Approved: true,
		Result:   "allowed",
		Policy:   "org-approval",
		Reason:   fmt.Sprintf("org approval %s consumed (granted by %s) for %s/%s", consumed.ID, consumed.GrantedBy, action, resource),
	})
	return consumed, true, nil
}

// randomApprovalID returns a cryptographically random org approval ID
// ("orgappr-<16 hex>") so pending org approvals cannot be guessed or
// enumerated.
func randomApprovalID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand should never fail on supported platforms; fall back
		// to a time-seeded ID (unique enough, never a constant) rather than
		// panic.
		return fmt.Sprintf("orgappr-%016x", time.Now().UnixNano())
	}
	return "orgappr-" + hex.EncodeToString(b[:])
}

// AuditFunc writes one org audit entry. The enterprise server's
// governance.AuditLog.Record is the production writer (wired via
// SetAuditHook); tests and non-enterprise processes use the log fallback.
type AuditFunc func(governance.AuditEntry)

// auditHook is the process-wide org audit writer. Nil (never set) means no
// org audit trail is wired — org approval events fall back to the log. The
// enterprise server sets it at startup so every create/approve/reject/
// consume lands on the shared org audit log. atomic.Value keeps concurrent
// readers (Consume from a deploy gate) race-free with the startup write.
var auditHook atomic.Value // AuditFunc

// SetAuditHook registers the org audit trail writer (e.g. an enterprise
// server's shared org audit log); nil disables the hook. It is a startup
// configuration call — callers must not race it against approval traffic.
func SetAuditHook(f AuditFunc) {
	auditHook.Store(f)
}

// recordAudit writes an org approval event to the org audit trail when one
// is wired; otherwise it falls back to a log line so the event is never
// silently dropped.
func recordAudit(e governance.AuditEntry) {
	if f, ok := auditHook.Load().(AuditFunc); ok && f != nil {
		f(e)
		return
	}
	log.Printf("orgapprovals: audit: %s/%s %s: %s", e.Action, e.Resource, e.Result, e.Reason)
}
