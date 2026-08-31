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

// auditInvalidate is the invalidation seam for P0.4's blueprint validation
// consumption: a BLOCK/ERROR validation outcome marks the blocked context
// entities stale. Production behavior is in-memory invalidation (no
// persistence); tests replace it to observe the call.
var auditInvalidate = func(entities []string, reason, source string, at time.Time) []kctx.InvalidationMarker {
	return kctx.InvalidateContext(entities, reason, source, at)
}

// runAudit implements `kern audit [task-id] [--root ROOT] [--json]`.
// With no task-id, shows all audit entries. With a task-id, shows entries for
// that task only.
// It reads the persisted trail through TaskService (one file per entry under
// <root>/.kern/audit/): the audit log's All() only returns in-memory entries,
// so a fresh process would otherwise see nothing. TaskService owns the
// persisted read so the CLI and MCP surface the same authoritative trail.
// `kern audit append` (see runAuditAppend) links an externally-authored entry
// into the chain instead of listing.
func runAudit(rest []string) {
	if len(rest) > 0 && rest[0] == "append" {
		runAuditAppend(rest[1:])
		return
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

// runAuditAppend implements `kern audit append [--root ROOT] [--file PATH]`,
// which links one externally-authored AuditEntry into the tamper-evident hash
// chain. The entry JSON is read from stdin (or from --file), Replay() loads
// the existing chain head from <root>/.kern/audit/, and AppendExternal chains
// the new entry onto it (first entry in a fresh chain links from ""). On
// success the assigned ID and chain hash are printed and the process exits 0;
// usage/parse errors exit 2, persistence failures exit 1. This is the
// external-append path Blueprint's audit writer uses so its records join
// kern's chain without corrupting it.
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

	store := storage.NewLocal(filepath.Join(root, ".kern", "audit"))
	log := governance.NewAuditLog().WithStore(store)
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
