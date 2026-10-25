package approvalcli

import (
	"errors"
	"fmt"
	"io"
	stdlog "log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/app"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/memory"
	"github.com/JayveerPrajapati/kern/internal/storage"
	"github.com/JayveerPrajapati/kern/internal/tasklife"
	"github.com/mattn/go-isatty"
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
		// Blueprint two-person-rule requests (apr-* ids, `kern
		// request-approval`, .blueprint/approvals/requests.jsonl) are
		// decidable by this same command surface (decideApproval resolves
		// both stores) — list them too, or `kern approve list` says "no
		// pending approvals" while apr-* ids sit decidable (F2-5).
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

	// Both polarities are human-gated: approving AND rejecting require a
	// human to type the id on the interactive terminal (an agent with CLI
	// access must not approve OR reject without a human, M3). The decided-
	// state guard fires before the gate (already-decided → rc=3 without
	// prompting).
	verb := "approve"
	if f.reject {
		verb = "reject"
	}
	requireApprovalConfirmation(root, id, approver, verb)

	// Resolve the id against the same two approval stores decideApproval
	// serves — governance first (via the app layer), then the blueprint
	// two-person-rule store — and record the decision. The decided-state
	// guard (rc=3) and the store order are shared with `kern reject`, so a
	// single command surface can decide every approval `kern approve`
	// resolves.
	a, mem := decideApproval(root, "approve", id, approver, !f.reject, f.reason)
	if f.reject {
		fmt.Printf("rejected: %s (by %s)\n", a.ID, approver)
		if a.TaskID != "" {
			fmt.Printf("  task: %s marked REJECTED\n", a.TaskID)
		}
	} else {
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
		// Ask Protocol (P1): `kern approve <id> --scope prefix [--prefix P]`
		// widens the just-made once-decision into a session-scoped prefix
		// grant (commands starting with the prefix run without re-asking,
		// use-capped and expiry-bounded, every use audit-logged). Only the
		// approve polarity qualifies — a rejection is never widened.
		if !f.reject && f.scope != "" && f.scope != "once" {
			if f.scope != "prefix" {
				fatalUsage("approve: --scope must be once or prefix (got %q)", f.scope)
			}
			grant, gerr := governance.SetScopePrefix(root, id, f.prefix, approver)
			if gerr != nil {
				fatal("approve --scope prefix: %v (the once-decision stands — this command authorized exactly one execution)", gerr)
			}
			fmt.Printf("  prefix grant: commands starting with %q run without re-asking\n", grant.Prefix)
			fmt.Printf("  uses: %d/%d — expires %s\n", grant.Uses, grant.MaxUses, grant.ExpiresAt.Format(time.RFC3339))
		}
	}
	// The confirmation above is printed immediately after the decision was
	// persisted (os.Stdout is unbuffered — each Printf is an immediate write
	// syscall), BEFORE the post-decision learning pass runs, so the waiting
	// agent sees the outcome promptly. Learning is best-effort and bounded
	// (recordPolicySignalsBounded) — it must never hold up the command exit.
	if mem != nil {
		recordPolicySignalsBounded(root, mem)
	}
}

// requireApprovalConfirmation runs the interactive human-confirmation gate
// for a decision (approve OR reject): a human must type the approval id on
// the interactive terminal before ANY decision is persisted. It is shared by
// runApprove and runReject so a reject is every bit as human-gated as an
// approve — an agent with CLI access must not be able to reject a pending
// approval without a human typing the id, exactly as it cannot approve one.
//
// The decided-state guard fires BEFORE the gate (an already-decided approval
// is refused rc=3 without prompting — prompting a human to confirm an
// already-decided id reads as a silent hang). A missing interactive terminal
// fails closed (rc=1, "no-terminal", undecided, audited); a wrong typed token
// is a human refusal ("refused", undecided, audited); a timeout is an aborted
// attempt ("confirm-timeout", undecided, audited).
func requireApprovalConfirmation(root, id, approver, verb string) {
	rec := domain.Approval{ID: id}
	if store := governance.NewFileStore(root); store.LoadError() == nil {
		if existing, gerr := store.Get(id); gerr == nil {
			if existing.DecidedAt != nil {
				fatalPolicy("%s: approval %q already decided as %s — check kern audit %s for state", verb, id, existing.Status, id)
			}
			rec = existing
		}
	}

	tty, terr := approveTerminal()
	if terr != nil {
		recordApprovalAttemptAuditVerb(root, verb, id, approver, "no-terminal", terr.Error())
		fatal("%s: %v — approval %s NOT decided (human confirmation requires an interactive terminal)", verb, terr, id)
	}
	// The confirmation prompt itself goes to the interactive terminal,
	// which a non-interactive caller (a governed agent, a script) cannot
	// see — say on stdout WHY the command is waiting, so it never reads
	// as a silent hang (QA 2026-10-15 finding). Nothing is decided
	// until a human types the id.
	fmt.Printf("%s: waiting for interactive confirmation of %s (a human must type the id on the terminal; confirm timeout %s) — approval NOT decided until then; a waiting agent should use: kern approval wait %s\n", verb, id, approveConfirmTimeout, id)
	if cerr := requireInteractiveApproval(tty, rec); cerr != nil {
		result := "refused"
		if errors.Is(cerr, errConfirmTimeout) {
			// A timeout is an aborted attempt (no human input), not a
			// human refusal — audit it as such so the chain never claims
			// a refusal that never happened.
			result = "confirm-timeout"
		}
		recordApprovalAttemptAuditVerb(root, verb, id, approver, result, cerr.Error())
		fatal("%s: %v — approval %s NOT decided", verb, cerr, id)
	}
}

// approveTerminal is the interactive channel for the approval gate. It is
// test-injectable; production resolves the human's terminal: stdin MUST be a
// TTY — the invocation's own stdin is the only terminal surface the caller
// can see. There is deliberately NO /dev/tty fallback: a caller whose stdin
// is a pipe or /dev/null (a governed agent, a script) is non-interactive by
// definition, and reading the process's controlling terminal from such a
// context prompts on a surface the caller cannot see — and in job-control
// contexts blocks unboundedly (QA 2026-10-15 live finding: the confirm read
// on /dev/tty stopped the process past its own timeout). Fail closed and
// bounded instead; the designed agent-side path is `kern approval wait`.
var approveTerminal = func() (io.ReadWriter, error) {
	if isatty.IsTerminal(os.Stdin.Fd()) {
		return os.Stdin, nil
	}
	return nil, fmt.Errorf("no interactive terminal: stdin is not a terminal (piped or scripted stdin cannot confirm a two-person-rule approval — run kern approve from an interactive terminal, or wait with: kern approval wait <id>)")
}

// approveConfirmTimeout bounds the typed confirmation read so a hung prompt
// aborts instead of blocking. 30s: enough for a human to find the id they were
// told to approve and type it (15s proved too tight in live QA), short enough
// that a non-interactive caller still fails fast. Test-injectable.
var approveConfirmTimeout = 30 * time.Second

// errConfirmTimeout marks a confirmation read that got no human input within
// the bound: an aborted attempt, not a decision and not a human refusal.
var errConfirmTimeout = errors.New("approval confirmation timed out — not decided (no human typed the id within the bound)")

// requireInteractiveApproval requires the human to TYPE the approval id they
// were told to approve on the interactive terminal f. It prints the record's
// evidence first (e.g. the exact command text) so the human sees WHAT they
// are approving. A wrong token or EOF is a refusal; a timeout is an aborted
// attempt (errConfirmTimeout), never a decision.
func requireInteractiveApproval(f io.ReadWriter, a domain.Approval) error {
	fmt.Fprintf(f, "approval %s", a.ID)
	if a.TaskID != "" {
		fmt.Fprintf(f, " (task %s)", a.TaskID)
	}
	fmt.Fprintf(f, " — type the approval id to confirm: ")
	ch := make(chan string, 1)
	go func() {
		var buf [4096]byte
		n, _ := f.Read(buf[:])
		ch <- string(buf[:n])
	}()
	var typed string
	select {
	case typed = <-ch:
	case <-time.After(approveConfirmTimeout):
		return errConfirmTimeout
	}
	if strings.TrimSpace(typed) != a.ID {
		return fmt.Errorf("approval not confirmed: typed %q, expected %q", strings.TrimSpace(typed), a.ID)
	}
	return nil
}

// recordApprovalAttemptAudit appends a failed (non-decided) approval attempt
// to the project's tamper-evident governance chain (.kern/audit) so aborted
// attempts show in `kern audit` next to decisions. result distinguishes a
// genuine human refusal ("refused": the wrong token was typed) from
// environmental aborts ("no-terminal", "confirm-timeout") — only a human
// typing the wrong token is a refusal; a timeout or a missing terminal is
// not a decision. Best-effort: a failure never blocks the abort.
func recordApprovalAttemptAudit(root, id, approver, result, reason string) {
	recordApprovalAttemptAuditVerb(root, "approve", id, approver, result, reason)
}

// recordApprovalAttemptAuditVerb is recordApprovalAttemptAudit with the
// action verb (approve|reject) explicit, so a rejected attempt is recorded
// as Action reject in the chain.
func recordApprovalAttemptAuditVerb(root, verb, id, approver, result, reason string) {
	entry := governance.AuditEntry{
		AgentID:   approver,
		Action:    verb,
		Resource:  "approval:" + id,
		Approved:  false,
		Result:    result,
		Policy:    "approval",
		Reason:    reason,
		Timestamp: time.Now(),
	}
	auditDir := filepath.Join(root, ".kern", "audit")
	l := governance.NewAuditLog().
		WithStore(storage.NewLog(auditDir)).
		WithLockPath(filepath.Join(auditDir, ".lock")).
		WithMirror(governance.MirrorFor(auditDir))
	if err := l.AppendExternal(entry); err != nil {
		stdlog.Printf("kern approve: aborted attempt for %s NOT recorded in audit chain: %v", id, err)
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
// outcome). The third return is the governance store's memory store, non-nil
// only when the governance store decided: the caller runs the policy-signal
// learning pass (recordPolicySignalsBounded) AFTER printing the confirmation,
// so learning can never delay the visible outcome.
func decideApproval(root, verb, id, approver string, approve bool, reason string) (domain.Approval, *memory.MemoryStore) {
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
	ts := tasklife.NewTaskService(p, nil).WithAgentID(approver)
	a, err := ts.ResolveApprovalForTask(id, approver, approve, reason)
	if err != nil {
		// Governance store miss: fail loudly (rc=1) — never silently
		// succeed. An approval `kern approve` lists must never be
		// undecidable, and an unknown id must never silently pass.
		if strings.Contains(err.Error(), "approval not found") {
			fatal("%s: approval %s not found — check kern audit %s for state", verb, id, id)
		}
		fatal("%s: %v — check kern audit %s for state", verb, err, id)
	}
	// The post-decision policy-signal learning pass (Self-improvement
	// use-cases Tier 3 #7) runs in the CALLER (runApprove) after the
	// confirmation is printed, bounded by a hard timeout — learning must
	// never hold up the command once the decision is durable.
	return a, p.Memory()
}

// policySignalTimeout bounds the post-decision policy-signal learning pass.
// Test-injectable so the bounded path is testable without a 10s wait.
var policySignalTimeout = 10 * time.Second

// recordPolicySignalsBounded runs the post-decision learning pass
// (Self-improvement Tier 3 #7: the approval log → typed-claim memories) with
// a hard time bound. The decision is already durable and the confirmation
// already printed, so a slow or hung learning pass must never hold up the
// command. On timeout or error it prints a warning line and returns; learning
// proposes memories only (RECOMMENDATION / INFERENCE) and never changes
// policy. Best-effort by design — the caller logs and ignores errors.
func recordPolicySignalsBounded(root string, mem *memory.MemoryStore) {
	done := make(chan struct{}, 1)
	go func() {
		defer close(done)
		if _, lerr := app.RecordPolicySignals(governance.NewFileStore(root), mem, app.DefaultPolicySignalThreshold); lerr != nil {
			stdlog.Printf("kern approve: policy signal learning skipped: %v", lerr)
		}
	}()
	select {
	case <-done:
	case <-time.After(policySignalTimeout):
		fmt.Fprintln(os.Stderr, "kern approve: WARNING: policy signal learning timed out and was skipped (the approval decision is already recorded)")
	}
}

// decideBlueprintApproval resolves a request in the blueprint approval store
// (written by `kern request-approval`) and records the human decision.
