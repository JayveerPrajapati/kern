package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/agent"
	"github.com/JayveerPrajapati/kern/internal/app"
)

// runDeploy implements `kern deploy <task-id> [--version V]`. Routes through
// TaskService.Deploy so the governance firewall, the human-approval gate
// (real deploys require an approval), and lifecycle events all apply — the
// same path the web console's POST /v1/tasks/{id}/deploy uses.
func runDeploy(rest []string) {
	f, args := parseFlagsOrDie(rest)
	if len(args) < 1 || args[0] == "" {
		fatalUsage("usage: kern deploy <task-id> [--version V]")
	}
	root := projectRoot(f)
	p, err := app.New(root)
	if err != nil {
		fatal("could not load project: %v — run kern index first", err)
	}
	ts := app.NewTaskService(p, nil)

	t, err := ts.Deploy(args[0], f.version)
	if err != nil {
		if errors.Is(err, agent.ErrApprovalRequired) {
			fatal("deploy: %v — resolve it with: kern approve", err)
		}
		if errors.Is(err, agent.ErrInvalidTransition) {
			fatal("deploy: task %s is not in a deployable state: %v", args[0], err)
		}
		if strings.Contains(err.Error(), "task not found") {
			fatal("deploy: task %s not found", args[0])
		}
		fatal("deploy: %v", err)
	}
	fmt.Printf("deployed: %s (state %s)\n", t.ID, t.State)
	if t.DeploymentRef != "" {
		fmt.Printf("  deployment ref: %s\n", t.DeploymentRef)
	}
}
