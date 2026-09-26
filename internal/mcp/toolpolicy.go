// Package mcp implements a minimal Model Context Protocol server over stdio.
// It is deliberately dependency-free so the binary stays offline and static.
package mcp

import (
	"fmt"
	"os"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/app"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/mcp/catalog"
)

// parseAllowlist reads the KERN_TOOLS allowlist from the environment. A
// comma-separated list restricts which tools the server exposes and executes;
// unset or empty means everything is allowed. The result is parsed once at
// server construction and cached on the Server, not re-read on every dispatch.
func parseAllowlist() []string {
	v := strings.TrimSpace(os.Getenv("KERN_TOOLS"))
	if v == "" {
		return nil
	}
	var out []string
	for _, n := range strings.Split(v, ",") {
		if n = strings.TrimSpace(n); n != "" {
			// CLI subcommand aliases ("exec", "search") normalize to
			// canonical MCP tool names ("kern_exec", "kern_search") so
			// KERN_TOOLS behaves identically whether the operator writes
			// CLI-style or MCP-style names.
			out = append(out, governance.NormalizeToolName(n))
		}
	}
	return out
}

// highLevelOnly reports whether to expose only high-level tools (per the MCP
// spec) via KERN_MCP_HIGH_LEVEL_ONLY=1; otherwise all tools are registered.
func highLevelOnly() bool {
	return os.Getenv("KERN_MCP_HIGH_LEVEL_ONLY") == "1"
}

// singleTool reports whether to expose only the kern meta-tool via
// KERN_MCP_SINGLE_TOOL=1. Agents that find the tool catalog overwhelming
// can point their MCP config at this mode and interact with kern through the
// single natural-language `kern` entry point.
func singleTool() bool {
	return os.Getenv("KERN_MCP_SINGLE_TOOL") == "1"
}

// fullCatalog reports whether to advertise the full tool catalog via
// KERN_MCP_FULL=1. By default only the minimal defaultTools surface is
// advertised; this opts back in to the full catalog for power users
// and direct sub-tool callers. Phase-aware routing (KERN_MCP_PHASE) still
// filters the advertised list within the full catalog.
func fullCatalog() bool {
	return os.Getenv("KERN_MCP_FULL") == "1"
}

// mcpPhase returns the active agent phase from KERN_MCP_PHASE. An unset or
// invalid value returns "" which means no phase filtering: the whole tier
// surface (default/high-level/full) is advertised as before.
func mcpPhase() string {
	p := strings.ToLower(strings.TrimSpace(os.Getenv("KERN_MCP_PHASE")))
	if !validPhase(p) {
		return ""
	}
	return p
}

// mcpCategory returns the active tool-family filter from KERN_MCP_CATEGORY.
// An unset or invalid value returns "" which means no category filtering:
// the whole tier surface (default/high-level/full) is advertised as before.
// Like KERN_MCP_PHASE it filters ADVERTISEMENT only (tools/list responses),
// never execution — direct sub-tool calls still work.
func mcpCategory() string {
	c := strings.ToLower(strings.TrimSpace(os.Getenv("KERN_MCP_CATEGORY")))
	if !catalog.ValidCategory(c) {
		return ""
	}
	return c
}

// validPhase reports whether p is one of the four agent phases.
func validPhase(p string) bool {
	switch p {
	case PhaseExplore, PhasePlan, PhaseEdit, PhaseVerify:
		return true
	}
	return false
}

// phaseToolAllowed reports whether a tool should be advertised for the active
// phase. Tools tagged "meta" or "cross" are always available; otherwise the
// tool's phase must equal the active phase. An empty phase allows everything.
func phaseToolAllowed(t Tool, phase string) bool {
	if phase == "" {
		return true
	}
	switch t.Phase {
	case PhaseMeta, PhaseCross:
		return true
	}
	return t.Phase == phase
}

// categoryToolAllowed reports whether a tool passes the KERN_MCP_CATEGORY
// family filter. kern_meta is always advertised as the router (mirroring how
// phase filtering always keeps the meta/cross tools); every other tool must
// carry the active category.
func categoryToolAllowed(t Tool, category string) bool {
	if category == "" {
		return true
	}
	if t.Name == "kern_meta" {
		return true
	}
	return t.Category == category
}

// toolAllowed reports whether name passes the KERN_TOOLS allowlist. toolsList
// is the (already filtered or full) registered catalog; a nil/empty allowlist
// allows everything, otherwise name must appear in both the allowlist and
// the catalog. The allowlist slice is passed in (already cached on the
// Server) rather than re-read from the environment on every call.
func toolAllowed(toolsList []Tool, allowed []string, name string) bool {
	if len(allowed) == 0 {
		return true
	}
	inCatalog := false
	for i := range toolsList {
		if toolsList[i].Name == name {
			inCatalog = true
			break
		}
	}
	if !inCatalog {
		return false
	}
	for i := range allowed {
		if allowed[i] == name {
			return true
		}
	}
	return false
}

// highLevelTools is the set of tools kept when KERN_MCP_HIGH_LEVEL_ONLY=1.
// It includes the 5 high-level orchestration tools (kern_analyze, kern_plan,
// kern_execute, kern_verify, kern_incident) plus a minimal set of essential
// primitives that high-level agents still need.
var highLevelTools = map[string]bool{
	"kern_analyze":              true,
	"kern_plan":                 true,
	"kern_execute":              true,
	"kern_verify":               true,
	"kern_incident":             true,
	"kern_what_if":              true,
	"kern_impact":               true,
	"kern_search":               true,
	"kern_context":              true,
	"kern_explore":              true,
	"kern_graph":                true,
	"kern_memory_add":           true,
	"kern_memory_list":          true,
	"kern_memory_recall":        true,
	"kern_review":               true,
	"kern_security":             true,
	"kern_validate":             true,
	"kern_repair_diagnostics":   true,
	"kern_refactor_transaction": true,
	"kern_exec":                 true,
	"kern_sandbox":              true,
	"kern_commitmsg":            true,
	"kern_pack":                 true,
	"kern_project_map":          true,
	"kern_compact_file":         true,
	"kern_fit_context":          true,
	"kern_buddy":                true,
	"kern_usage_guide":          true,
	"kern_mask_pii":             true,
	"kern_optimize_prompt":      true,
	"kern_optimize_log":         true,
	"kern_doc_search":           true,
	"kern_doc_fetch":            true,
	"kern_doc_index":            true,
	"kern_context_budget":       true,
	"kern_swap":                 true,
	"kern_verify_output":        true,
	"kern_check_draft":          true,
	"kern_taint":                true,
	"kern_schema_validate":      true,
	"kern_stats":                true,
}

// defaultTools is the minimal surface advertised by default. The full
// full catalog is gated behind KERN_MCP_FULL=1, and phase-aware routing
// (KERN_MCP_PHASE) filters either surface down to the active phase's
// shortlist. kern_meta's NL router
// still reaches every sub-tool handler internally regardless of what is
// advertised, so no capability is lost — only the advertised surface
// shrinks. This implements the MCP spec's "high-level tools, not dozens
// of tiny low-value tools" guidance.
var defaultTools = map[string]bool{
	"kern_meta":              true, // NL router → all sub-tools
	"kern_explore":           true, // symbol source + callers/callees + blast radius
	"kern_impact":            true, // blast radius of a change
	"kern_review":            true, // token-optimised review context
	"kern_search":            true, // ranked symbol search
	"kern_context":           true, // minimal source slice
	"kern_optimize_prompt":   true, // compress prompts
	"kern_plan":              true, // implementation plan
	"kern_verify":            true, // unified verification
	"kern_run":               true, // orchestrate a whole task
	"kern_authorize_context": true, // authorized-context primitive (P0.1)
}

// schemaVersionFor returns the tool schema contract version this connection
// negotiated during initialize (P2-003), defaulting to the current catalog
// version when no handshake happened yet.
func (s *Server) schemaVersionFor() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.schemaVersion == "" {
		return SchemaVersionCurrent
	}
	return s.schemaVersion
}

// filteredTools returns the registered tools minus any excluded by the
// KERN_TOOLS allowlist, intersected with the active agent phase from
// KERN_MCP_PHASE. By default only the minimal defaultTools surface is
// advertised; KERN_MCP_FULL=1 opts back in to the full catalog, the legacy
// KERN_MCP_HIGH_LEVEL_ONLY mode keeps the mid-size highLevelTools set for
// backward compat, and KERN_MCP_SINGLE_TOOL=1 collapses to kern_meta alone.
// Phase-aware routing (KERN_MCP_PHASE=explore|plan|edit|verify) keeps only
// the active phase's tools plus the always-on meta/cross tools; an unset or
// invalid phase advertises the whole tier surface. It lazily reads the env
// once per server lifetime and caches the result.

func (s *Server) filteredTools() []Tool {
	s.toolsMu.Lock()
	defer s.toolsMu.Unlock()
	if s.filtered != nil {
		return s.filtered
	}
	if singleTool() {
		for _, t := range tools {
			if t.Name == "kern_meta" {
				s.filtered = []Tool{t}
				return s.filtered
			}
		}
	}
	allowed := s.allowlist
	// KERN_MCP_FULL=1 → advertise the full catalog (with KERN_TOOLS filter).
	if fullCatalog() {
		out := make([]Tool, 0, len(tools))
		for _, t := range tools {
			if len(allowed) > 0 {
				in := false
				for _, a := range allowed {
					if a == t.Name {
						in = true
						break
					}
				}
				if !in {
					continue
				}
			}
			if !phaseToolAllowed(t, mcpPhase()) {
				continue
			}
			if !categoryToolAllowed(t, mcpCategory()) {
				continue
			}
			out = append(out, t)
		}
		s.filtered = out
		return s.filtered
	}
	// KERN_MCP_HIGH_LEVEL_ONLY=1 → the legacy 38-tool middle set (deprecated;
	// prefer the default minimal set or KERN_MCP_FULL). Otherwise the NEW
	// DEFAULT: the minimal 11-tool defaultTools surface.
	var keep map[string]bool
	if highLevelOnly() {
		keep = highLevelTools
	} else {
		keep = defaultTools
	}
	out := make([]Tool, 0, len(keep))
	for _, t := range tools {
		if !keep[t.Name] {
			continue
		}
		if len(allowed) > 0 {
			in := false
			for _, a := range allowed {
				if a == t.Name {
					in = true
					break
				}
			}
			if !in {
				continue
			}
		}
		if !phaseToolAllowed(t, mcpPhase()) {
			continue
		}
		if !categoryToolAllowed(t, mcpCategory()) {
			continue
		}
		out = append(out, t)
	}
	s.filtered = out
	return s.filtered
}

// precheckTool validates a tool name against the KERN_TOOLS allowlist and the
// root argument before dispatch, resolving blocked tools to their
// policy-approved fallback. It returns the (possibly fallback) tool name to
// dispatch, or an error when the tool is not allowed and no allowed
// alternative exists, or when the root argument fails validation.
func (s *Server) precheckTool(name string, args map[string]any) (string, error) {
	// Validates against the full registered catalog by design — phase
	// filtering (KERN_MCP_PHASE) only affects advertisement, never execution.
	// Resolve against the FULL registered catalog, not the advertised
	// (filtered) set: kern_meta's NL router reaches every sub-tool handler
	// internally even when the sub-tool is not advertised, and an explicit
	// KERN_TOOLS allowlist still gates execution here.
	if !toolAllowed(tools, s.allowlist, name) {
		// Tool fallback: when a tool is blocked by the KERN_TOOLS
		// allowlist, route to its policy-approved alternative if one exists and
		// IS allowed, so a restricted deployment still gets an equivalent
		// result instead of a hard failure. Fail closed when no allowed
		// alternative exists.
		if alt := app.FallbackFor(name); alt != "" {
			if toolAllowed(tools, s.allowlist, alt) {
				name = alt
			}
		}
		// Fail closed only when no allowed alternative exists.
		if !toolAllowed(tools, s.allowlist, name) {
			return "", fmt.Errorf("%w: tool %q is not allowed (KERN_TOOLS allowlist)", domain.ErrToolDenied, name)
		}
	}
	if err := s.checkRootArg(args); err != nil {
		return "", fmt.Errorf("%w: %v", domain.ErrToolDenied, err)
	}
	if root := argString(args, "root"); root != "" {
		if err := validateRoot(root); err != nil {
			return "", fmt.Errorf("%w: %v", domain.ErrToolDenied, err)
		}
	}
	// track every tool call against the safety budget through the
	// ToolGateway. A nil gateway or nil budget is a safe no-op (back-compat).
	// The budget gate runs after allowlist/root validation so blocked calls
	// never consume budget; when the budget is already exceeded the call is
	// denied with a structured error before any handler side effect runs.
	// Evaluate's boundary is empty (the tool name is the resource) and its
	// firewall is nil (per-call firewalls live in newGovernor), so only the
	// budget dimension is enforced here.
	if s.gateway != nil && s.budget != nil {
		s.budgetMu.Lock()
		defer s.budgetMu.Unlock()
		agentID := argString(args, "agent_id")
		if agentID == "" {
			agentID = governance.DefaultAgentID
		}
		if _, _, _, gerr := s.gateway.Evaluate(agentID, argString(args, "task"), name, "call", domain.TaskBoundary{}, s.budget); gerr != nil {
			// Surface the budget reason verbatim in the structured denial.
			if _, reason := s.budget.Exceeded(); reason != "" {
				return "", fmt.Errorf("%w: safety budget exceeded: %s", domain.ErrToolDenied, reason)
			}
			return "", fmt.Errorf("%w: tool call denied by safety gateway: %s", domain.ErrToolDenied, gerr)
		}
		s.budget.TrackToolCall()
	}
	return name, nil
}
