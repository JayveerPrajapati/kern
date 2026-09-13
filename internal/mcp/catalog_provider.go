package mcp

import "github.com/JayveerPrajapati/kern/internal/blueprint/checks/diffgate"

// init wires the live MCP tool catalog into the diff-gate drift checks
// (KERN-P2-003). internal/blueprint/cli cannot import internal/mcp (mcp
// transitively imports cli via enterprise → web → service), so the checks
// consume the catalog through a provider instead. Registering it here means
// every binary that links mcp — the kern binary and its tests — supplies the
// live catalog automatically; the drift checks SKIP only when mcp is absent.
func init() {
	diffgate.SetCatalogProvider(func() []diffgate.ToolInfo {
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
	})
}
