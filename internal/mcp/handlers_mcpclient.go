package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/mcpclient"
)

// handleMcpCall implements kern_mcp_call: bridges a tool from an external
// MCP server configured via `kern mcp add` (config at
// .kern/mcp-servers.json). The model may pass either the raw wire tool name
// or the public bridged name (mcp__<server>__<tool>); only the raw name is
// ever sent on the wire (dsh naming contract). Tools only — resources and
// prompts are unsupported.
func (s *Server) handleMcpCall(ctx context.Context, args map[string]any) (string, error) {
	serverName := argString(args, "server")
	if serverName == "" {
		return "", fmt.Errorf("server is required")
	}
	tool := argString(args, "tool")
	if tool == "" {
		return "", fmt.Errorf("tool is required")
	}
	root := resolveRoot(argString(args, "root"))

	servers, err := mcpclient.LoadConfig(root)
	if err != nil {
		return "", err
	}
	server, ok := mcpclient.FindServer(servers, serverName)
	if !ok {
		return "", fmt.Errorf("no configured MCP server %q (see `kern mcp list`; add with `kern mcp add`)", serverName)
	}

	var toolArgs map[string]any
	if raw, ok := args["arguments"].(map[string]any); ok {
		toolArgs = raw
	}

	// Accept the public bridged name and strip the namespace back to the raw
	// wire name; anything else is used verbatim.
	raw := tool
	if strings.HasPrefix(tool, "mcp__"+serverName+"__") {
		raw = strings.TrimPrefix(tool, "mcp__"+serverName+"__")
	}

	c, err := mcpclient.Dial(ctx, &server)
	if err != nil {
		return "", err
	}
	defer c.Close()

	res, err := c.CallTool(ctx, raw, toolArgs)
	if err != nil {
		return "", err
	}
	out, err := json.MarshalIndent(map[string]any{
		"server": serverName,
		"tool":   raw,
		"public": mcpclient.PublicName(serverName, raw),
		"result": res,
	}, "", "  ")
	if err != nil {
		return "", err
	}
	return string(out), nil
}