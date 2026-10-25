// Package toolsurface is the leaf-package home of the default advertised
// MCP tool surface. It lives in its own leaf so both the mcp
// root (advertisement) and the highlevel control-plane handlers (rendering
// kern_run's tool recommendations) share ONE definition — highlevel cannot
// import the mcp root (the root imports highlevel), so the surface list
// cannot stay there without duplicating it.
//
// Surface rules (deep-dive A1, 2026-10-03): a tool recommendation that
// names a tool outside this set must say so — on a default install the
// agent cannot call it (it is KERN_MCP_FULL=1 / CLI-gated).
package toolsurface

// Default is the full 32-tool catalog advertised on a default install
// (toolpolicy.go defaultTools). The first seven are the original core surface;
// the remaining tools are usage-curated additions. Order is deterministic.
// The full catalog is the default surface (standing mandate 2026-10-23: agents
// should use ALL available kern tools — kern_graph and kern_arch moved out of
// KERN_MCP_FULL-only gating because they are low-risk, cacheable, explore-phase
// tools with no execution surface). KERN_MCP_FULL=1 and KERN_MCP_PHASE remain
// available to lift the surface further or phase-filter it.
var Default = []string{
	"kern_meta",             // NL router → all sub-tools
	"kern_explore",          // symbol source + callers/callees + blast radius
	"kern_impact",           // blast radius of a change
	"kern_apply",            // safe edit path: transactional apply + verify + rollback
	"kern_search",           // ranked symbol search
	"kern_verify",           // unified verification
	"kern_buddy",            // session onboarding digest
	"kern_exec",             // governed execution (fails closed: KERN_ALLOW_EXEC/KERN_TOOLS still gate every call — D5)
	"kern_compact_file",     // compact symbolic file summary (tier=full verbatim)
	"kern_run",              // run an intent through the full task pipeline
	"kern_optimize",         // compress context: action=prompt|log|output
	"kern_context",          // minimal relevant source slice for a symbol
	"kern_memory",           // project brain memory (add/list/recall/remove)
	"kern_evidence",         // signed-evidence toolkit (verify/explain/export/anchor)
	"kern_probe",            // micro-context router: task → symbol bundle
	"kern_plan",             // deterministic implementation plan for a change
	"kern_doc",              // project docs: action=search|fetch|index
	"kern_project_map",      // compressed whole-project symbol map
	"kern_ast_search",       // AST-level symbol search
	"kern_stats",            // before/after token savings and cost estimates
	"kern_why",              // rationale and doc-reference report for a symbol
	"kern_heal",             // self-correction loop on failing files (snapshot)
	"kern_fit_context",      // fit files/symbols into a token budget
	"kern_plan_context",     // deterministically plan which context to include
	"kern_path",             // shortest call path between two symbols
	"kern_semcache",         // inspect/manage the semantic cache
	"kern_swap",             // swap fenced code blocks to per-file signatures
	"kern_mask_pii",         // locally scan text for secrets/PII
	"kern_fetch_raw_anchor", // re-read truncated anchor output
	"kern_review",           // token-optimised code-review context
	"kern_graph",            // one-call graph context (callers/callees/blast radius)
	"kern_arch",             // architecture overview from call-graph communities
}

// Set returns the default surface as a membership map (the shape
// toolpolicy.go's filteredTools consumes).
func Set() map[string]bool {
	m := make(map[string]bool, len(Default))
	for _, n := range Default {
		m[n] = true
	}
	return m
}

// IsDefault reports whether name is advertised on a default install
// (KERN_MCP_FULL unset). A non-default tool is still callable via
// KERN_MCP_FULL=1 or the CLI — recommendations naming one must annotate it.
func IsDefault(name string) bool {
	for _, n := range Default {
		if n == name {
			return true
		}
	}
	return false
}

// Annotate renders tool names with an honest surface marker: default-surface
// tools pass through unchanged; every other tool is suffixed
// " (KERN_MCP_FULL)" so a default-install agent is never told to call a
// tool it cannot see without knowing why.
func Annotate(tools []string) string {
	out := make([]string, 0, len(tools))
	for _, t := range tools {
		if IsDefault(t) {
			out = append(out, t)
			continue
		}
		out = append(out, t+" (KERN_MCP_FULL)")
	}
	joined := ""
	for i, t := range out {
		if i > 0 {
			joined += ", "
		}
		joined += t
	}
	return joined
}
