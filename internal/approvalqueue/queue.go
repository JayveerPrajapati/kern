// Package approvalqueue is the extracted approval-queue READ surface of
// `kern approval`: pending-approval listing (with the anti-mint-storm row
// cap) and stale-pending pruning. The decide/ask/wait lifecycle stayed in
// internal/approvalcli — this package owns only the observation side, so
// queue rendering can evolve without growing the lifecycle package (the
// headroom extraction documented in cmd/kern/cmd_arch_headroom.go, landed
// with the T-02 approval-wait work 2026-11-19).
package approvalqueue

import (
	"fmt"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/governance"
)

// List renders every PENDING approval a human could still decide plus every
// live prefix grant. limit caps the pending rows (0 = all); the default view
// is capped so a mint-storm cannot flood the queue (2026-11-10).
func List(root string, limit int, limitSet bool) error {
	store := governance.NewFileStore(root)
	pending, err := store.Pending()
	if err != nil {
		return fmt.Errorf("approval list: %w", err)
	}
	fmt.Printf("pending approvals (%d):\n", len(pending))
	if len(pending) == 0 {
		fmt.Println("  (none)")
	}
	if !limitSet || limit <= 0 {
		limit = 20
	}
	shown := pending
	if limit > 0 && len(shown) > limit {
		shown = shown[:limit]
	}
	for _, a := range shown {
		reason := a.Reason
		if len(reason) > 60 {
			reason = reason[:57] + "..."
		}
		fmt.Printf("  %-20s %-8s %s\n", a.ID, a.RiskLevel, reason)
		if rep := governance.ApprovalIntegrity(a, root); !rep.OK() {
			fmt.Printf("    %s\n", integrityNote(rep))
		}
	}
	if len(shown) < len(pending) {
		fmt.Printf("  … %d more pending (kern approval list --limit 0 for all)\n", len(pending)-len(shown))
	}
	grants, gerr := governance.LivePrefixGrants(root)
	if gerr != nil {
		return fmt.Errorf("approval list: %w", gerr)
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
	return nil
}

// Prune removes PENDING approvals older than window; decided records are
// never touched — they are the policy signal and the audit history.
func Prune(root string, window time.Duration) error {
	n, err := governance.NewFileStore(root).PruneOlderThan(time.Now().Add(-window))
	if err != nil {
		return fmt.Errorf("approval prune: %w", err)
	}
	fmt.Printf("pruned %d pending approval(s) older than %s\n", n, window)
	return nil
}

// integrityNote renders an integrity verdict for one pending row: key-
// rotated and signature-unverifiable records say HOW to resolve them, a
// tampered verdict is never softened.
func integrityNote(rep governance.IntegrityReport) string {
	switch rep.Verdict {
	case governance.IntegrityKeyRotated:
		return "!! UNVERIFIABLE (key-rotated): " + rep.Reason + " — stale entries: `kern approval clear --stale`"
	case governance.IntegritySignatureUnverifiable:
		return "!! UNVERIFIABLE (signature): " + rep.Reason
	default:
		// A tampered verdict is NEVER softened, whatever reason it carries.
		msg := "!! INTEGRITY (tampered): " + rep.Reason
		if !strings.Contains(msg, "DO NOT APPROVE") {
			msg += " — DO NOT APPROVE"
		}
		return msg
	}
}
