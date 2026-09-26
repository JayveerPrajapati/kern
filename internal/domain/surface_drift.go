package domain

import "time"

// SurfaceTouchRecord is one observed commit that touched one or more of the
// tool-catalog parity surfaces — the sync points TestPluginMatchesMCPCatalog
// and the diffgate doc checks keep aligned (Self-Improvement use-cases Tier 4
// #11, tool-catalog self-consistency / drift prediction). The deterministic
// learning pass scans the recent commit history for drift-prone touch
// patterns BEFORE the parity tests fail: a commit that touched the catalog
// without the plugin, or the plugin without the docs, is a risk event the
// next verify/plan run reads as a typed-claim INFERENCE memory.
type SurfaceTouchRecord struct {
	// Commit is the commit hash. It is the deterministic dedupe key for the
	// running log: a commit already recorded is never appended again, so
	// repeated `kern check` runs stay idempotent.
	Commit string
	// CatalogTouched reports whether the commit changed the MCP catalog
	// manifest (internal/mcp/catalog/ — the source of truth behind
	// mcp.ToolNames()).
	CatalogTouched bool
	// PluginTouched reports whether the commit changed either copy of the
	// opencode plugin (.opencode/plugins/kern.ts or
	// internal/setup/assets/plugin/kern.ts), which must stay byte-identical.
	PluginTouched bool
	// DocsTouched reports whether the commit changed the regenerated tool
	// docs (docs/tool-catalog.md or docs/mcp/tool-contracts.md).
	DocsTouched bool
	// At is when the commit was made (committer timestamp).
	At time.Time
}
