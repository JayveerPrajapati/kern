// Package toolsurface is the leaf-package home of the default advertised
// MCP tool surface (the 6-tool defaultTools set formerly defined inline in
// internal/mcp/toolpolicy.go). It lives in its own leaf so both the mcp
// root (advertisement) and the highlevel control-plane handlers (rendering
// kern_run's tool recommendations) share ONE definition — highlevel cannot
// import the mcp root (the root imports highlevel), so the surface list
// cannot stay there without duplicating it.
//
// Surface rules (deep-dive A1, 2026-10-03): a tool recommendation that
// names a tool outside this set must say so — on a default install the
// agent cannot call it (it is KERN_MCP_FULL=1 / CLI-gated).
package toolsurface

// Default is the list of tool names advertised on a default install
// (toolpolicy.go defaultTools). Order mirrors the toolpolicy declaration.
var Default = []string{
	"kern_meta",    // NL router → all sub-tools
	"kern_explore", // symbol source + callers/callees + blast radius
	"kern_impact",  // blast radius of a change
	"kern_search",  // ranked symbol search
	"kern_verify",  // unified verification
	"kern_buddy",   // session onboarding digest
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
