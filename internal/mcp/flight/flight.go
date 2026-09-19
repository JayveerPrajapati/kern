// Package flight owns the flight-recorder MCP tool bodies (kern_flight)
// as plain functions.
package flight

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/JayveerPrajapati/kern/internal/flight"
	"github.com/JayveerPrajapati/kern/internal/mcp/mcpargs"
)

func resolveRoot(root string) string {
	if root == "" {
		if cwd, err := os.Getwd(); err == nil {
			return filepath.Clean(cwd)
		}
		return "."
	}
	if abs, err := filepath.Abs(root); err == nil {
		return filepath.Clean(abs)
	}
	return root
}

// Flight replays the full recorded audit trail for one task as text.
func Flight(ctx context.Context, args map[string]any) (string, error) {
	root := resolveRoot(mcpargs.ArgString(args, "root"))
	task := mcpargs.ArgString(args, "task")
	if task == "" {
		return "", fmt.Errorf("task is required")
	}
	return flight.New(root).TrailText(task), nil
}
