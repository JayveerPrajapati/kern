package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	bpmcp "github.com/JayveerPrajapati/kern/internal/blueprint/mcp"
)

// Blueprint change-firewall tools bridged into the kern catalog.

// runBlueprintHandler adapts a blueprint ToolHandler (raw-JSON args,
// ToolResult) to the kern handler shape (map args, text/error).
func runBlueprintHandler(h bpmcp.ToolHandler, args map[string]any) (string, error) {
	// kern callers say "root"; the blueprint handlers say "repo".
	if root, ok := args["root"]; ok {
		args["repo"] = root
		delete(args, "root")
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return "", fmt.Errorf("kern_blueprint: invalid arguments: %v", err)
	}
	res := h.Handle(context.Background(), raw)
	var texts []string
	for _, c := range res.Content {
		if c.Type == "text" {
			texts = append(texts, c.Text)
		}
	}
	out := strings.TrimSpace(strings.Join(texts, "\n"))
	if res.IsError {
		if out == "" {
			out = "blueprint gate error"
		}
		return out, fmt.Errorf("%s", out)
	}
	return out, nil
}

func (s *Server) handleValidateStaged(ctx context.Context, args map[string]any) (string, error) {
	return runBlueprintHandler(bpmcp.ValidateStagedHandler{}, args)
}

func (s *Server) handleValidateProposed(ctx context.Context, args map[string]any) (string, error) {
	return runBlueprintHandler(bpmcp.ValidateProposedHandler{}, args)
}

func (s *Server) handleExplainFinding(ctx context.Context, args map[string]any) (string, error) {
	return runBlueprintHandler(bpmcp.ExplainFindingHandler{}, args)
}

func (s *Server) handleRepairGuidance(ctx context.Context, args map[string]any) (string, error) {
	return runBlueprintHandler(bpmcp.RepairGuidanceHandler{}, args)
}
