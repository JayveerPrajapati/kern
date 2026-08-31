package main

import (
	"fmt"
	"time"

	"github.com/JayveerPrajapati/kern/internal/app"
)

// runApprove implements `kern approve [id] [--reject --reason "..." --approver "..."]`.
// With no args, lists pending approvals. With an ID, approves it.
// Use --reject to reject instead of approve.
func runApprove(rest []string) {
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

	if len(args) < 1 || args[0] == "" {
		// List pending approvals.
		pending, err := ts.PendingApprovals()
		if err != nil {
			fatal("%v", err)
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
		}
		return
	}

	id := args[0]
	approver := f.approver
	if approver == "" {
		approver = "cli-user"
	}

	if f.reject {
		_, err := ts.ResolveApproval(id, approver, false, f.reason)
		if err != nil {
			fatal("%v", err)
		}
		fmt.Printf("rejected: %s (by %s)\n", id, approver)
	} else {
		a, err := ts.ResolveApproval(id, approver, true, f.reason)
		if err != nil {
			fatal("%v", err)
		}
		fmt.Printf("approved: %s\n", a.ID)
		fmt.Printf("  task: %s\n", a.TaskID)
		fmt.Printf("  approver: %s\n", a.Approver)
		fmt.Printf("  decided: %s\n", a.DecidedAt.Format(time.RFC3339))
	}
}
