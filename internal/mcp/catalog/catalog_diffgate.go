package catalog

import "github.com/JayveerPrajapati/kern/internal/blueprint/checks/diffgate"

// WithDiffgateTools injects the live MCP tool catalog into the diff-gate
// drift checks. It is called EXPLICITLY at server construction
// and binary startup (mcp.NewServer, cmd/kern main) — never from init(): a
// binary that omits the call leaves the checks without a catalog, and they
// fail loud (StatusError) instead of silently skipping. internal/blueprint/cli
// cannot import internal/mcp (import cycle via enterprise → web → service),
// so the checks read the injected list through diffgate.ToolInfos() at
// construction.
func WithDiffgateTools() {
	diffgate.SetToolInfos(toDiffgateToolInfos(All))
}

// toDiffgateToolInfos maps the catalog's Tool table to the neutral diff-gate
// contract surface (name, phase, risk, input schema, description).
func toDiffgateToolInfos(tools []Tool) []diffgate.ToolInfo {
	out := make([]diffgate.ToolInfo, 0, len(tools))
	for _, t := range tools {
		out = append(out, diffgate.ToolInfo{
			Name:        t.Name,
			Phase:       t.Phase,
			RiskLevel:   t.RiskLevel,
			InputSchema: t.InputSchema,
			Description: t.Description,
		})
	}
	return out
}
