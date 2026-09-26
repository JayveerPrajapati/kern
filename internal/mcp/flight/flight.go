// Package flight owns the flight-recorder MCP tool bodies (kern_flight)
// as plain functions.
package flight

import (
	"context"
	"fmt"

	"github.com/JayveerPrajapati/kern/internal/flight"
	"github.com/JayveerPrajapati/kern/internal/mcp/mcpargs"
	"github.com/JayveerPrajapati/kern/internal/mcp/root"
)

// Flight replays the full recorded audit trail for one task as text.
func Flight(ctx context.Context, args map[string]any) (string, error) {
	root := root.ResolveRoot(mcpargs.ArgString(args, "root"))
	task := mcpargs.ArgString(args, "task")
	if task == "" {
		return "", fmt.Errorf("task is required")
	}
	return flight.New(root).TrailText(task), nil
}
