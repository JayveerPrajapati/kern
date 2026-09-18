package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/app"
	"github.com/JayveerPrajapati/kern/internal/governance"
)

// runAgentMessage implements `kern agent-message`: sends a message to an
// agent's coordination inbox. CLI mirror of kern_agent_message — writes the
// same handoff record the MCP handler persists, so
// kern_agent_coordination action=inbox sees it.
func runAgentMessage(rest []string) {
	var to, from, taskID, msg, root string
	taskExplicit := false // true only when the caller passed --task (an auto-generated id must not be validated)
	var words []string
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case "--to":
			if i+1 < len(rest) {
				to = rest[i+1]
				i++
			}
		case "--from":
			if i+1 < len(rest) {
				from = rest[i+1]
				i++
			}
		case "--task":
			if i+1 < len(rest) {
				taskID = rest[i+1]
				taskExplicit = true
				i++
			}
		case "--root", "-r":
			if i+1 < len(rest) {
				root = rest[i+1]
				i++
			}
		default:
			if strings.HasPrefix(rest[i], "-") {
				fatalUsage("unknown agent-message flag %q", rest[i])
			}
			words = append(words, rest[i])
		}
	}
	msg = strings.TrimSpace(strings.Join(words, " "))
	if to == "" || msg == "" {
		fatalUsage("usage: kern agent-message --to <agent> [--from <agent>] [--task <id>] \"<message>\"")
	}
	if from == "" {
		from = "model"
	}
	if taskID == "" {
		taskID = fmt.Sprintf("task-%d", time.Now().UnixNano()%100000)
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		fatalUsage("%v", err)
	}
	// Recipient validation against the governance agent registry (when one
	// exists). A registry that is absent or empty keeps the open-inbox
	// behavior (cold-start handoffs) but says so on stderr; a non-empty
	// registry rejects unknown recipients loudly instead of silently
	// queueing a message nobody will read.
	_ = governance.LoadAgents(absRoot)
	registered := governance.ListAgents()
	if len(registered) > 0 {
		known := false
		ids := make([]string, 0, len(registered))
		for _, a := range registered {
			if a.ID == to {
				known = true
			}
			ids = append(ids, a.ID)
		}
		if !known {
			fatal("agent-message: recipient %q is not a registered agent (registered: %s)", to, strings.Join(ids, ", "))
		}
	} else {
		fmt.Fprintln(os.Stderr, "kern agent-message: warning: no registered agents in this workspace — queuing to open inbox")
	}
	// Task validation against the agent task registry: a --task naming an
	// unknown task id is a caller error — fail BEFORE queueing instead of
	// silently creating a handoff that references a task that does not exist.
	// Uses the same TaskService lookup kern agent-interrupt performs (the
	// registry in memory first, then the persisted store).
	if taskExplicit {
		p, err := app.New(absRoot)
		if err != nil {
			fatal("agent-message: %v", err)
		}
		ts := app.NewTaskService(p, nil)
		if _, ok := ts.Get(taskID); !ok {
			fatal("agent-message: task %q not found", taskID)
		}
	}
	dir := filepath.Join(absRoot, ".kern", "coordination")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fatal("agent-message: %v", err)
	}
	handoff := map[string]any{
		"id":         fmt.Sprintf("hf-%d", time.Now().UnixNano()%100000),
		"from_agent": from,
		"to_agent":   to,
		"task_id":    taskID,
		"created_at": time.Now().UTC(),
		"notes":      msg,
		"status":     "pending",
	}
	b, err := json.MarshalIndent(handoff, "", "  ")
	if err != nil {
		fatal("agent-message: %v", err)
	}
	path := filepath.Join(dir, handoff["id"].(string)+".json")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		fatal("agent-message: %v", err)
	}
	fmt.Printf("queued message to %q (handoff %s)\n", to, handoff["id"].(string))
}

// runAgentInterrupt implements `kern agent-interrupt`: cancels a running
// task through the TaskService. CLI mirror of kern_agent_interrupt.
func runAgentInterrupt(rest []string) {
	var taskID, root string
	var words []string
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case "--root", "-r":
			if i+1 < len(rest) {
				root = rest[i+1]
				i++
			}
		default:
			if strings.HasPrefix(rest[i], "-") {
				fatalUsage("unknown agent-interrupt flag %q", rest[i])
			}
			words = append(words, rest[i])
		}
	}
	if len(words) == 0 {
		fatalUsage("usage: kern agent-interrupt <task-id> [reason words...]")
	}
	taskID = words[0]
	reason := strings.TrimSpace(strings.Join(words[1:], " "))
	if reason == "" {
		reason = "interrupted via kern agent-interrupt"
	}
	p, err := app.New(root)
	if err != nil {
		fatal("agent-interrupt: %v", err)
	}
	ts := app.NewTaskService(p, nil)
	if err := ts.Cancel(taskID, reason); err != nil {
		fatal("agent-interrupt: %v", err)
	}
	fmt.Printf("task %s cancelled: %s\n", taskID, reason)
}
