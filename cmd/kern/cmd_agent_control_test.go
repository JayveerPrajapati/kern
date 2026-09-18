package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/app"
	"github.com/JayveerPrajapati/kern/internal/governance"
)

// agentRegistryFixture writes a governance agent registry at
// <root>/.kern/agents.json containing exactly one agent (qa-1), marshaled the
// same way LoadAgents unmarshals it (governance.AgentIdentity JSON).
func agentRegistryFixture(t *testing.T, root string) {
	t.Helper()
	agents := []*governance.AgentIdentity{governance.NewAgent("qa-1", "QA Agent", "qa", nil)}
	b, err := json.Marshal(agents)
	if err != nil {
		t.Fatalf("marshal agents: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".kern"), 0o755); err != nil {
		t.Fatalf("mkdir .kern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".kern", "agents.json"), b, 0o644); err != nil {
		t.Fatalf("write agents.json: %v", err)
	}
}

// TestAgentMessageRejectsUnknownRecipient locks FIX B: with a non-empty
// agent registry, a message to an unregistered recipient must exit 1 loudly
// instead of silently queueing to an inbox nobody reads.
func TestAgentMessageRejectsUnknownRecipient(t *testing.T) {
	root := newRoot(t)
	agentRegistryFixture(t, root)
	expectExit(t, 1, func() {
		runAgentMessage([]string{"--root", root, "--to", "qa-2", "hi"})
	})
}

// TestAgentMessageKnownRecipientQueues locks the happy path with a registered
// recipient: the message is queued (rc 0) and a handoff file is created
// under <root>/.kern/coordination/.
func TestAgentMessageKnownRecipientQueues(t *testing.T) {
	root := newRoot(t)
	agentRegistryFixture(t, root)
	out := captureStdout(t, func() {
		runAgentMessage([]string{"--root", root, "--to", "qa-1", "hi"})
	})
	if !strings.Contains(out, `queued message to "qa-1"`) {
		t.Fatalf("output = %q, want queued message", out)
	}
	entries, err := os.ReadDir(filepath.Join(root, ".kern", "coordination"))
	if err != nil {
		t.Fatalf("coordination dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 handoff file, got %d", len(entries))
	}
}

// TestAgentMessageRejectsUnknownTask locks the QA finding: with --task naming
// an id that does not exist in the task registry, the command must exit 1
// loudly BEFORE queueing — no handoff file may be created.
func TestAgentMessageRejectsUnknownTask(t *testing.T) {
	root := newRoot(t)
	agentRegistryFixture(t, root)
	expectExit(t, 1, func() {
		runAgentMessage([]string{"--root", root, "--to", "qa-1", "--task", "nonexistent-task-xyz", "hello"})
	})
	if _, err := os.Stat(filepath.Join(root, ".kern", "coordination")); err == nil {
		t.Fatal("coordination dir must not exist when task validation fails")
	}
}

// TestAgentMessageKnownTaskQueues locks the happy path with a --task that
// exists in the task registry: the message is queued (rc 0), a handoff file
// is created, and the handoff carries the given task id.
func TestAgentMessageKnownTaskQueues(t *testing.T) {
	root := newRoot(t)
	agentRegistryFixture(t, root)
	p, err := app.New(root)
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	ts := app.NewTaskService(p, nil)
	task, err := ts.Create("agent-message task validation")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	out := captureStdout(t, func() {
		runAgentMessage([]string{"--root", root, "--to", "qa-1", "--task", task.ID, "hello"})
	})
	if !strings.Contains(out, `queued message to "qa-1"`) {
		t.Fatalf("output = %q, want queued message", out)
	}
	entries, err := os.ReadDir(filepath.Join(root, ".kern", "coordination"))
	if err != nil {
		t.Fatalf("coordination dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 handoff file, got %d", len(entries))
	}
	b, err := os.ReadFile(filepath.Join(root, ".kern", "coordination", entries[0].Name()))
	if err != nil {
		t.Fatalf("read handoff: %v", err)
	}
	var handoff map[string]any
	if err := json.Unmarshal(b, &handoff); err != nil {
		t.Fatalf("handoff not json: %v", err)
	}
	if handoff["task_id"] != task.ID {
		t.Errorf("handoff task_id = %v, want %s", handoff["task_id"], task.ID)
	}
}
