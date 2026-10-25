package approvalcli

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/governance"
)

// approvalWaitPollInterval is how often `kern approval wait` re-reads the
// approval store while blocked on a pending decision.
const approvalWaitPollInterval = 1 * time.Second

// approvalWaitDefaultTimeout is the default --timeout for `kern approval wait`
// when the operator gives none: a waiting agent gets a generous window for
// the human decision without the command blocking forever.
const approvalWaitDefaultTimeout = 30 * time.Minute

// runApproval implements `kern approval <subcommand>` — the approval-family
// surface beyond the decision commands (`kern approve` / `kern approve
// --reject`). Sub-dispatch mirrors audit.go's "audit append/repair"
// pattern: the first positional selects the subcommand, the rest is passed
// to it. Bare `kern approval` (or an unknown subcommand) is a usage error.
func runApproval(rest []string) int {
	if len(rest) > 0 {
		switch rest[0] {
		case "wait":
			return runApprovalWait(rest[1:])
		case "clear":
			return runApprovalClear(rest[1:])
		case "ask":
			return runApprovalAsk(rest[1:])
		case "list":
			return runApprovalList(rest[1:])
		}
	}
	fatalUsage("approval requires a subcommand: kern approval ask <id> [--root R] | kern approval wait <id> [--timeout 30m] [--root R] | kern approval list [--root R] | kern approval clear <id> [--reason R] [--root R] — see 'kern approval --help'")
	return 0 // unreachable (fatalUsage panics with the exitError sentinel)
}

// runApprovalList implements `kern approval list [--root R]` — the Ask
// Protocol's visibility surface: every PENDING approval a human could still
// decide, plus every LIVE prefix grant (scope "prefix": unexpired, under its
// use cap) with prefix, uses/max and expiry. `kern approval clear <id>`
// revokes a grant early; the deny/ask paths never mint silently, so this
// listing is the complete picture of what agents may currently do.
func runApprovalList(rest []string) int {
	f, _ := parseFlagsOrDie(rest)
	root := projectRoot(f)
	store := governance.NewFileStore(root)
	pending, err := store.Pending()
	if err != nil {
		fatal("approval list: %v", err)
	}
	fmt.Printf("pending approvals (%d):\n", len(pending))
	if len(pending) == 0 {
		fmt.Println("  (none)")
	}
	for _, a := range pending {
		reason := a.Reason
		if len(reason) > 60 {
			reason = reason[:57] + "..."
		}
		fmt.Printf("  %-20s %-8s %s\n", a.ID, a.RiskLevel, reason)
		if !governance.ApprovalIntegrityOK(a) {
			fmt.Printf("    !! INTEGRITY: record was modified outside the approval workflow — DO NOT APPROVE\n")
		}
	}
	grants, gerr := governance.LivePrefixGrants(root)
	if gerr != nil {
		fatal("approval list: %v", gerr)
	}
	fmt.Printf("live prefix grants (%d):\n", len(grants))
	if len(grants) == 0 {
		fmt.Println("  (none)")
	}
	for _, g := range grants {
		cmd := g.Command
		if len(cmd) > 60 {
			cmd = cmd[:57] + "..."
		}
		fmt.Printf("  %-20s prefix %q uses %d/%d expires %s\n", g.ID, g.Prefix, g.Uses, g.MaxUses, g.ExpiresAt.Format(time.RFC3339))
		fmt.Printf("    command: %s\n", cmd)
		fmt.Printf("    revoke: kern approval clear %s --root %s\n", g.ID, root)
	}
	return 0
}

// runApprovalAsk implements `kern approval ask <id> [--root R]` — the
// Ask Protocol's ONE answer surface for humans. It renders the record
// (command, risk, evidence), then the three options every host and every
// agent use identically:
//
//	[1] accept — this time only (single-use grant, the legacy semantics)
//	[2] accept prefix — same-prefix commands run without re-asking for the
//	    rest of the session (24h TTL, use-capped, every use audit-logged);
//	    the suggested prefix is the command's first two tokens and can be
//	    edited before answering
//	[3] reject — sticky rejection (TTL-bounded as today)
//
// The prompt requires an interactive terminal (the same tty gate as
// `kern approve`): an agent or a script cannot answer — the designed
// agent-side path is `kern approval wait <id>`. The typed choice IS the
// human action; nothing is decided on timeout/EOF (an abort, never a
// decision, and the abort is audit-recorded).
func runApprovalAsk(rest []string) int {
	f, args := parseFlagsOrDie(rest)
	root := projectRoot(f)
	if len(args) < 1 || args[0] == "" {
		fatalUsage("approval ask requires an approval id: kern approval ask <id> [--root R] — pending ids: `kern approval list` or the deny message that minted it")
	}
	id := args[0]
	rec, gerr := governance.NewFileStore(root).Get(id)
	if gerr != nil {
		fatal("approval ask: %v (unknown id — pending ids: kern approval list --root %s)", gerr, root)
	}
	if rec.DecidedAt != nil || (rec.Status != "" && rec.Status != "pending") {
		fatalPolicy("approval ask: approval %q already decided as %s — check kern audit %s for state", id, rec.Status, id)
	}
	if !governance.ApprovalIntegrityOK(rec) {
		fatalPolicy("approval ask: approval %q failed integrity verification — it was modified outside the approval workflow; DO NOT answer it", id)
	}
	tty, terr := approveTerminal()
	if terr != nil {
		recordApprovalAttemptAuditVerb(root, "ask", id, "cli-user", "no-terminal", terr.Error())
		fatal("approval ask: %v — the ask is NOT answered (an interactive terminal is required; a waiting agent uses: kern approval wait %s)", terr, id)
	}
	// Render the record: the human must see exactly WHAT they are deciding.
	fmt.Fprintf(tty, "kern approval ask — %s\n", id)
	if rec.Command != "" {
		fmt.Fprintf(tty, "  command: %s\n", rec.Command)
	} else if len(rec.EvidenceRefs) > 0 {
		fmt.Fprintf(tty, "  evidence: %s\n", rec.EvidenceRefs[0])
	}
	fmt.Fprintf(tty, "  risk: %s\n", rec.RiskLevel)
	fmt.Fprintf(tty, "  reason: %s\n", rec.Reason)
	choice, cerr := readAskChoice(tty)
	if cerr != nil {
		result := "confirm-timeout"
		if !errors.Is(cerr, errConfirmTimeout) {
			result = "refused"
		}
		recordApprovalAttemptAuditVerb(root, "ask", id, "cli-user", result, cerr.Error())
		fatal("approval ask: %v — the ask is NOT answered", cerr)
	}
	// Legacy records (minted before the ask protocol, no command bound)
	// cannot carry a prefix grant — the suggested prefix would be empty.
	// Tell the human instead of dying inside SetScopePrefix; the once-
	// decision fallback never fires because [2] is re-prompted, not executed.
	for choice == "2" && rec.Command == "" {
		fmt.Fprintf(tty, "  (this approval has no command bound — it predates the ask protocol, so a prefix grant is unavailable; [1] accepts this once, [3] rejects)\n")
		choice, cerr = readAskChoice(tty)
		if cerr != nil {
			result := "confirm-timeout"
			if !errors.Is(cerr, errConfirmTimeout) {
				result = "refused"
			}
			recordApprovalAttemptAuditVerb(root, "ask", id, "cli-user", result, cerr.Error())
			fatal("approval ask: %v — the ask is NOT answered", cerr)
		}
		if choice == "2" {
			// A second [2] on a command-less record is a refusal to choose —
			// abort rather than loop forever.
			recordApprovalAttemptAuditVerb(root, "ask", id, "cli-user", "refused", "prefix requested on a command-less (legacy) approval")
			fatal("approval ask: no prefix grant is possible for a legacy approval — answer [1] or [3]")
		}
	}
	approver := f.approver
	if approver == "" {
		approver = "cli-user"
	}
	switch choice {
	case "1":
		a, mem := decideApproval(root, "approve", id, approver, true, f.reason)
		fmt.Printf("accepted once: %s — authorizes exactly ONE execution; the retry must be byte-identical\n", a.ID)
		if mem != nil {
			recordPolicySignalsBounded(root, mem)
		}
		return 0
	case "2":
		prefix := f.prefix
		if prefix == "" {
			prefix = governance.SuggestedPrefix(rec.Command)
		}
		prefix = promptAskPrefix(tty, rec.Command, prefix)
		if _, mem := decideApproval(root, "approve", id, approver, true, f.reason); mem != nil {
			recordPolicySignalsBounded(root, mem)
		}
		grant, serr := governance.SetScopePrefix(root, id, prefix, approver)
		if serr != nil {
			fatal("approval ask: accept-prefix failed: %v (the once-decision stands — this command authorized exactly one execution)", serr)
		}
		fmt.Printf("accepted prefix: %s — commands starting with %q run without re-asking\n", grant.ID, grant.Prefix)
		fmt.Printf("  uses: %d/%d — expires %s\n", grant.Uses, grant.MaxUses, grant.ExpiresAt.Format(time.RFC3339))
		return 0
	default: // "3"
		a, mem := decideApproval(root, "reject", id, approver, false, f.reason)
		fmt.Printf("rejected: %s (by %s)\n", a.ID, approver)
		if mem != nil {
			recordPolicySignalsBounded(root, mem)
		}
		return 0
	}
}

// readAskChoice reads the [1]/[2]/[3] answer from the interactive terminal
// with the shared confirm timeout. Anything but 1, 2 or 3 is a refusal (an
// abort, never a decision).
func readAskChoice(tty io.ReadWriter) (string, error) {
	fmt.Fprintf(tty, "[1] accept (this time only)  [2] accept prefix (rest of session)  [3] reject — type 1, 2 or 3: ")
	ch := make(chan string, 1)
	go func() {
		var buf [64]byte
		n, _ := tty.Read(buf[:])
		ch <- string(buf[:n])
	}()
	var typed string
	select {
	case typed = <-ch:
	case <-time.After(approveConfirmTimeout):
		return "", errConfirmTimeout
	}
	switch strings.TrimSpace(typed) {
	case "1", "2", "3":
		return strings.TrimSpace(typed), nil
	}
	return "", fmt.Errorf("invalid choice %q — type 1, 2 or 3", strings.TrimSpace(typed))
}

// promptAskPrefix shows the suggested prefix and lets the human edit it:
// empty input keeps the suggestion, anything else replaces it (the caller
// re-validates that it is a token prefix of the approved command).
func promptAskPrefix(tty io.ReadWriter, command, suggested string) string {
	if suggested == "" {
		return ""
	}
	fmt.Fprintf(tty, "prefix [default %q — empty input keeps it]: ", suggested)
	ch := make(chan string, 1)
	go func() {
		var buf [512]byte
		n, _ := tty.Read(buf[:])
		ch <- string(buf[:n])
	}()
	var typed string
	select {
	case typed = <-ch:
	case <-time.After(approveConfirmTimeout):
		return suggested // no edit within the bound: keep the suggestion, the typed-id decision still needs the main choice
	}
	if t := strings.TrimSpace(typed); t != "" {
		return t
	}
	return suggested
}

// runApprovalClear implements `kern approval clear <id> [--reason R]
// [--root R]` — the operator-side escape hatch for approval records that
// block future work without being decidable anymore. It removes the record
// (whatever its status) from the governance store in one atomic mutation
// and appends a tamper-evident audit-chain entry, so a REJECTED record
// stops blocking byte-identical retries of its command (rejection
// stickiness) and a stray PENDING record leaves `kern approve list`.
// Clearing never grants anything — the next gated attempt mints a FRESH
// approval a human still has to decide — which is why clear, like reject,
// stays non-interactive: every direction is deny-or-re-arm, and the
// dangerous direction (granting) keeps its interactive typed-id gate in
// `kern approve`.
func runApprovalClear(rest []string) int {
	f, args := parseFlagsOrDie(rest)
	root := projectRoot(f)
	if len(args) < 1 || args[0] == "" {
		fatalUsage("approval clear requires an approval id: kern approval clear <id> [--reason R] [--root R] — rejected ids: see the denial message or `kern audit`; pending ids: `kern approve list`")
	}
	id := args[0]
	actor := f.approver
	if actor == "" {
		actor = "cli-user"
	}
	store := governance.NewFileStore(root)
	if err := store.LoadError(); err != nil {
		fatal("approval clear: %v", err)
	}
	a, err := store.Clear(id, actor, f.reason)
	if err != nil {
		fatal("approval clear: %v — pending ids: `kern approve list`", err)
	}
	status := a.Status
	if status == "" {
		status = "pending"
	}
	fmt.Printf("cleared: %s\n", a.ID)
	fmt.Printf("  status: %s\n", status)
	if a.TaskID != "" {
		fmt.Printf("  task: %s\n", a.TaskID)
	}
	if a.DecidedAt != nil {
		fmt.Printf("  decided: %s\n", a.DecidedAt.Format(time.RFC3339))
	}
	fmt.Printf("  by: %s\n", actor)
	if status == "rejected" {
		fmt.Printf("  effect: byte-identical retries of the gated command now mint a fresh approval (a human still has to decide it)\n")
	}
	return 0
}

// runApprovalWait blocks until the approval with the given ID is decided,
// polling the two stores `kern approve` resolves — the governance store
// (<root>/.kern/approvals.json, appr-* ids) and the blueprint two-person-rule
// store (<root>/.blueprint/approvals/requests.jsonl, apr-* ids written by
// `kern request-approval`) — on a ~1s ticker. It is the blocking observer
// for a pending approval: an agent that hit a "resolve with: kern approve
// <id>" denial can await the human decision instead of relying on manual
// relay. It prints the current state once at start ("pending: <id>"), then
// the decision when it lands.
//
// Exit codes per the repo contract (see `kern exitcode`):
//   - 0 when the approval was approved;
//   - 3 (denied / policy outcome) when it was rejected;
//   - 1 when --timeout expires before a decision lands (or the id is
//     unknown — a miss is a real error, not a reason to block for the full
//     timeout).
func runApprovalWait(rest []string) int {
	f, args := parseFlagsOrDie(rest)
	root := projectRoot(f)
	if len(args) < 1 || args[0] == "" {
		fatalUsage("approval wait requires an approval id: kern approval wait <id> [--timeout 30m] [--root R]")
	}
	id := args[0]
	timeout := approvalWaitTimeout(f)

	govStore := governance.NewFileStore(root)
	fmt.Printf("pending: %s\n", id)

	// Fail fast on an unknown id: the wait is for an approval the caller was
	// already told about (e.g. via the exec deny message or `kern
	// request-approval`'s "A human must run: kern approve <id>" output), so
	// a miss in BOTH stores is a real error, not a reason to block for the
	// full timeout. Store loads are cheap (two small files), so the poll
	// loop re-reads them on every tick and observes decisions made by any
	// process.
	inGov := false
	if _, err := govStore.Get(id); err == nil {
		inGov = true
	}
	if !inGov {
		// A miss is either a wrong id or an approval that was already
		// decided AND consumed (the single-use governance grant is removed
		// from its store once a retry passed with it). Both mean waiting is
		// pointless — fail fast, but say which is which so the caller is
		// not left guessing whether to re-run the original command.
		fatal("approval wait: approval not found: %s — the id is wrong, or the approval was already consumed by a successful retry (re-run the original command; it will pass without a new approval); pending ids: `kern approve list`", id)
	}

	ticker := time.NewTicker(approvalWaitPollInterval)
	defer ticker.Stop()
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	for {
		if inGov {
			if a, err := govStore.Get(id); err == nil && a.DecidedAt != nil {
				return printApprovalDecision(a)
			}
		}
		select {
		case <-ticker.C:
		case <-timer.C:
			fmt.Printf("approval %s still pending after %s — timed out\n", id, timeout)
			return 1
		}
	}
}

// approvalWaitTimeout resolves `kern approval wait`'s --timeout: a duration
// string (30m, 500ms) wins, then integer seconds, then the 30m default.
func approvalWaitTimeout(f flags) time.Duration {
	if f.timeoutDur != "" {
		if d, err := time.ParseDuration(f.timeoutDur); err == nil {
			return d
		}
	}
	if f.timeoutSet {
		return time.Duration(f.timeout) * time.Second
	}
	return approvalWaitDefaultTimeout
}

// printApprovalDecision renders the landed decision and maps it to the exit
// code the repo contract assigns: approved → 0, rejected → 3 (denied), any
// other decided state → 1 (present but not classifiable as a grant).
func printApprovalDecision(a domain.Approval) int {
	switch a.Status {
	case "approved":
		fmt.Printf("approved: %s\n", a.ID)
	case "rejected":
		fmt.Printf("rejected: %s\n", a.ID)
	default:
		fmt.Printf("decided: %s (status %s)\n", a.ID, a.Status)
		return 1
	}
	if a.TaskID != "" {
		fmt.Printf("  task: %s\n", a.TaskID)
	}
	if a.Approver != "" {
		fmt.Printf("  approver: %s\n", a.Approver)
	}
	if a.DecidedAt != nil {
		fmt.Printf("  decided: %s\n", a.DecidedAt.Format(time.RFC3339))
	}
	if a.Status == "approved" {
		return 0
	}
	return 3
}
