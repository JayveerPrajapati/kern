// Package catalog owns the kern MCP tool catalog: the Tool type, the
// registration table (tools.go), the phase/risk/schema-version constants,
// the JSON-schema helpers the table is built with, and catalog
// introspection (ToolNames).
//
// It is a pure-data leaf package: it must never import internal/mcp (or any
// handler/dispatch/transport code), so the monolith can shrink while
// dispatch, transports and handlers stay behind. The only import is the
// diff-gate drift-check bridge (catalog_diffgate.go), which injects the
// live catalog for the catalog:drift guard via an explicit
// WithDiffgateTools() call at server construction — never from init()
// (diff-gate cannot import mcp).
package catalog

import "sync"

// Tool is an MCP tool definition.
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	// Phase tags the tool with the agent phase it belongs to (explore, plan,
	// edit or verify). "meta" (kern_meta itself) and "cross" (phase-agnostic
	// utilities) are always advertised regardless of the active phase; an
	// empty phase means the tool is not phase-filtered.
	Phase string `json:"phase,omitempty"`
	// RiskLevel tags the tool with the risk it carries when called (low,
	// medium, high or critical; see Risk* constants). low = read-only,
	// medium = contained state mutation or analysis, high = security-sensitive
	// or destructive, critical = arbitrary command execution or deployment.
	// Governed clients use this to gate tool access (P0-004).
	RiskLevel string `json:"riskLevel,omitempty"`
	// Category tags the tool with its functional family (analyze, graph,
	// governance, ...; see Category* constants). KERN_MCP_CATEGORY filters
	// the advertised tool list to a single family (kern_meta always stays,
	// like phase filtering). Every registered tool must carry one; the
	// catalog drift test enforces it.
	Category string `json:"category,omitempty"`
	// SchemaVersion tags the tool's input/output contract with a semantic
	// version (P2-003). Every registration carries it; a bump signals
	// clients that tool contracts changed and they should re-validate
	// before calling. Governed clients use this for versioned tool
	// contracts, negotiating during initialize (see negotiateSchemaVersion).
	SchemaVersion string `json:"schemaVersion,omitempty"`
	// Cacheable opts a tool into the D1 tool-response cache: its output is a
	// deterministic function of (name, args, root, index identity, schema
	// version, kern version) and it has no side effects, so repeated
	// identical calls may be served from the cache. Explicit opt-in only
	// (F1) — default false. Stateful, git-diff-family and non-deterministic
	// tools are never cached.
	Cacheable bool `json:"cacheable,omitempty"`
	// Slow marks a tool whose dispatch can take seconds to minutes (index
	// builds, builds, scans, sandboxes, test-suite runs). The MCP server
	// derives its progress-notification set from this flag (see slowTools in
	// server.go): slow tools emit 0%/keep-alive/100% progress so agents see
	// liveness, fast lookups stay silent.
	Slow bool `json:"slow,omitempty"`
}

// Agent phases for phase-aware tool routing (P1.2). Each phase exposes a
// focused shortlist of tools instead of the full catalog; kern_meta routes
// within whatever tools are available. Tools tagged PhaseMeta or PhaseCross
// are always advertised regardless of the active phase.
const (
	PhaseExplore = "explore"
	PhasePlan    = "plan"
	PhaseEdit    = "edit"
	PhaseVerify  = "verify"
	PhaseMeta    = "meta"
	PhaseCross   = "cross"
)

// Tool categories for the functional-family taxonomy (surface consolidation,
// T3). Every registered tool carries exactly one; KERN_MCP_CATEGORY=<cat>
// advertises only that family (kern_meta always stays as the router). The
// catalog drift test (catalog_test.go) fails on any tool with an empty
// Category, so new registrations must pick a family here.
const (
	CategoryAnalyze    = "analyze"
	CategoryAgent      = "agent"
	CategoryArch       = "arch"
	CategoryAST        = "ast"
	CategoryContext    = "context"
	CategoryDoc        = "doc"
	CategoryEdit       = "edit"
	CategoryEvidence   = "evidence"
	CategoryExec       = "exec"
	CategoryFramework  = "framework"
	CategoryGraph      = "graph"
	CategoryGovernance = "governance"
	CategoryIncident   = "incident"
	CategoryLock       = "lock"
	CategoryMemory     = "memory"
	CategoryMeta       = "meta"
	CategoryMCPBridge  = "mcpbridge"
	CategoryOptimize   = "optimize"
	CategoryOrg        = "org"
	CategoryProject    = "project"
	CategoryReview     = "review"
	CategoryTask       = "task"
	CategoryVerify     = "verify"
)

// ValidCategory reports whether c is one of the registered tool categories.
// An empty string means no category filter is active.
func ValidCategory(c string) bool {
	switch c {
	case CategoryAnalyze, CategoryAgent, CategoryArch, CategoryAST,
		CategoryContext, CategoryDoc, CategoryEdit, CategoryEvidence,
		CategoryExec, CategoryFramework, CategoryGraph, CategoryGovernance,
		CategoryIncident, CategoryLock, CategoryMemory, CategoryMeta,
		CategoryMCPBridge, CategoryOptimize, CategoryOrg, CategoryProject,
		CategoryReview, CategoryTask, CategoryVerify:
		return true
	}
	return false
}

// Tool risk levels for risk-aware tool metadata (P0-004). RiskLevel tags each
// registered tool with the blast radius of calling it: RiskLow for read-only
// tools, RiskMedium for contained state mutation or analysis, RiskHigh for
// security-sensitive or destructive operations, and RiskCritical for arbitrary
// command execution or deployment. Governed clients can gate tool access on
// these levels; every tool in the catalog must carry one.
const (
	RiskLow      = "low"
	RiskMedium   = "medium"
	RiskHigh     = "high"
	RiskCritical = "critical"
)

// Tool schema versions for versioned tool contracts (P2-003). SchemaVersion
// tags every registered tool with the version of its input/output contract;
// clients negotiate the schema version they speak during initialize and the
// server honors a supported request verbatim (falling back to the current
// catalog version otherwise). Bump SchemaVersionCurrent — and register the
// new value in supportedSchemaVersions — whenever a tool's contract changes.
const (
	// SchemaVersionV1 is the initial tool schema contract version: every
	// tool registration carries it, and all input/response shapes are
	// stable within it.
	SchemaVersionV1 = "1.0.0"
	// SchemaVersionCurrent is the schema version the server serves by
	// default and the version new tool registrations must carry.
	SchemaVersionCurrent = SchemaVersionV1
)

// All is the registration table — the single source of truth for the kern
// MCP catalog (declared in tools.go, kept in its own file so the table does
// not bury the server core). Dispatch, transports and handlers map tool
// names onto it; the catalog parity invariants (plugin <-> MCP, docs <->
// MCP) read it via ToolNames().

// ToolNames returns every registered MCP tool name, in registration order.
func ToolNames() []string {
	out := make([]string, len(All))
	for i, t := range All {
		out[i] = t.Name
	}
	return out
}

// byNameMu guards the lazily built name -> Tool map.
var (
	byNameMu  sync.Mutex
	byNameMap map[string]Tool
)

// ByName resolves a tool by name in O(1) instead of a linear scan over All
// (the semantic router's firstQueryParam did that per matched request). The
// map is rebuilt when the catalog grows since the last build, so a future
// dynamic registration cannot serve stale misses.
func ByName(name string) (Tool, bool) {
	byNameMu.Lock()
	if byNameMap == nil || len(byNameMap) != len(All) {
		m := make(map[string]Tool, len(All))
		for _, t := range All {
			m[t.Name] = t
		}
		byNameMap = m
	}
	t, ok := byNameMap[name]
	byNameMu.Unlock()
	return t, ok
}

// schema builds the JSON-schema object for a tool's InputSchema: a
// properties map plus an optional required list.
func schema(props map[string]any, required []string) map[string]any {
	s := map[string]any{
		"type":       "object",
		"properties": props,
	}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

// strProp is a string property with a description.
func strProp(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}

// enumProp is a string property constrained to a closed vocabulary whose
// values the server validates (or dispatches on) — e.g. kern_meta's phase
// (explore|plan|edit|verify) and the whatif/impact change kinds. Declaring
// the enum lets strict MCP clients constrain generation at the source
// instead of discovering valid values by trial and error. Use it ONLY for
// genuinely closed vocabularies: open-ended params (free-form presets,
// registry-extensible names, language overrides) must stay strProp.
func enumProp(desc string, values ...string) map[string]any {
	return map[string]any{"type": "string", "description": desc, "enum": values}
}

// ToolsForPhase returns the focused shortlist of tools active for phase.
// PhaseMeta and PhaseCross tools are always included. If phase is empty,
// All is returned.
func ToolsForPhase(phase string) []Tool {
	if phase == "" {
		return All
	}
	var out []Tool
	for _, t := range All {
		if t.Phase == PhaseMeta || t.Phase == PhaseCross || t.Phase == phase {
			out = append(out, t)
		}
	}
	return out
}

// ToolsForRisk returns tools filtered to at most maxRisk (low <= medium <= high <= critical).
func ToolsForRisk(maxRisk string) []Tool {
	rank := map[string]int{
		RiskLow:      1,
		RiskMedium:   2,
		RiskHigh:     3,
		RiskCritical: 4,
	}
	maxR := rank[maxRisk]
	if maxR == 0 {
		return All
	}
	var out []Tool
	for _, t := range All {
		if r := rank[t.RiskLevel]; r > 0 && r <= maxR {
			out = append(out, t)
		}
	}
	return out
}
