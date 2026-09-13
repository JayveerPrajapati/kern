package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/app"
)

// runAgentMessage implements `kern agent-message`: sends a message to an
// agent's coordination inbox. CLI mirror of kern_agent_message — writes the
// same handoff record the MCP handler persists, so
// kern_agent_coordination action=inbox sees it.
func runAgentMessage(rest []string) {
	var to, from, taskID, msg, root string
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
	dir := filepath.Join(absRoot, ".kern", "coordination")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		panic(exitError{code: 1})
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
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		panic(exitError{code: 1})
	}
	path := filepath.Join(dir, handoff["id"].(string)+".json")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		panic(exitError{code: 1})
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
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		panic(exitError{code: 1})
	}
	ts := app.NewTaskService(p, nil)
	if err := ts.Cancel(taskID, reason); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		panic(exitError{code: 1})
	}
	fmt.Printf("task %s cancelled: %s\n", taskID, reason)
}