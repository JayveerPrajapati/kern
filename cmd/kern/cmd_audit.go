package main

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/JayveerPrajapati/kern/internal/governance/audit"
	"github.com/JayveerPrajapati/kern/internal/storage"
)

// runAudit implements `kern audit [task-id] [--root ROOT] [--json]`.
// With no task-id, shows all audit entries. With a task-id, shows entries for
// that task only.
//
// The audit log's All()/FilterByTask() only return in-memory entries, so a
// fresh process would see nothing. We read the persisted store directly
// instead: the running firewall (or any prior server) writes one file per entry
// under <root>/.kern/audit/.
func runAudit(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	root := f.root
	if root == "" {
		root = "."
	}

	auditDir := filepath.Join(root, ".kern", "audit")
	store := storage.NewLocal(auditDir)

	taskID := ""
	if len(args) > 0 {
		taskID = args[0]
	}

	entries := loadAuditEntries(store)
	if taskID != "" {
		entries = filterByTask(entries, taskID)
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

// loadAuditEntries reads every persisted audit entry from the store, one file
// per key. Entries whose files are missing/corrupt are skipped rather than
// aborting the listing.
func loadAuditEntries(store storage.Store) []audit.AuditEntry {
	ctx := context.Background()
	entries, err := store.List(ctx)
	if err != nil {
		fatal("%v", err)
	}
	var out []audit.AuditEntry
	for _, e := range entries {
		raw, err := store.Get(ctx, e.Key)
		if err != nil {
			continue
		}
		var entry audit.AuditEntry
		if err := storage.UnmarshalValue(raw, &entry); err != nil {
			continue
		}
		out = append(out, entry)
	}
	return out
}

// filterByTask returns only entries whose TaskID matches, preserving order.
func filterByTask(entries []audit.AuditEntry, taskID string) []audit.AuditEntry {
	var out []audit.AuditEntry
	for _, e := range entries {
		if e.TaskID == taskID {
			out = append(out, e)
		}
	}
	return out
}