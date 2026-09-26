package main

import (
	"fmt"
	stdlog "log"
	"path/filepath"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/app"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/gates"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/storage"
)

// runApprove implements `kern approve [id] [--reject --reason "..." --approver "..."]`.
// With no args, lists pending approvals. With an ID, approves it.
// Use --reject to reject instead of approve.
func runApprove(rest []string) {
	f, args := parseFlagsOrDie(rest)
	root := projectRoot(f)

	if len(args) < 1 || args[0] == "" {
		fatalUsage("approve requires an approval id: kern approve <id> [--approver NAME] [--reason ...] — use 'kern approve list' to see pending approvals")
	}

	// `kern approve list` shows the pending-approval queue. (QA F2: bare
	// `kern approve` used to fall through to this listing and exit 0, which
	// read as success to scripts; the id requirement now matches reject's.)
	if args[0] == "list" {
		pending, err := governance.NewFileStore(root).Pending()
		if err != nil {
			fatal("approve: %v", err)
		}
		if len(pending) == 0 {
			fmt.Println("no pending approvals")
			return
		}
		fmt.Printf("%-20s %-12s %-20s %s\n", "ID", "TASK", "REQUESTER", "REASON")
		for _, a := range pending {
			reason := a.Reason
			if len(reason) > 40 {
				reason = reason[:37] + "..."
			}
			fmt.Printf("%-20s %-12s %-20s %s\n", a.ID, a.TaskID, a.Requester, reason)
			// Gate-1 attempt-2: the human must see exactly what they are
			// approving. Exec approvals carry the command text in
			// EvidenceRefs; a record whose integrity does not verify is
			// flagged instead of being shown with possibly tampered fields.
			if !governance.ApprovalIntegrityOK(a) {
				fmt.Printf("%54s !! INTEGRITY: record was modified outside the approval workflow — DO NOT APPROVE\n", "")
			}
			for _, ref := range a.EvidenceRefs {
				r := ref
				if len(r) > 240 {
					r = r[:237] + "..."
				}
				fmt.Printf("%54s evidence: %s\n", "", r)
			}
		}
		return
	}

	id := args[0]
	approver := f.approver
	if approver == "" {
		approver = "cli-user"
	}

	// Resolve the id against the same two approval stores decideApproval
	// serves — governance first (via the app layer), then the blueprint
	// two-person-rule store — and record the decision. The decided-state
	// guard (rc=3) and the store order are shared with `kern reject`, so a
	// single command surface can decide every approval `kern approve`
	// resolves.
	a, req := decideApproval(root, "approve", id, approver, !f.reject, f.reason)
	if req != nil {
		printBlueprintDecision(req, approver, f.reject)
		return
	}
	if f.reject {
		fmt.Printf("rejected: %s (by %s)\n", a.ID, approver)
		if a.TaskID != "" {
			fmt.Printf("  task: %s marked REJECTED\n", a.TaskID)
		}
		return
	}
	fmt.Printf("approved: %s\n", a.ID)
	fmt.Printf("  task: %s\n", a.TaskID)
	fmt.Printf("  approver: %s\n", a.Approver)
	fmt.Printf("  decided: %s\n", a.DecidedAt.Format(time.RFC3339))
	for _, ref := range a.EvidenceRefs {
		r := ref
		if len(r) > 240 {
			r = r[:237] + "..."
		}
		fmt.Printf("  evidence: %s\n", r)
	}
	if a.TaskID != "" {
		fmt.Printf("  resume: kern workflow --task %s\n", a.TaskID)
	}
}

// decideApproval resolves an approval id against the SAME two stores
// runApprove's decision path serves and records the human decision, in this
// order:
//
//  1. the governance approval store (<root>/.kern/approvals.json) through the
//     app layer — ResolveApprovalForTask also advances a task parked at
//     WAITING_FOR_APPROVAL to its approval-resolved state (so `kern task`
//     reflects the decision) and records the decision in the tamper-evident
//     audit chain. The workflow engine still resumes and drives the
//     remaining steps.
//  2. the blueprint two-person-rule store (.blueprint/approvals/, written by
//     `kern request-approval`, apr-* ids) via decideBlueprintApproval, which
//     mirrors the governance audit entry (D8).
//
// verb names the command for error messages ("approve"/"reject") and approve
// the decision polarity. The decided-state guard is shared: an approval that
// already carries a decision is a policy outcome (rc=3), never a
// re-decision. When neither store resolves the id the command fails loudly
// (rc=1) — an approval `kern approve` lists must never be undecidable, and
// an unknown id must never silently succeed.
//
// Returns the governance approval when the governance store decided, or the
// blueprint request when the blueprint store decided (the caller renders the
// outcome).
func decideApproval(root, verb, id, approver string, approve bool, reason string) (domain.Approval, *gates.Request) {
	// Decided-state guard: an approval that already has a decision must not
	// be re-decided (exitcode convention rc=3). Read-only pre-check; the
	// engine flows keep their idempotent Decide semantics.
	if store := governance.NewFileStore(root); store.LoadError() == nil {
		if existing, gerr := store.Get(id); gerr == nil && existing.DecidedAt != nil {
			fatalPolicy("%s: approval %q already decided as %s — check kern audit %s for state", verb, id, existing.Status, id)
		}
	}

	// Resolve through the app layer: the decision is persisted to the shared
	// approval store AND, when it gates a task parked at WAITING_FOR_APPROVAL,
	// the task is advanced to its approval-resolved state (so `kern task`
	// reflects the decision) and recorded in the audit chain.
	p, err := app.New(root)
	if err != nil {
		fatal("%s: could not load project: %v — run kern index first", verb, err)
	}
	ts := app.NewTaskService(p, nil).WithAgentID(approver)

	a, err := ts.ResolveApprovalForTask(id, approver, approve, reason)
	if err != nil {
		// Governance store miss: the id may belong to the blueprint
		// two-person-rule store that `kern request-approval` writes
		// (apr-* ids under .blueprint/approvals/). Resolve it there so
		// a single command surface can decide both request kinds instead
		// of forcing users to hunt for a different binary.
		if strings.Contains(err.Error(), "approval not found") {
			if req, aerr := decideBlueprintApproval(root, id, approver, approve, reason); aerr == nil {
				return domain.Approval{}, req
			} else if strings.Contains(aerr.Error(), "already decided") {
				fatalPolicy("%s: %v", verb, aerr)
			}
			// Found in neither store: fail loudly (rc=1) — never silently
			// succeed.
			fatal("%s: approval %s not found — check kern audit %s for state", verb, id, id)
		}
		fatal("%s: %v — check kern audit %s for state", verb, err, id)
	}
	// Self-improvement (use-cases Tier 3 #7): the human decision is now
	// recorded — learn from the approval log which actions always get
	// approved vs which are risky. Best-effort and non-blocking: learning
	// proposes typed-claim memories (RECOMMENDATION / INFERENCE) only and
	// never changes policy; a failure here is logged and ignored.
	if _, lerr := app.RecordPolicySignals(governance.NewFileStore(root), p.Memory(), app.DefaultPolicySignalThreshold); lerr != nil {
		stdlog.Printf("kern %s: policy signal learning skipped: %v", verb, lerr)
	}
	return a, nil
}

// decideBlueprintApproval resolves a request in the blueprint approval store
// (written by `kern request-approval`) and records the human decision.
func decideBlueprintApproval(root, id, approver string, approve bool, reason string) (*gates.Request, error) {
	store := gates.NewStore(root)
	cur, err := store.Get(id)
	if err != nil {
		return nil, err
	}
	if cur.Status != gates.StatusPending {
		return nil, fmt.Errorf("request %s already decided as %s", id, cur.Status)
	}
	if approve {
		if err := store.Approve(id, approver, reason); err != nil {
			return nil, err
		}
	} else {
		if err := store.Reject(id, approver, reason); err != nil {
			return nil, err
		}
	}
	// D8: the blueprint store records the decision in requests.jsonl, but
	// the tamper-evident .kern/audit chain never carried it, so `kern audit`
	// had to re-derive blueprint decisions at render time. Mirror
	// governance.FileStore.recordAudit exactly: same entry shape, same
	// best-effort semantics (a failed audit write never fails the decision —
	// the decision is already persisted; a missing chain entry is detectable
	// via `kern audit repair`).
	recordBlueprintAuditDecision(root, id, approver, approve, reason)
	return store.Get(id)
}

// recordBlueprintAuditDecision appends a human approve/reject decision on a
// blueprint approval request to the project's tamper-evident governance
// chain (.kern/audit). The entry shape mirrors governance.FileStore.recordAudit
// (Action approve/reject, Resource "approval:<id>", Policy "approval",
// Result approved/denied) so `kern audit` and the render-layer dedupe in
// cmd_audit.go see exactly the same rows a task-gated approval produces.
func recordBlueprintAuditDecision(root, id, approver string, approve bool, reason string) {
	action := "reject"
	result := "denied"
	if approve {
		action = "approve"
		result = "approved"
	}
	if reason == "" {
		reason = fmt.Sprintf("%s by %s", result, approver)
	}
	entry := governance.AuditEntry{
		AgentID:   approver,
		Action:    action,
		Resource:  "approval:" + id,
		Approved:  approve,
		Result:    result,
		Policy:    "approval",
		Reason:    reason,
		Timestamp: time.Now(),
	}
	auditDir := filepath.Join(root, ".kern", "audit")
	l := governance.NewAuditLog().
		WithStore(storage.NewLog(auditDir)).
		WithLockPath(filepath.Join(auditDir, ".lock"))
	if err := l.AppendExternal(entry); err != nil {
		// Loud, non-blocking — same contract as FileStore.recordAudit.
		stdlog.Printf("kern approve: approval %s %s by %s NOT recorded in audit chain: %v", id, result, approver, err)
	}
}

// printBlueprintDecision renders the outcome of a blueprint-store decision.
func printBlueprintDecision(a *gates.Request, approver string, rejected bool) {
	if rejected {
		fmt.Printf("rejected: %s (by %s)\n", a.ID, approver)
		return
	}
	fmt.Printf("approved: %s\n", a.ID)
	fmt.Printf("  request: %s\n", a.Intent)
	fmt.Printf("  risk: %s\n", a.RiskLevel)
	fmt.Printf("  approver: %s\n", approver)
	if a.DecidedAt != nil {
		fmt.Printf("  decided: %s\n", a.DecidedAt.Format(time.RFC3339))
	}
}
