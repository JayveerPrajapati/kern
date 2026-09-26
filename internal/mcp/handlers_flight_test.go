package mcp

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/flight"
)

// TestHandleFlightRequiresTask pins the kern_flight contract: the task
// argument is mandatory and rejected before any store access.
func TestHandleFlightRequiresTask(t *testing.T) {
	t.Parallel()
	s := newTestServer()
	_, err := s.handleFlight(context.Background(), map[string]any{"root": "."})
	if err == nil || !strings.Contains(err.Error(), "task is required") {
		t.Fatalf("missing task: got err %v, want rejection with 'task is required'", err)
	}
}

// TestHandleFlightReplaysTrail records a flight trail into a temp project
// root and verifies the handler replays it end to end (the C3 reader).
func TestHandleFlightReplaysTrail(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	rec := flight.New(root)
	base := time.Now().Add(-5 * time.Minute).UTC()
	for _, r := range []flight.Record{
		{AgentID: "agentA", TaskID: "task-42", Action: "task_started", Context: "fix the N+1", Status: "ok", Timestamp: base},
		{AgentID: "agentA", TaskID: "task-42", Action: "tool_called", Arguments: "kern_search n+1", Result: "3 symbols", Status: "ok", Timestamp: base.Add(time.Minute)},
		{AgentID: "agentA", TaskID: "task-42", Action: "change_accepted", Status: "ok", Approved: true, Timestamp: base.Add(2 * time.Minute)},
	} {
		if _, err := rec.Record(r); err != nil {
			t.Fatalf("Record: %v", err)
		}
	}

	s := NewServer(strings.NewReader(""), io.Discard)
	out, err := s.handleFlight(context.Background(), map[string]any{"root": root, "task": "task-42"})
	if err != nil {
		t.Fatalf("handleFlight: %v", err)
	}
	for _, want := range []string{"flight trail for task task-42 (3 records)", "task_started by agentA", "kern_search n+1", "approved: yes"} {
		if !strings.Contains(out, want) {
			t.Errorf("handleFlight output missing %q in:\n%s", want, out)
		}
	}
}
