package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"strings"

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
		fatalUsage("       kern task pause <id> [--root ROOT]")
		fatalUsage("       kern task efficiency <id> [--root ROOT]")
	}

	// Subcommands: resume, replay, cancel, retry, pause, efficiency.
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
		case "pause":
			runTaskPause(root, args[1])
			return
		case "efficiency":
			runTaskEfficiency(root, args[1])
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
// history and records the metadata (repo version, model, config hash) needed
// to interpret the replay (Phase 16.3).
func runTaskReplay(root, id string) {
	p, err := app.New(root)
	if err != nil {
		fatal("%v", err)
	}
	ts := app.NewTaskService(p, eventbus.New())
	if ts.Snapshots() == nil {
		fatal("snapshot store not available")
	}

	// Phase 16.3: capture the replay metadata. Best-effort — on any error the
	// field falls back to a sensible default and replay still proceeds.
	repoVersion := "unknown"
	if out, err := gitDiffC(root, "rev-parse", "HEAD"); err == nil {
		if v := strings.TrimSpace(string(out)); v != "" {
			repoVersion = v
		}
	}
	model := os.Getenv("KERN_MODEL")
	if model == "" {
		model = "default"
	}
	sum := sha256.Sum256([]byte(root + ":" + id))
	configHash := fmt.Sprintf("%x", sum[:])[:16]

	rec, err := ts.ReplayTask(id, repoVersion, model, configHash)
	if err != nil {
		fatal("%v", err)
	}
	fmt.Printf("replay record: task_id=%s repo_version=%s model=%s config_hash=%s replayed_at=%s\n",
		rec.TaskID, rec.RepoVersion, rec.Model, rec.ConfigHash, rec.ReplayedAt.Format("2006-01-02 15:04:05"))

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

// runTaskPause implements `kern task pause <id>`.
func runTaskPause(root, id string) {
	p, err := app.New(root)
	if err != nil {
		fatal("%v", err)
	}
	ts := app.NewTaskService(p, eventbus.New())
	if err := ts.Pause(id, "user requested"); err != nil {
		fatal("%v", err)
	}
	fmt.Printf("paused task %s\n", id)
}

// runTaskEfficiency implements `kern task efficiency <id>` (Phase 17.6): it
// renders the consolidated context-quality + task-outcome report for a task.
func runTaskEfficiency(root, id string) {
	p, err := app.New(root)
	if err != nil {
		fatal("%v", err)
	}
	ts := app.NewTaskService(p, eventbus.New())
	t, ok := ts.Get(id)
	if !ok {
		fatal("task not found: %s", id)
	}
	report := app.BuildEfficiencyReport(t)
	fmt.Print(app.RenderEfficiencyReport(report))
}

// runEfficiency implements the top-level `kern efficiency <id>` (Phase 17.6),
// which delegates to the same efficiency report renderer as `kern task
// efficiency <id>`. It parses the standard --root flag so callers can target a
// different workspace.
func runEfficiency(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	root := f.root
	if root == "" {
		root = "."
	}
	if len(args) < 1 || args[0] == "" {
		fatalUsage("usage: kern efficiency <id> [--root ROOT]")
		return
	}
	runTaskEfficiency(root, args[0])
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
