package main

import (
	"encoding/json"
	"fmt"
	stdlog "log"
	"os"
	"path/filepath"
	"time"

	"github.com/JayveerPrajapati/kern/internal/app"
	kctx "github.com/JayveerPrajapati/kern/internal/context"
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

	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	root := f.root
	if root == "" {
		root = "."
	}

	p, err := app.New(root)
	if err != nil {
		fatal("%v", err)
	}
	ts := app.NewTaskService(p, nil)

	taskID := ""
	if len(args) > 0 {
		taskID = args[0]
	}

	var entries []governance.AuditEntry
	if taskID != "" {
		entries, err = ts.AuditEntriesForTask(taskID)
	} else {
		entries, err = ts.AuditEntries()
	}
	if err != nil {
		fatal("%v", err)
	}

	if len(entries) == 0 {
		if taskID != "" {
			fmt.Printf("no audit entries for task %s\n", taskID)
		} else {
			fmt.Println("no audit entries")
		}
		return
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
		result := e.Result
		if len(result) > 40 {
			result = result[:37] + "..."
		}
		fmt.Printf("%-22s %-14s %-12s %-20s %-8s %s\n",
			e.Timestamp.Format("2006-01-02 15:04:05"),
			e.AgentID,
			e.Action,
			e.Resource,
			approved,
			result,
		)
	}
}

// runAuditAppend links an external entry into the tamper-evident audit chain.
func runAuditAppend(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	if len(args) > 0 {
		fatalUsage("audit append: unexpected argument %q (entry JSON comes from stdin or --file)", args[0])
	}
	root := f.root
	if root == "" {
		root = "."
	}

	var entry governance.AuditEntry
	switch {
	case f.file != "":
		data, err := os.ReadFile(f.file)
		if err != nil {
			fatal("audit append: read entry file: %v", err)
		}
		if err := json.Unmarshal(data, &entry); err != nil {
			fatalUsage("audit append: invalid AuditEntry JSON in %s: %v", f.file, err)
		}
	default:
		data, err := readStdin()
		if err != nil {
			fatal("audit append: read stdin: %v", err)
		}
		if err := json.Unmarshal(data, &entry); err != nil {
			fatalUsage("audit append: invalid AuditEntry JSON on stdin: %v", err)
		}
	}

	auditDir := filepath.Join(root, ".kern", "audit")
	store := storage.NewLog(auditDir)
	log := governance.NewAuditLog().WithStore(store).WithLockPath(filepath.Join(auditDir, ".lock"))
	if _, err := log.Replay(); err != nil {
		fatal("audit append: replay: %v", err)
	}
	if err := log.AppendExternal(entry); err != nil {
		fatal("audit append: %v", err)
	}

	// P0.4: consume Blueprint's validation outcome. BLOCK/ERROR marks the
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

// runAuditRepair re-chains persisted audit entries from the first broken
// link. It repairs self-inflicted breaks (e.g. the pre-lock concurrent-writer
// bug) but cannot distinguish those from genuine tampering, so it only runs
// on explicit user request.
func runAuditRepair(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	if len(args) > 0 {
		fatalUsage("audit repair: unexpected argument %q", args[0])
	}
	root := f.root
	if root == "" {
		root = "."
	}

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
