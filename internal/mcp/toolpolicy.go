// Package mcp implements a minimal Model Context Protocol server over stdio.
// It is deliberately dependency-free so the binary stays offline and static.
package mcp

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/mcp/toolsurface"
	"github.com/JayveerPrajapati/kern/internal/mcpcatalog"
	"github.com/JayveerPrajapati/kern/internal/tasklife"
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
// Server) rather than re-read from the environment on every call. Catalog
// membership is resolved through the init-built toolByName map (O(1)) — built
// from the same `tools` slice every caller passes — instead of a linear scan
// of the tool catalog per call.
func toolAllowed(toolsList []Tool, allowed []string, name string) bool {
	if len(allowed) == 0 {
		return true
	}
	if _, inCatalog := toolByName[name]; !inCatalog {
		return false
	}
	for i := range allowed {
		if allowed[i] == name {
			return true
		}
	}
	return false
}

// HighLevelToolsCount reports the size of the legacy
// KERN_MCP_HIGH_LEVEL_ONLY surface. Exported so doc-parity tests derive
// the number docs state instead of hand-writing it (rec 1d: counts come
// from the catalog, never from a keyboard).
func HighLevelToolsCount() int { return len(highLevelTools) }

// highLevelTools is the set of tools kept when KERN_MCP_HIGH_LEVEL_ONLY=1
// (the legacy high-level surface). After the 2026-10-18 surface slim it is
// the orchestration + verification core: the 6 tools a high-level
// (task-planning) agent drives end to end. The read/analysis primitives
// (search/graph/arch/probe/context/...) are deliberately excluded — they
// stay reachable via kern_meta.
var highLevelTools = map[string]bool{
	"kern_run":    true,
	"kern_plan":   true,
	"kern_heal":   true,
	"kern_verify": true,
	"kern_impact": true,
	"kern_review": true,
}

// defaultTools is the surface advertised by default. The full catalog is
// gated behind KERN_MCP_FULL=1, and phase-aware routing (KERN_MCP_PHASE)
// filters either surface down to the active phase's shortlist. kern_meta's
// NL router still reaches every sub-tool handler internally regardless of
// what is advertised, so no capability is lost — only the advertised
// surface shrinks. This implements the MCP spec's "high-level tools, not
// dozens of tiny low-value tools" guidance.
//
// The default surface is usage-curated: it exposes the lifecycle tools agents
// use most while keeping schema cost below the full catalog. kern_exec is the
// deliberate execution exception: advertising it keeps the governed execution
// path discoverable, while every call still fails closed behind
// KERN_ALLOW_EXEC/KERN_TOOLS. Advertisement is not an execution firewall.
//
// The name list lives in the leaf package internal/mcp/toolsurface so the
// highlevel control plane (kern_run recommendations) can annotate tools
// outside the default surface with their KERN_MCP_FULL gating (deep-dive
// A1) without an import cycle.
var defaultTools = toolsurface.Set()

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
				s.filtered = []Tool{t.WithDerivedAnnotations()}
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
		annotateTools(out)
		s.filtered = slimIfRequested(out)
		return s.filtered
	}
	// KERN_MCP_HIGH_LEVEL_ONLY=1 → the legacy 6-tool middle set (deprecated;
	// prefer the full 32-tool catalog). Otherwise the
	// DEFAULT: the full 32-tool catalog (standing mandate 2026-10-23).
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
	annotateTools(out)
	s.filtered = slimIfRequested(out)
	return s.filtered
}

// annotateTools stamps the standard MCP annotations (derived from each tool's
// risk metadata) onto every advertised tool in place (W5, 2026-10-26
// black-box audit: hosts render readOnlyHint/destructiveHint/openWorldHint in
// their confirmation UX).
func annotateTools(out []Tool) {
	for i := range out {
		out[i] = out[i].WithDerivedAnnotations()
	}
}

// toolPhase returns the catalog phase of a tool name ("" for unknown tools —
// toolByName indexes the registered catalog once at init, so this is O(1)).
func toolPhase(name string) string {
	return toolByName[name].Phase
}

// exploreBudgetCap returns the per-session explore-phase budget cap from
// KERN_EXPLORE_BUDGET: N >= 1 enables it, unset disables it (completely off —
// zero behavior change). A malformed value (non-numeric or < 1) warns on
// stderr and disables (fails-loud, mirroring cacheTTL in tool_cache.go).
func exploreBudgetCap() int {
	v := strings.TrimSpace(os.Getenv("KERN_EXPLORE_BUDGET"))
	if v == "" {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		fmt.Fprintf(os.Stderr, "kern-mcp: KERN_EXPLORE_BUDGET=%q not a positive integer, disabled\n", v)
		return 0
	}
	return n
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
		if alt := tasklife.FallbackFor(name); alt != "" {
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
	// Explore-phase budget (B2, anti-stall campaign Phase 2): a per-SERVER,
	// per-session-key cap on explore-phase tool calls (KERN_EXPLORE_BUDGET=N,
	// opt-in; unset = completely off). Placed after allowlist/root validation
	// so blocked calls never consume budget (same ordering rationale as the
	// safety-budget block below) and BEFORE it so a phase denial never counts
	// against the safety budget. The counter is the server's own per-session
	// map (phaseBudget) — the explore cap is per-session-key, while s.budget
	// is per-Server and task-scoped elsewhere — so the cap works standalone
	// with zero safety-budget env opt-in. The count is mirrored into the
	// generic domain mechanism (TrackToolCallPhase) when the safety budget
	// exists, keeping that mechanism live on this path; its limit is
	// deliberately NOT set on s.budget, because the gateway would otherwise
	// deny with the generic message instead of this teaching text, and the
	// per-session semantics would be lost.
	if cap := s.phaseBudgetCap; cap > 0 && toolPhase(name) == PhaseExplore {
		key := sessionKeyFor(args)
		s.budgetMu.Lock()
		deny := s.phaseBudget[key] >= cap
		if !deny {
			s.phaseBudget[key]++
			if s.budget != nil {
				s.budget.TrackToolCallPhase(PhaseExplore)
			}
		}
		s.budgetMu.Unlock()
		if deny {
			return "", fmt.Errorf("%w: explore budget exceeded (%d calls) — emit a plan (kern_plan) or raise KERN_EXPLORE_BUDGET", domain.ErrToolDenied, cap)
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
