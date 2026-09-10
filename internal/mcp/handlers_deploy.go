package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/agent"
	"github.com/JayveerPrajapati/kern/internal/app"
)

// handleDeploy implements kern_deploy: deploys a task through
// TaskService.Deploy so the governance firewall, the human-approval gate,
// and lifecycle events all apply. Mirrors `kern deploy <task-id>` and the
// web console's POST /v1/tasks/{id}/deploy.
func (s *Server) handleDeploy(ctx context.Context, args map[string]any) (string, error) {
	taskID := argString(args, "task_id")
	if taskID == "" {
		taskID = argString(args, "task")
	}
	if taskID == "" {
		return "", fmt.Errorf("task_id is required")
	}
	root := resolveRoot(argString(args, "root"))
	p, err := s.platformFor(ctx, root)
	if err != nil {
		return "", err
	}
	ts := app.NewTaskService(p, nil)
	t, err := ts.Deploy(taskID, argString(args, "version"))
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
