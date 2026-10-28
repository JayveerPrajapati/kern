package approvalcli

import (
	"context"
	"encoding/json"
	"fmt"
	stdlog "log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/cliflags"
	kctx "github.com/JayveerPrajapati/kern/internal/context"
	"github.com/JayveerPrajapati/kern/internal/govaudit"
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
		case "rotate":
			runAuditRotate(rest[1:])
			return
		}
	}

	f, args := parseFlagsOrDie(rest)
	root := projectRoot(f)

	// NIT-18a: --limit -1 (like any negative) is junk input, not "all" —
	// 0 is the documented everything cap. Reject as a usage error (exit 2),
	// matching the parser's fail-loud treatment of non-numeric values.
	if f.limitSet && f.limit < 0 {
		fatalUsage("--limit: invalid integer %d (0 shows all entries)", f.limit)
	}

	// `kern audit .` (and any positional directory) is a root, not a task ID:
	// most kern commands accept a positional root, so treating "." as a task
	// produced a misleading "no audit entries for task ." result.
	if f.root == "" && len(args) > 0 {
		if info, err := os.Stat(args[0]); err == nil && info.IsDir() {
			root = args[0]
			args = args[1:]
		}
	}
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
		l := govaudit.NewAuditLog().
			WithStore(storage.NewLog(auditDir)).
			WithLockPath(filepath.Join(auditDir, ".lock")).
			WithMirror(govaudit.MirrorFor(auditDir))
		if _, err := l.Replay(); err == nil {
			all := l.All()
			if brk, verified := l.VerifyChainReport(); brk >= 0 {
				tiers := l.ChainTierReport()
				// NEW-5 tier-aware verification (mirrors platform startup):
				// a break fully explained by format downgrades is an
				// environment-degraded write, not tampering — report it
				// accurately instead of "FAILED".
				if tiers.DowngradeExplainsBreak() {
					if tiers.TailDowngraded > 0 {
						fmt.Fprintf(os.Stderr, "WARNING: audit chain: %d entr%s starting at entry %d lack HMAC — written by an environment-degraded server (audit secret unavailable at write time). Ordering is verified; those entries' authenticity is not. They stay unkeyed by design — `kern audit repair` re-chains only genuinely broken links, never re-signs an entry of unknown origin\n", tiers.TailDowngraded, map[bool]string{true: "y", false: "ies"}[tiers.TailDowngraded == 1], tiers.FirstTailDowngraded+1)
					}
				} else if verified == 0 {
					fmt.Fprintf(os.Stderr, "WARNING: audit log chain verification could not verify ANY entries (0 of %d) — this indicates either a legacy version migration or a full-chain rewrite; if this is not a known upgrade, investigate before trusting governance records (kern audit repair / kern evidence verify)\n", len(all))
				} else {
					fmt.Fprintf(os.Stderr, "WARNING: audit log tamper chain verification FAILED at entry %d of %d — governance records may have been modified; investigate before trusting them (kern audit repair / kern evidence verify)\n", brk+1, len(all))
					if tiers.Downgraded > 0 {
						fmt.Fprintf(os.Stderr, "WARNING: %d entr%s verify only under a weaker unkeyed hash format than earlier keyed entries — a format downgrade, which is how a forged entry would look (or the HMAC key was unavailable when they were written)\n", tiers.Downgraded, map[bool]string{true: "y", false: "ies"}[tiers.Downgraded == 1])
					}
				}
			}
			// Surface degraded chain integrity: when the HMAC secret is
			// unavailable, hashes fall back to plain SHA-256 and
			// VerifyChainReport still passes, so the degradation must be
			// visible here rather than silent (the write path deliberately
			// never loses entries over an unavailable key).
			// Entries that verify only under an unkeyed SHA-256 formula prove
			// ordering, not authenticity: anyone able to write the store can
			// recompute them. Say so instead of presenting them as keyed.
			if tiers := l.ChainTierReport(); tiers.Unkeyed() > 0 && tiers.Broken == 0 {
				fmt.Fprintf(os.Stderr, "NOTE: audit integrity: %d of %d entries are HMAC-keyed; %d verify only under an unkeyed formula (written before keyed hashing or while the key was unavailable) and could have been forged by a writer with store access\n", tiers.Keyed, tiers.Total, tiers.Unkeyed())
			}
			if l.IntegrityMode() == "plain" {
				fmt.Fprintf(os.Stderr, "WARNING: audit chain HMAC secret unavailable — hashes are plain SHA-256 (full-chain-rewrite tamper protection is OFF); restore the key at <UserConfigDir>/kern/audit-chain.key, then run `kern audit repair`\n")
			}
		}
	}
	// NIT#34: `kern audit` dumped the whole chain to stdout unbounded (4.3 MB
	// observed on a mature repo). Default to the 50 most recent entries;
	// --limit N (alias --tail N) changes the cap, --limit 0 returns everything.
	// The chain verification above always runs over the FULL chain — only the
	// display is bounded.
	limit := 50
	if f.limitSet {
		limit = f.limit
	}
	all := entries
	if limit > 0 && len(all) > limit {
		entries = all[len(all)-limit:]
		if !f.json {
			fmt.Fprintf(os.Stderr, "showing %d most recent of %d entries (kern audit --limit N to change, --limit 0 for all)\n", limit, len(all))
		}
	}
	if f.json {
		printJSON(map[string]any{
			"entries": entries,
			"total":   len(all),
			"limit":   limit,
			"chain":   auditChainJSON(root, all),
		})
		return
	}

	fmt.Printf("%-22s %-14s %-12s %-20s %-8s %-7s %s\n", "TIME", "AGENT", "ACTION", "RESOURCE", "APPROVED", "TOOK", "RESULT")
	for _, e := range entries {
		approved := "no"
		if e.Approved {
			approved = "yes"
		}
		// Latency visibility L3: tool_call entries carry DurationMs on the
		// tamper-evident chain; older/legacy entries show a dash.
		took := ""
		if e.DurationMs > 0 {
			took = fmt.Sprintf("%.1fs", float64(e.DurationMs)/1000)
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
		fmt.Printf("%-22s %-14s %-12s %-20s %-8s %-7s %s\n",
			e.Timestamp.Format("2006-01-02 15:04:05"),
			cellOrDash(agent),
			cellOrDash(action),
			cellOrDash(resource),
			approved,
			cellOrDash(took),
			cellOrDash(result),
		)
	}
}

// auditChainJSON returns the tamper-chain diagnostic block for `kern audit
// --json`: tier counts (keyed/plain/legacy/broken/downgraded), the first
// broken entry index, and whether any entries carry a write-path Annotation
// (recorded by persist when it proceeded unlocked or degraded to plain
// hashing). This gives the same discrimination the text warnings provide,
// machine-readable, so a script can tell a benign format downgrade apart
// from genuine tampering without grepping stderr.
func auditChainJSON(root string, entries []govaudit.AuditEntry) map[string]any {
	out := map[string]any{
		"integrity_mode":   "unknown",
		"entries_total":    len(entries),
		"broken":           false,
		"first_broken":     nil,
		"degraded_entries": nil,
	}
	auditDir := filepath.Join(root, ".kern", "audit")
	if !isDir(auditDir) {
		return out
	}
	l := govaudit.NewAuditLog().
		WithStore(storage.NewLog(auditDir)).
		WithLockPath(filepath.Join(auditDir, ".lock")).
		WithMirror(govaudit.MirrorFor(auditDir))
	if _, err := l.Replay(); err != nil {
		out["replay_error"] = err.Error()
		return out
	}
	out["integrity_mode"] = l.IntegrityMode()
	tiers := l.ChainTierReport()
	out["tiers"] = map[string]int{
		"total":      tiers.Total,
		"keyed":      tiers.Keyed,
		"plain":      tiers.Plain,
		"legacy":     tiers.Legacy,
		"broken":     tiers.Broken,
		"downgraded": tiers.Downgraded,
	}
	if brk, _ := l.VerifyChainReport(); brk >= 0 {
		out["broken"] = true
		out["first_broken"] = brk + 1
	}
	// Surface recorded write-path degradations (Annotation on the persisted
	// entries) so a broken chain names its likely cause.
	var annotated []string
	for _, e := range l.All() {
		if e.Annotation != "" {
			annotated = append(annotated, e.ID+": "+e.Annotation)
		}
	}
	if len(annotated) > 0 {
		out["degraded_entries"] = annotated
	}
	return out
}

// cellOrDash renders an empty table cell as "-" so an entry never shows as a
// fully blank row.
func cellOrDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// (status approved/rejected in .blueprint/approvals/requests.jsonl) into
// governance-shaped audit rows. Best-effort and read-only: a missing or
// unreadable approval store yields no entries, never an error. Entries the
// governance chain already records for the same decision (Action approve/
// reject with Resource "approval:<id>", e.g. task-gated approvals) are
// skipped so a decision is never shown twice.

// runAuditAppend links an external entry into the tamper-evident audit chain.
// runAuditRotate archives the active audit store under .kern/audit and starts
// a fresh chain, keeping the newest --keep archives (default 3). The audit
// chain grows without bound otherwise (23 MB / 53k entries observed
// 2026-10-26); rotation is the bounded-retention path that preserves history
// on disk instead of truncating it.
func runAuditRotate(rest []string) {
	keep := 3
	root := ""
	jsonOut := false
	for i := 0; i < len(rest); i++ {
		arg := rest[i]
		switch {
		case arg == "--keep" || arg == "--root":
			if i+1 >= len(rest) {
				fatalUsage("audit rotate: %s requires a value", arg)
			}
			i++
			if arg == "--keep" {
				n, err := strconv.Atoi(rest[i])
				if err != nil || n < 0 {
					fatalUsage("audit rotate: --keep must be a non-negative integer")
				}
				keep = n
			} else {
				root = rest[i]
			}
		case strings.HasPrefix(arg, "--keep="):
			n, err := strconv.Atoi(strings.TrimPrefix(arg, "--keep="))
			if err != nil || n < 0 {
				fatalUsage("audit rotate: --keep must be a non-negative integer")
			}
			keep = n
		case strings.HasPrefix(arg, "--root="):
			root = strings.TrimPrefix(arg, "--root=")
		case arg == "--json":
			jsonOut = true
		case strings.HasPrefix(arg, "-"):
			fatalUsage("audit rotate: unknown flag %s", arg)
		default:
			root = arg
		}
	}
	if root == "" {
		wd, err := os.Getwd()
		if err != nil {
			fatal("Audit rotate: %v", err)
		}
		root = wd
	}
	dest, err := govaudit.ArchiveAuditStore(root, keep)
	if err != nil {
		fatal("Audit rotate: %v", err)
	}
	if jsonOut {
		b, _ := json.Marshal(map[string]any{"archived": dest, "keep": keep})
		fmt.Println(string(b))
		return
	}
	fmt.Printf("audit: archived to %s (keeping %d archive(s)); the chain restarts empty and keyed\n", dest, keep)
}

func runAuditAppend(rest []string) {
	f, args := parseFlagsOrDie(rest)
	if len(args) > 0 {
		fatalUsage("audit append: unexpected argument %q (entry JSON comes from stdin or --file)", args[0])
	}
	root := projectRoot(f)

	var entry govaudit.AuditEntry
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
		data, err := cliflags.ReadStdin()
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
	log := govaudit.NewAuditLog().WithStore(store).WithLockPath(filepath.Join(auditDir, ".lock")).WithMirror(govaudit.MirrorFor(auditDir))
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
func applyAuditEntryAliases(e *govaudit.AuditEntry, raw map[string]any) {
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

	l := govaudit.NewAuditLog().
		WithStore(storage.NewLog(auditDir)).
		WithLockPath(filepath.Join(auditDir, ".lock")).
		WithMirror(govaudit.MirrorFor(auditDir))
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
