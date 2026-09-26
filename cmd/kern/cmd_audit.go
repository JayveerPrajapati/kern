package main

import (
	"context"
	"encoding/json"
	"fmt"
	stdlog "log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	kctx "github.com/JayveerPrajapati/kern/internal/context"
	"github.com/JayveerPrajapati/kern/internal/gates"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/storage"
)

// auditInvalidate marks blocked context entities stale on validation failure.
var auditInvalidate = func(entities []string, reason, source string, at time.Time) []kctx.InvalidationMarker {
	return kctx.InvalidateContext(entities, reason, source, at)
}

// runAudit shows audit trail entries, optionally filtered by task ID.
func runAudit(rest []string) {
	if len(rest) > 0 {
		switch rest[0] {
		case "append":
			runAuditAppend(rest[1:])
			return
		case "repair":
			runAuditRepair(rest[1:])
			return
		}
	}

	f, args := parseFlagsOrDie(rest)
	root := projectRoot(f)

	taskID := ""
	if len(args) > 0 {
		taskID = args[0]
	}

	entries, err := governance.ReadAuditTrail(context.Background(), root, taskID)
	if err != nil {
		fatal("Audit: %v", err)
	}

	// F22: approval decisions made via the blueprint approval store
	// (`kern request-approval` + `kern approve/reject apr-*`) are recorded in
	// .blueprint/approvals/requests.jsonl — not the governance chain — so
	// they never appeared in `kern audit`. Surface them as audit rows so a
	// human approve/reject decision is always reconstructable end-to-end.
	// Entries the governance chain already carries for the same decision
	// (task-gated approvals record Action approve/reject with Resource
	// "approval:<id>") are not duplicated.
	if taskID == "" {
		entries = append(entries, blueprintApprovalAuditEntries(root, entries)...)
		sort.SliceStable(entries, func(i, j int) bool {
			return entries[i].Timestamp.Before(entries[j].Timestamp)
		})
	}

	if len(entries) == 0 {
		if taskID != "" {
			fmt.Printf("no audit entries for task %s\n", taskID)
		} else {
			fmt.Println("no audit entries")
		}
		return
	}
	// Verify the tamper-evident hash chain before displaying records: the
	// viewer must not present forged entries as genuine (validation W-3).
	// Same semantics as the platform startup check (internal/app/platform.go)
	// and the hard gate in `kern evidence verify`; this is a warning, not a
	// refusal — `kern audit repair` and `kern evidence verify` remain the
	// explicit remediation commands.
	if auditDir := filepath.Join(root, ".kern", "audit"); isDir(auditDir) {
		l := governance.NewAuditLog().
			WithStore(storage.NewLog(auditDir)).
			WithLockPath(filepath.Join(auditDir, ".lock"))
		if _, err := l.Replay(); err == nil {
			all := l.All()
			if brk, verified := l.VerifyChainReport(); brk >= 0 {
				if verified == 0 {
					fmt.Fprintf(os.Stderr, "WARNING: audit log chain verification could not verify ANY entries (0 of %d) — this indicates either a legacy version migration or a full-chain rewrite; if this is not a known upgrade, investigate before trusting governance records (kern audit repair / kern evidence verify)\n", len(all))
				} else {
					fmt.Fprintf(os.Stderr, "WARNING: audit log tamper chain verification FAILED at entry %d of %d — governance records may have been modified; investigate before trusting them (kern audit repair / kern evidence verify)\n", brk+1, len(all))
				}
			}
			// Surface degraded chain integrity: when the HMAC secret is
			// unavailable, hashes fall back to plain SHA-256 and
			// VerifyChainReport still passes, so the degradation must be
			// visible here rather than silent (the write path deliberately
			// never loses entries over an unavailable key).
			if l.IntegrityMode() == "plain" {
				fmt.Fprintf(os.Stderr, "WARNING: audit chain HMAC secret unavailable — hashes are plain SHA-256 (full-chain-rewrite tamper protection is OFF); restore the key at <UserConfigDir>/kern/audit-chain.key, then run `kern audit repair`\n")
			}
		}
	}
	if f.json {
		printJSON(entries)
		return
	}

	fmt.Printf("%-22s %-14s %-12s %-20s %-8s %s\n", "TIME", "AGENT", "ACTION", "RESOURCE", "APPROVED", "RESULT")
	for _, e := range entries {
		approved := "no"
		if e.Approved {
			approved = "yes"
		}
		// Surface every field an entry carries: external appends often set
		// only some fields (e.g. event/by/note), so fall back along the
		// field chain instead of printing blank columns.
		agent := e.AgentID
		if agent == "" {
			agent = e.Policy
		}
		action := e.Action
		resource := e.Resource
		if resource == "" {
			resource = e.TaskID
		}
		result := e.Result
		if result == "" {
			result = e.Reason
		}
		if len(result) > 40 {
			result = result[:37] + "..."
		}
		fmt.Printf("%-22s %-14s %-12s %-20s %-8s %s\n",
			e.Timestamp.Format("2006-01-02 15:04:05"),
			cellOrDash(agent),
			cellOrDash(action),
			cellOrDash(resource),
			approved,
			cellOrDash(result),
		)
	}
}

// cellOrDash renders an empty table cell as "-" so an entry never shows as a
// fully blank row.
func cellOrDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// blueprintApprovalAuditEntries converts decided blueprint approval requests
// (status approved/rejected in .blueprint/approvals/requests.jsonl) into
// governance-shaped audit rows. Best-effort and read-only: a missing or
// unreadable approval store yields no entries, never an error. Entries the
// governance chain already records for the same decision (Action approve/
// reject with Resource "approval:<id>", e.g. task-gated approvals) are
// skipped so a decision is never shown twice.
func blueprintApprovalAuditEntries(root string, existing []governance.AuditEntry) []governance.AuditEntry {
	reqs, err := gates.NewStore(root).List("")
	if err != nil || len(reqs) == 0 {
		return nil
	}
	have := map[string]bool{}
	for _, e := range existing {
		if (e.Action == "approve" || e.Action == "reject") && strings.HasPrefix(e.Resource, "approval:") {
			have[e.Action+":"+e.Resource] = true
		}
	}
	var out []governance.AuditEntry
	for _, r := range reqs {
		var action, result string
		var approved bool
		switch r.Status {
		case gates.StatusApproved:
			action, result, approved = "approve", "approved", true
		case gates.StatusRejected:
			action, result = "reject", "rejected"
		default:
			continue // pending/expired — no human decision to surface
		}
		if r.DecidedAt == nil {
			continue // decided record without a timestamp is not a real decision
		}
		resource := "approval:" + r.ID
		if have[action+":"+resource] {
			continue
		}
		reason := r.Reason
		if reason == "" {
			reason = r.Intent
		}
		out = append(out, governance.AuditEntry{
			Timestamp: *r.DecidedAt,
			AgentID:   r.Approver,
			Action:    action,
			Resource:  resource,
			Approved:  approved,
			Result:    result,
			Policy:    "approval",
			Reason:    reason,
		})
	}
	return out
}

// runAuditAppend links an external entry into the tamper-evident audit chain.
func runAuditAppend(rest []string) {
	f, args := parseFlagsOrDie(rest)
	if len(args) > 0 {
		fatalUsage("audit append: unexpected argument %q (entry JSON comes from stdin or --file)", args[0])
	}
	root := projectRoot(f)

	var entry governance.AuditEntry
	var raw map[string]any
	switch {
	case f.file != "":
		data, err := os.ReadFile(f.file)
		if err != nil {
			fatal("audit append: read entry file: %v", err)
		}
		if err := json.Unmarshal(data, &entry); err != nil {
			fatalUsage("audit append: invalid AuditEntry JSON in %s: %v", f.file, err)
		}
		_ = json.Unmarshal(data, &raw) // best-effort: alias keys below are optional
	default:
		data, err := readStdin()
		if err != nil {
			fatal("audit append: read stdin: %v", err)
		}
		if err := json.Unmarshal(data, &entry); err != nil {
			fatalUsage("audit append: invalid AuditEntry JSON on stdin: %v", err)
		}
		_ = json.Unmarshal(data, &raw) // best-effort: alias keys below are optional
	}
	// Accept the friendly keys external writers commonly use (event/by/note)
	// alongside the canonical AuditEntry fields, so an appended entry carries
	// its content into the table render instead of appearing as blank columns.
	applyAuditEntryAliases(&entry, raw)

	auditDir := filepath.Join(root, ".kern", "audit")
	store := storage.NewLog(auditDir)
	log := governance.NewAuditLog().WithStore(store).WithLockPath(filepath.Join(auditDir, ".lock"))
	if _, err := log.Replay(); err != nil {
		fatal("audit append: replay: %v", err)
	}
	if err := log.AppendExternal(entry); err != nil {
		fatal("audit append: %v", err)
	}

	// Consume Blueprint's validation outcome. BLOCK/ERROR marks the
	// blocked context entities stale (in-memory invalidation only — no
	// persistence in this first cut); WARN is logged but does not invalidate;
	// PASS/SKIP and absent outcomes take no action.
	if vo := entry.ValidationOutcome; vo != nil {
		switch vo.Status {
		case "BLOCK", "ERROR":
			markers := auditInvalidate(vo.BlockedFiles, "blueprint-validation-failed", "blueprint", entry.Timestamp)
			stdlog.Printf("INFO: invalidated %d context entities due to blueprint validation failure (status=%s, correlation=%s)",
				len(markers), vo.Status, vo.CorrelationID)
		case "WARN":
			stdlog.Printf("INFO: blueprint validation warning (status=WARN, correlation=%s) — no invalidation", vo.CorrelationID)
		}
	}

	all := log.All()
	last := all[len(all)-1]
	fmt.Printf("appended %s (hash %s)\n", last.ID, last.Hash)
}

// applyAuditEntryAliases maps the human-friendly JSON keys external writers
// commonly use onto the canonical AuditEntry fields, only filling fields the
// entry does not already carry. Keys recognized: event → Action, by → AgentID,
// note → Reason, resource → Resource, result → Result, task → TaskID.
func applyAuditEntryAliases(e *governance.AuditEntry, raw map[string]any) {
	if e == nil {
		return
	}
	if e.Action == "" {
		if v, ok := raw["event"].(string); ok {
			e.Action = v
		}
	}
	if e.AgentID == "" {
		if v, ok := raw["by"].(string); ok {
			e.AgentID = v
		}
	}
	if e.Reason == "" {
		if v, ok := raw["note"].(string); ok {
			e.Reason = v
		}
	}
	if e.Resource == "" {
		if v, ok := raw["resource"].(string); ok {
			e.Resource = v
		}
	}
	if e.Result == "" {
		if v, ok := raw["result"].(string); ok {
			e.Result = v
		}
	}
	if e.TaskID == "" {
		if v, ok := raw["task"].(string); ok {
			e.TaskID = v
		}
	}
}

// runAuditRepair re-chains persisted audit entries from the first broken
// link. It repairs self-inflicted breaks (e.g. the pre-lock concurrent-writer
// bug) but cannot distinguish those from genuine tampering, so it only runs
// on explicit user request.
func runAuditRepair(rest []string) {
	f, args := parseFlagsOrDie(rest)
	if len(args) > 0 {
		fatalUsage("audit repair: unexpected argument %q", args[0])
	}
	root := projectRoot(f)

	auditDir := filepath.Join(root, ".kern", "audit")
	if fi, err := os.Stat(auditDir); err != nil || !fi.IsDir() {
		fatal("no audit store at %s", auditDir)
	}

	l := governance.NewAuditLog().
		WithStore(storage.NewLog(auditDir)).
		WithLockPath(filepath.Join(auditDir, ".lock"))
	if _, err := l.Replay(); err != nil {
		fatal("audit repair: replay: %v", err)
	}
	n, err := l.RepairChain()
	if err != nil {
		fatal("audit repair: %v", err)
	}
	if n == 0 {
		fmt.Println("audit chain already verified (no repair needed)")
		return
	}
	fmt.Printf("repair: re-chained %d entry/entries; chain verified\n", n)
}
