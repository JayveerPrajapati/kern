package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/JayveerPrajapati/kern/internal/runtime"
)

// handleRuntime exposes the production-intelligence layer over MCP:
// kern_runtime with action=status|drift, mirroring `kern runtime status|drift
// --json`. status reports which runtime source is wired (live adapter via
// env/config, or the local snapshot) plus per-service profiles; drift
// compares the routes observed at runtime against the routes declared in
// code (framework entry points in the index). Both use the shared
// runtime.StatusSnapshot/DriftSnapshot shapes so CLI, MCP, and web cannot
// drift. No source wired = identical to the CLI's "not wired" hint.
func (s *Server) handleRuntime(ctx context.Context, args map[string]any) (string, error) {
	action := argString(args, "action")
	if action == "" {
		action = "status"
	}
	root := argString(args, "root")
	ix, err := s.loadIndex(ctx, root)
	if err != nil {
		return "", err
	}
	src := runtime.LoadSource(ix.Root)

	switch action {
	case "status":
		return jsonOf(runtime.StatusSnapshot(src))
	case "drift":
		var codeRoutes []string
		for _, sym := range ix.Symbols {
			if sym.Route != "" {
				codeRoutes = append(codeRoutes, sym.Route)
			}
		}
		return jsonOf(runtime.DriftSnapshot(src, codeRoutes))
	default:
		return "", fmt.Errorf("kern_runtime: unknown action %q (status|drift)", action)
	}
}

func jsonOf(v any) (string, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}
