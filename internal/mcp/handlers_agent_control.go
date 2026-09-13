package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/JayveerPrajapati/kern/internal/app"
)

// handleAgentMessage implements kern_agent_message: the model sends a
// message to an agent's coordination inbox. Wraps the coordination handoff
// primitive (ToAgent = target, Notes = message) with the model as the
// default sender, so a running agent observing its inbox sees the directive.
func (s *Server) handleAgentMessage(ctx context.Context, args map[string]any) (string, error) {
	to := argString(args, "to_agent")
	if to == "" {
		return "", fmt.Errorf("to_agent is required")
	}
	notes := argString(args, "notes")
	if notes == "" {
		return "", fmt.Errorf("notes (the message) is required")
	}
	root := resolveRoot(argString(args, "root"))
	from := argString(args, "from_agent")
	if from == "" {
		from = "model"
	}

	coordMu.Lock()
	if _, ok := activeHandoffs[root]; !ok {
		activeHandoffs[root] = []AgentHandoff{}
	}
	if _, ok := activeClaims[root]; !ok {
		activeClaims[root] = map[string]ResourceClaim{}
	}
	coordMu.Unlock()

	send := map[string]any{
		"from_agent": from,
		"to_agent":   to,
		"notes":      notes,
	}
	if v := argString(args, "task_id"); v != "" {
		send["task_id"] = v
	}
	return s.coordHandoff(root, time.Now().UTC(), from, "json", send)
}

// handleAgentInterrupt implements kern_agent_interrupt: cancels a running
// task by ID through the TaskService (the real cancel path — task
// transitions to CANCELLED with a reason, persisted, event published).
func (s *Server) handleAgentInterrupt(ctx context.Context, args map[string]any) (string, error) {
	taskID := argString(args, "task_id")
	if taskID == "" {
		return "", fmt.Errorf("task_id is required")
	}
	reason := argString(args, "reason")
	if reason == "" {
		reason = "interrupted by model via kern_agent_interrupt"
	}
	root := resolveRoot(argString(args, "root"))

	p, err := s.platformFor(ctx, root)
	if err != nil {
		return "", err
	}
	ts := app.NewTaskService(p, nil)
	if err := ts.Cancel(taskID, reason); err != nil {
		return "", err
	}
	out, err := json.MarshalIndent(map[string]any{
		"status":  "cancelled",
		"task_id": taskID,
		"reason":  reason,
	}, "", "  ")
	if err != nil {
		return "", err
	}
	return string(out), nil
}