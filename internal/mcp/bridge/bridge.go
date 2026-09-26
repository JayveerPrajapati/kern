// Package bridge owns external MCP tool forwarding tool bodies (kern_mcp_call)
// as plain functions.
package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/mcp/mcpargs"
	"github.com/JayveerPrajapati/kern/internal/mcp/root"
	"github.com/JayveerPrajapati/kern/internal/mcpclient"
)

// CallTool bridges a tool call from an external MCP server configured via .kern/mcp-servers.json.
func CallTool(ctx context.Context, args map[string]any) (string, error) {
	serverName := mcpargs.ArgString(args, "server")
	if serverName == "" {
		return "", fmt.Errorf("server is required")
	}
	tool := mcpargs.ArgString(args, "tool")
	if tool == "" {
		return "", fmt.Errorf("tool is required")
	}
	root := root.ResolveRoot(mcpargs.ArgString(args, "root"))

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
