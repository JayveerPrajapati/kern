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

import (
	"os"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/mcpcatalog"
)

// Default is the direct eleven-tool surface advertised on a default install.
// These are the common code-intelligence and verification routes agents need
// without first learning the full catalog. KERN_MCP_FULL=1 opts into the full
// 34-tool catalog; KERN_MCP_PHASE and KERN_MCP_SINGLE_TOOL remain supported.
var Default = []string{
	"kern_buddy",    // session onboarding and repo digest
	"kern_search",   // ranked symbol search
	"kern_explore",  // symbol source + callers/callees + blast radius
	"kern_context",  // minimal relevant source slice
	"kern_probe",    // task-driven multi-symbol context bundle
	"kern_research", // read-only multi-symbol research bundle
	"kern_impact",   // transitive blast radius
	"kern_graph",    // one-hop call graph context
	"kern_review",   // token-optimised review context
	"kern_verify",   // unified verification
	"kern_meta",     // optional natural-language router → advertised sub-tools
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

// Mode returns the active advertisement surface for local telemetry. It is
// deliberately privacy-safe: only the mode name, never roots, paths, commands,
// arguments, or file contents. Direct is the default; explicit environment
// modes take precedence in the same broad order the plugin and server use.
func Mode() string {
	switch {
	case os.Getenv("KERN_MCP_SINGLE_TOOL") == "1":
		return "single"
	case os.Getenv("KERN_MCP_FULL") == "1":
		return "full"
	case os.Getenv("KERN_MCP_HIGH_LEVEL_ONLY") == "1":
		return "high-level"
	case validPhase(os.Getenv("KERN_MCP_PHASE")):
		return "phase"
	case os.Getenv("KERN_MCP_CATEGORY") != "" && catalog.ValidCategory(strings.ToLower(strings.TrimSpace(os.Getenv("KERN_MCP_CATEGORY")))):
		return "category"
	case strings.TrimSpace(os.Getenv("KERN_TOOLS")) != "":
		return "allowlist"
	case os.Getenv("KERN_MCP_SLIM") == "1":
		return "slim"
	default:
		return "direct"
	}
}

func validPhase(p string) bool {
	switch strings.ToLower(strings.TrimSpace(p)) {
	case "explore", "plan", "edit", "verify":
		return true
	}
	return false
}
