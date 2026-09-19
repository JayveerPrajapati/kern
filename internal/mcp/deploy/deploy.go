// Package deploy owns task deployment MCP tool bodies (kern_deploy)
// as plain functions.
package deploy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/agent"
	"github.com/JayveerPrajapati/kern/internal/app"
	"github.com/JayveerPrajapati/kern/internal/mcp/mcpargs"
)

// Hooks provides platform resolution from the owning MCP server.
type Hooks struct {
	PlatformFor func(ctx context.Context, root string) (*app.Platform, error)
}

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

// Deploy deploys a task through TaskService.Deploy with governance and human-approval gates.
func Deploy(ctx context.Context, h Hooks, args map[string]any) (string, error) {
	taskID := mcpargs.ArgString(args, "task_id")
	if taskID == "" {
		taskID = mcpargs.ArgString(args, "task")
	}
	if taskID == "" {
		return "", fmt.Errorf("task_id is required")
	}
	root := resolveRoot(mcpargs.ArgString(args, "root"))
	if h.PlatformFor == nil {
		return "", fmt.Errorf("platform hook not configured")
	}
	p, err := h.PlatformFor(ctx, root)
	if err != nil {
		return "", err
	}
	ts := app.NewTaskService(p, nil)
	t, err := ts.Deploy(taskID, mcpargs.ArgString(args, "version"))
	if err != nil {
		if errors.Is(err, agent.ErrApprovalRequired) {
			return "", fmt.Errorf("deploy: %w — resolve it with: kern approve", err)
		}
		if errors.Is(err, agent.ErrInvalidTransition) {
			return "", fmt.Errorf("deploy: task %s is not in a deployable state: %w", taskID, err)
		}
		if strings.Contains(err.Error(), "task not found") {
			return "", fmt.Errorf("deploy: task %s not found", taskID)
		}
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "deployed: %s (state %s)\n", t.ID, t.State)
	if t.DeploymentRef != "" {
		fmt.Fprintf(&b, "deployment ref: %s\n", t.DeploymentRef)
	}
	return b.String(), nil
}
