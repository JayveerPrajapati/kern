package mcp

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/app"
)

func TestAgentMessageSendsHandoff(t *testing.T) {
	s := NewServer(strings.NewReader(""), io.Discard)
	s.roots = []string{t.TempDir()}
	res, err := s.handleAgentMessage(context.Background(), map[string]any{
		"to_agent": "fixer-1",
		"notes":    "please re-check the tests",
		"root":     s.roots[0],
	})
	if err != nil {
		t.Fatalf("handleAgentMessage error: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(res), &parsed); err != nil {
		t.Fatalf("result should be JSON: %v", err)
	}
	if parsed["status"] != "created" {
		t.Errorf("expected status created, got %v", parsed["status"])
	}
	// The handoff must be visible in the target's inbox.
	inbox, err := s.handleAgentCoordination(context.Background(), map[string]any{
		"action":   "inbox",
		"agent_id": "fixer-1",
		"root":     s.roots[0],
		"format":   "json",
	})
	if err != nil {
		t.Fatalf("inbox error: %v", err)
	}
	if !strings.Contains(inbox, "please re-check the tests") {
		t.Errorf("inbox should contain the message: %s", inbox)
	}
}

func TestAgentMessageMissingFields(t *testing.T) {
	s := NewServer(strings.NewReader(""), io.Discard)
	if _, err := s.handleAgentMessage(context.Background(), map[string]any{}); err == nil {
		t.Fatal("expected error when to_agent missing")
	}
	if _, err := s.handleAgentMessage(context.Background(), map[string]any{"to_agent": "x"}); err == nil {
		t.Fatal("expected error when notes missing")
	}
}

func TestAgentInterruptCancelsTask(t *testing.T) {
	s := NewServer(strings.NewReader(""), io.Discard)
	root := t.TempDir()
	s.roots = []string{root}

	// Create a task through the app layer (the MCP surface creates tasks via
	// the high-level pipeline; the app TaskService is the authoritative path).
	p, err := app.New(root)
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	ts := app.NewTaskService(p, nil)
	task, err := ts.Create("fix the flaky test")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	res, err := s.handleAgentInterrupt(context.Background(), map[string]any{
		"task_id": task.ID,
		"reason":  "obsolete",
		"root":    root,
	})
	if err != nil {
		t.Fatalf("handleAgentInterrupt error: %v", err)
	}
	if !strings.Contains(res, "cancelled") {
		t.Errorf("expected cancelled status: %s", res)
	}

	// Interrupting a second time must fail (task is terminal).
	if _, err := s.handleAgentInterrupt(context.Background(), map[string]any{
		"task_id": task.ID,
		"root":    root,
	}); err == nil {
		t.Error("expected error interrupting an already-cancelled task")
	}
}

func TestAgentInterruptMissingTaskID(t *testing.T) {
	s := NewServer(strings.NewReader(""), io.Discard)
	if _, err := s.handleAgentInterrupt(context.Background(), map[string]any{}); err == nil {
		t.Fatal("expected error when task_id missing")
	}
}