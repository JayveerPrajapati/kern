package mcp

import (
	"context"
	"fmt"

	"github.com/JayveerPrajapati/kern/internal/flight"
)

// handleFlight serves kern_flight: the flight-recorder reader (Workflow E
// observability). It replays the full recorded trail for one task as text —
// the same rendering as `kern flight show <task-id>`. Read-only.
func (s *Server) handleFlight(ctx context.Context, args map[string]any) (string, error) {
	root := resolveRoot(argString(args, "root"))
	task := argString(args, "task")
	if task == "" {
		return "", fmt.Errorf("task is required")
	}
	return flight.New(root).TrailText(task), nil
}
