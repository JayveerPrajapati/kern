package cli

import (
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/blueprint/approval"
	"github.com/JayveerPrajapati/kern/internal/blueprint/audit"
	"github.com/JayveerPrajapati/kern/internal/blueprint/domain"
	"github.com/JayveerPrajapati/kern/internal/blueprint/policy"
	"github.com/JayveerPrajapati/kern/internal/blueprint/risk"
)

// runRequestApproval implements `blueprint request-approval` — the first half
// of the two-person rule (P1.3). It classifies the change surface, records a
// pending approval request in .blueprint/approvals/requests.jsonl, and prints
// the request id for a human to approve.
//
// Flags:
//
//	--repo=PATH      Repository root (default: current directory).
//	--intent=TEXT    Human-readable intent (required).
//	--source=SRC     Change source (default "agent").
//	--files=LIST     Comma-separated paths (default: discover staged changes).
//	--requester=WHO  Requester identity (default: git config user.email or $USER).
func runRequestApproval(args []string) int {
	fs := flag.NewFlagSet("request-approval", flag.ContinueOnError)
	repoRoot := fs.String("repo", "", "repository root (default: current directory)")
	intent := fs.String("intent", "", "human-readable intent for the change (required)")
	source := fs.String("source", "agent", "change source: agent|ide|human|refactor|dep-bot|ci")
	files := fs.String("files", "", "comma-separated paths to request approval for (default: staged changes)")
	requester := fs.String("requester", "", "requester identity (default: git config user.email or $USER)")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0 // -h/--help: usage already printed; exit clean
		}
		return 2
	}
	if strings.TrimSpace(*intent) == "" {
		fmt.Fprintln(os.Stderr, "blueprint: request-approval: --intent is required")
		return 2
	}

	root := *repoRoot
	if root == "" {
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(os.Stderr, "blueprint: request-approval: %v\n", err)
			return 2
		}
		root = cwd
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "blueprint: request-approval: invalid repository path %q: %v\n", root, err)
		return 2
	}

	cfg, err := policy.Load(absRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "blueprint: request-approval: invalid configuration: %v\n", err)
		return 3
	}
	if !cfg.File.Approval.IsEnabled() {
		fmt.Fprintln(os.Stderr, "blueprint: approval gate is not enabled; configure `approval: enabled: true` in .blueprint/config.yaml")
		return 2
	}

	// Determine the change surface: explicit --files, else staged changes.
	var changes []domain.FileChange
	if strings.TrimSpace(*files) != "" {
		for _, p := range strings.Split(*files, ",") {
			p = strings.TrimSpace(p)
			if p != "" {
				changes = append(changes, domain.FileChange{Path: p, Op: domain.OpEdit})
			}
		}
	} else {
		staged, err := discoverStagedChanges(absRoot)
		if err != nil {
			fmt.Fprintf(os.Stderr, "blueprint: request-approval: cannot discover staged changes: %v\n", err)
			return 2
		}
		changes = staged
	}
	if len(changes) == 0 {
		fmt.Fprintln(os.Stderr, "blueprint: request-approval: no files to request approval for (stage changes or pass --files)")
		return 2
	}

	req := domain.ChangeRequest{
		RepositoryRoot: absRoot,
		Source:         domain.Source(*source),
		Operation:      domain.OpCommit,
		Files:          changes,
	}
	assessment := risk.Classify(req, risk.LoadConfig(cfg.File.Approval))

	who := strings.TrimSpace(*requester)
	if who == "" {
		who = currentApprover(absRoot)
	}

	paths := make([]string, 0, len(changes))
	for _, fc := range changes {
		paths = append(paths, fc.Path)
	}

	store := approval.NewStore(absRoot)
	ar := approval.Request{
		ID:        newApprovalID(),
		RepoRoot:  absRoot,
		Intent:    strings.TrimSpace(*intent),
		RiskLevel: string(assessment.Level),
		Requester: who,
		Files:     paths,
		CreatedAt: time.Now(),
	}
	if err := store.Create(ar); err != nil {
		fmt.Fprintf(os.Stderr, "blueprint: request-approval: %v\n", err)
		return 2
	}

	fmt.Printf("Request %s created (risk=%s", ar.ID, assessment.Level)
	if len(assessment.Reasons) > 0 {
		fmt.Printf("; %s", strings.Join(assessment.Reasons, "; "))
	}
	fmt.Printf(").\n")
	fmt.Printf("A human must run: blueprint approve %s\n", ar.ID)
	return 0
}

// runApprovalDecision implements `blueprint approve <id>` and
// `blueprint reject <id>` — the second half of the two-person rule (P1.3). It
// records the human's decision (with identity, timestamp, and optional
// reason) in the approval store AND appends an approval-decision record to
// .blueprint/audit/audit.jsonl.
//
// Flags:
//
//	--repo=PATH     Repository root (default: current directory).
//	--reason=TEXT   Optional human reason for the decision.
//	--approver=WHO  Approver identity (default: git config user.email or $USER).
func runApprovalDecision(decision string, args []string) int {
	fs := flag.NewFlagSet(decision, flag.ContinueOnError)
	repoRoot := fs.String("repo", "", "repository root (default: current directory)")
	reason := fs.String("reason", "", "reason for the decision (optional)")
	approver := fs.String("approver", "", "approver identity (default: git config user.email or $USER)")
	// stdlib flag stops at the first positional argument, so `approve <id>
	// --reason ...` would silently drop trailing flags. Reorder: flags first,
	// positional (the request id) last, preserving flag values.
	if err := fs.Parse(approvalFlagArgs(args, map[string]bool{"repo": true, "reason": true, "approver": true})); err != nil {
		if err == flag.ErrHelp {
			return 0 // -h/--help: usage already printed; exit clean
		}
		return 2
	}
	id := fs.Arg(0)
	if id == "" {
		fmt.Fprintf(os.Stderr, "blueprint: %s requires a request id: blueprint %s <id>\n", decision, decision)
		return 2
	}

	root := *repoRoot
	if root == "" {
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(os.Stderr, "blueprint: %s: %v\n", decision, err)
			return 2
		}
		root = cwd
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "blueprint: %s: invalid repository path %q: %v\n", decision, root, err)
		return 2
	}

	who := strings.TrimSpace(*approver)
	if who == "" {
		who = currentApprover(absRoot)
	}

	store := approval.NewStore(absRoot)
	cur, err := store.Get(id)
	if err != nil {
		// Request not found is an operational error (exit 3), not a policy
		// violation (exit 1).
		fmt.Fprintf(os.Stderr, "blueprint: %s: %v\n", decision, err)
		return 3
	}
	if cur.Status != approval.StatusPending {
		// Already-decided is an operational error (exit 3), not a policy
		// violation (exit 1).
		fmt.Fprintf(os.Stderr, "blueprint: %s: request %s already decided as %s\n", decision, id, cur.Status)
		return 3
	}

	switch decision {
	case "approve":
		if err := store.Approve(id, who, *reason); err != nil {
			fmt.Fprintf(os.Stderr, "blueprint: approve: %v\n", err)
			return 2
		}
	case "reject":
		if err := store.Reject(id, who, *reason); err != nil {
			fmt.Fprintf(os.Stderr, "blueprint: reject: %v\n", err)
			return 2
		}
	default:
		fmt.Fprintf(os.Stderr, "blueprint: unknown decision %q\n", decision)
		return 2
	}

	// Audit the decision: an approval-decision record distinguishes it from
	// validation records in the same JSONL trail (audit.Record.Kind).
	status := domain.StatusPass
	ruleID := "approval:approved"
	if decision == "reject" {
		status = domain.StatusBlock
		ruleID = "approval:rejected"
	}
	writeApprovalAudit(absRoot, audit.Record{
		Kind:      "approval-decision",
		Timestamp: time.Now(),
		Source:    domain.SourceHuman,
		Operation: domain.OpCommit,
		RepoRoot:  absRoot,
		Status:    status,
		ExitCode:  0,
		Summary:   audit.SummaryMeta{Total: 1},
		Findings: []audit.FindingMeta{{
			RuleID:   ruleID,
			Severity: domain.SeverityWarn,
			Category: domain.CategoryPolicy,
		}},
	})

	fmt.Printf("Request %s %s by %s. Re-run your change with --approval-id %s\n", id, decision+"d", who, id)
	return 0
}

// writeApprovalAudit appends an approval-decision record to the repo's audit
// trail. Best-effort, exactly like validation audit writes: a failure never
// fails the CLI command.
func writeApprovalAudit(repoRoot string, rec audit.Record) {
	w := audit.NewWriter(filepath.Join(repoRoot, ".blueprint", "audit", "audit.jsonl"))
	_ = w.Write(rec)
}

// currentApprover derives a human identity for approval decisions: git
// config user.email when available, else $USER.
func currentApprover(repoRoot string) string {
	if email, err := gitOutput(repoRoot, "config", "user.email"); err == nil {
		if e := strings.TrimSpace(email); e != "" {
			return e
		}
	}
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	return "unknown"
}

// newApprovalID returns a random short request id ("apr-<16 hex>") for the
// approval store. Collisions are practically impossible; the store treats the
// latest record per id as authoritative regardless.
func newApprovalID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("apr-%d", time.Now().UnixNano())
	}
	return "apr-" + hex.EncodeToString(b)
}

// approvalFlagArgs reorders command-line args so flags come before positional
// arguments. Go's stdlib flag package stops parsing at the first non-flag
// argument, which would silently drop trailing flags after a positional
// request id (`approve <id> --reason ...`). valueFlags names the flags that
// consume the following argument, so their values stay attached.
func approvalFlagArgs(args []string, valueFlags map[string]bool) []string {
	var flags, positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") && a != "-" {
			flags = append(flags, a)
			name := strings.SplitN(strings.TrimLeft(a, "-"), "=", 2)[0]
			if valueFlags[name] && !strings.Contains(a, "=") && i+1 < len(args) {
				flags = append(flags, args[i+1])
				i++
			}
		} else {
			positional = append(positional, a)
		}
	}
	return append(flags, positional...)
}
