package sdk

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/mcp"
	"github.com/JayveerPrajapati/kern/internal/mcp/catalog"
)

// ToolInfo is the discovery projection of one catalog tool: its name,
// description, phase/risk/category tags and input JSON schema. Callers can
// enumerate the full catalog without any running server.
type ToolInfo struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Phase       string         `json:"phase,omitempty"`
	RiskLevel   string         `json:"riskLevel,omitempty"`
	Category    string         `json:"category,omitempty"`
	InputSchema map[string]any `json:"inputSchema,omitempty"`
}

func toToolInfo(t catalog.Tool) ToolInfo {
	return ToolInfo{
		Name:        t.Name,
		Description: t.Description,
		Phase:       t.Phase,
		RiskLevel:   t.RiskLevel,
		Category:    t.Category,
		InputSchema: t.InputSchema,
	}
}

func toToolInfos(ts []catalog.Tool) []ToolInfo {
	out := make([]ToolInfo, len(ts))
	for i := range ts {
		out[i] = toToolInfo(ts[i])
	}
	return out
}

// Catalog is the in-process SDK passthrough to the full MCP tool catalog. It
// reuses the exact governed dispatch path MCP clients hit — KERN_TOOLS
// allowlist, root confinement, RBAC — with zero server and zero transport:
// construct it with NewCatalog and Call() any tool by name. It is a
// passthrough, never a governance bypass: a call an MCP client would be
// refused (out-of-confinement root, disallowed tool, RBAC denial, unknown
// tool) fails closed here too.
type Catalog struct {
	srv *mcp.Server
}

// NewCatalog constructs an in-process catalog client. No HTTP/stdio server is
// started: the server object is the same transport-free construction the CLI
// loopback uses (mcp.NewServer + CallTool), routed through the full governed
// dispatch path (CallToolGoverned).
func NewCatalog() *Catalog {
	// The reader is never consumed (Serve is never called — CallToolGoverned
	// drives dispatch directly); strings.NewReader("") is the empty stdio.
	return &Catalog{srv: mcp.NewServer(strings.NewReader(""), io.Discard)}
}

// Call invokes any catalog tool by name with the given arguments and returns
// its raw output. args may be nil. The call goes through the same governed
// dispatch path MCP clients hit: the pre-tool-use confinement gate, the
// KERN_TOOLS allowlist, checkRootArg root confinement and the RBAC agent
// check all stay in force. Unknown tools and governed denials return errors.
func (c *Catalog) Call(tool string, args map[string]any) (string, error) {
	return c.CallContext(context.Background(), tool, args)
}

// CallContext is Call with an explicit context (cancellation, deadlines).
func (c *Catalog) CallContext(ctx context.Context, tool string, args map[string]any) (string, error) {
	return c.srv.CallToolGoverned(ctx, tool, args)
}

// Tools returns every registered catalog tool's name and description (plus
// phase/risk/category tags), in registration order.
func (c *Catalog) Tools() []ToolInfo {
	return toToolInfos(catalog.All)
}

// ToolSchema returns the JSON input schema for a single tool by name. An
// unknown tool is an error.
func (c *Catalog) ToolSchema(name string) (map[string]any, error) {
	t, ok := catalog.ByName(name)
	if !ok {
		return nil, fmt.Errorf("unknown tool: %s", name)
	}
	return t.InputSchema, nil
}

// ToolsForPhase returns the tools active for an agent phase (explore, plan,
// edit, verify). The always-on meta/cross tools are included; an empty phase
// returns the whole catalog.
func (c *Catalog) ToolsForPhase(phase string) []ToolInfo {
	return toToolInfos(catalog.ToolsForPhase(phase))
}

// ToolsForRisk returns the tools whose risk is at most maxRisk (low, medium,
// high, critical). An unrecognized level returns the whole catalog.
func (c *Catalog) ToolsForRisk(maxRisk string) []ToolInfo {
	return toToolInfos(catalog.ToolsForRisk(maxRisk))
}

// Close releases the in-process server's background resources (index
// sessions, file watchers). Idempotent; safe to defer. Calls made after Close
// are not supported.
func (c *Catalog) Close() {
	c.srv.Close()
}
