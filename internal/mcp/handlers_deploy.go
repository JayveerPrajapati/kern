package mcp

import (
	"context"

	mcpdeploy "github.com/JayveerPrajapati/kern/internal/mcp/deploy"
)

// handleDeploy implements kern_deploy: deploys a task through TaskService.Deploy.
func (s *Server) handleDeploy(ctx context.Context, args map[string]any) (string, error) {
	return mcpdeploy.Deploy(ctx, mcpdeploy.Hooks{
		PlatformFor: s.platformFor,
	}, args)
}
