package main

import (
	"fmt"
	"github.com/JayveerPrajapati/kern/internal/app"
	"github.com/JayveerPrajapati/kern/internal/eventbus"
)

// runTask implements `kern task <id>` — the human-facing control surface for
// querying task state. It looks up a task by ID (from the persisted store, so
// tasks from prior sessions are retrievable) and prints its state, intent,
// steps, and lifecycle results.
//
// This is Phase 2's "Task is authoritative" CLI surface: every analyze/what-if/
// verify that creates a Task can be queried here afterward.
func runTask(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	root := f.root
	if root == "" {
		root = "."
	}
	if len(args) < 1 || args[0] == "" {
		fatalUsage("usage: kern task <id> [--root ROOT]")
		fatalUsage("       kern task resume <id> [--root ROOT]")
		fatalUsage("       kern task replay <id> [--root ROOT]")
	}

	// Subcommands: resume, replay, cancel, retry.
	if len(args) >= 2 {
		switch args[0] {
		case "resume":
			runTaskResume(root, args[1])
			return
		case "replay":
			runTaskReplay(root, args[1])
			return
		case "cancel":
			runTaskCancel(root, args[1])
			return
		case "retry":
			runTaskRetry(root, args[1])
			return
		}
	}

	id := args[0]

	p, err := app.New(root)
	if err != nil {
		fatal("%v", err)
	}
	ts := app.NewTaskService(p, eventbus.New()).WithPRProvider(app.AutoPRProvider())
	t, ok := ts.Get(id)
	if !ok {
		fatal("task not found: %s", id)
	}

	fmt.Printf("task: %s\n", t.ID)
	fmt.Printf("state: %s\n", t.State)
	fmt.Printf("type: %s\n", t.Type)
	if t.Intent != "" {
		fmt.Printf("intent: %s\n", t.Intent)
	}
	if t.Input != "" && t.Input != t.Intent {
		fmt.Printf("input: %s\n", t.Input)
	}
	fmt.Printf("created: %s\n", t.CreatedAt.Format("2006-01-02 15:04:05"))
	fmt.Printf("updated: %s\n", t.UpdatedAt.Format("2006-01-02 15:04:05"))
	if t.AgentID != "" {
		fmt.Printf("agent: %s\n", t.AgentID)
	}
	if len(t.Steps) > 0 {
		fmt.Printf("steps: %d\n", len(t.Steps))
		for _, s := range t.Steps {
			status := s.Status
			if status == "" {
				status = "?"
			}
			fmt.Printf("  %d. %s [%s] %s\n", s.Index, s.Action, status, s.Result)
		}
	}
	if t.ContextPacket != nil {
		pkt := t.ContextPacket
		fmt.Printf("context packet: %d symbols, %d files, %d risks\n", len(pkt.Symbols), len(pkt.Files), len(pkt.Risks))
	}
	if t.ImpactReport != nil {
		imp := t.ImpactReport
		fmt.Printf("impact: %d affected, %d files, risk=%s\n", len(imp.Affected), len(imp.Files), imp.Risk)
	}
	if t.Verification != nil {
		fmt.Printf("verification: verdict=%s summary=%s\n", t.Verification.Verdict, t.Verification.Summary)
	}
	if len(t.Risks) > 0 {
		fmt.Printf("risks: %d\n", len(t.Risks))
		for _, r := range t.Risks {
			fmt.Printf("  %s\n", r.Level)
		}
	}
	if len(t.Evidence) > 0 {
		fmt.Printf("evidence: %d claims\n", len(t.Evidence))
	}
}

// runTaskResume implements `kern task resume <id>` — resumes a BLOCKED task.
func runTaskResume(root, id string) {
	p, err := app.New(root)
	if err != nil {
		fatal("%v", err)
	}
	ts := app.NewTaskService(p, eventbus.New())
	t, err := ts.Resume(id)
	if err != nil {
		fatal("%v", err)
	}
	fmt.Printf("resumed task %s: state=%s\n", t.ID, t.State)
}

// runTaskReplay implements `kern task replay <id>` — shows the task's snapshot
// history (the artifact chain can be replayed via `kern artifacts <id>`).
func runTaskReplay(root, id string) {
	p, err := app.New(root)
	if err != nil {
		fatal("%v", err)
	}
	ts := app.NewTaskService(p, eventbus.New())
	if ts.Snapshots() == nil {
		fatal("snapshot store not available")
	}
	history, err := ts.Snapshots().History(id)
	if err != nil {
		fatal("%v", err)
	}
	if len(history) == 0 {
		fmt.Printf("no snapshots for task %s\n", id)
		return
	}
	fmt.Printf("snapshot history for task %s (%d snapshots):\n", id, len(history))
	for i, snap := range history {
		fmt.Printf("  %d. state=%s agent=%s time=%s\n", i+1, snap.State, snap.AgentID, snap.Timestamp.Format("2006-01-02 15:04:05"))
	}
}

// runTaskCancel implements `kern task cancel <id>`.
func runTaskCancel(root, id string) {
	p, err := app.New(root)
	if err != nil {
		fatal("%v", err)
	}
	ts := app.NewTaskService(p, eventbus.New())
	if err := ts.Cancel(id, "user requested"); err != nil {
		fatal("%v", err)
	}
	fmt.Printf("cancelled task %s\n", id)
}

// runTaskRetry implements `kern task retry <id>`.
func runTaskRetry(root, id string) {
	p, err := app.New(root)
	if err != nil {
		fatal("%v", err)
	}
	ts := app.NewTaskService(p, eventbus.New())
	t, err := ts.Retry(id)
	if err != nil {
		fatal("%v", err)
	}
	fmt.Printf("retried task %s: state=%s\n", t.ID, t.State)
}
