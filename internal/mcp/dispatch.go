// Tool dispatch table — routes every registered kern_* tool to its handler.
// Extracted from the dispatchTool switch (expensive tier): a data table
// replaces control-flow cases, and the dispatch<->registration parity
// invariant is enforced structurally by TestDispatchParityWithRegistration
// (map keys <-> tools table), no source parsing required.
//
// Two handler shapes exist: the majority take (ctx, args); sandbox, heal,
// run-build and verify also take the call id (verify for M4 phase progress).
// dispatchFunc is the union shape and simple() adapts the plain handlers to it.

package mcp

import (
	"context"
	"fmt"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/mcp/etag"
)

// dispatchFunc is the signature of every tool handler in dispatchTable. The
// id is threaded through for the few handlers that need it (sandbox, heal,
// run-build); simple() discards it for the rest.
type dispatchFunc func(s *Server, ctx context.Context, id string, args map[string]any) (string, error)

// simple adapts a (ctx, args) handler to the dispatch signature.
func simple(h func(s *Server, ctx context.Context, args map[string]any) (string, error)) dispatchFunc {
	return func(s *Server, ctx context.Context, _ string, args map[string]any) (string, error) {
		return h(s, ctx, args)
	}
}

// dispatchTable routes every registered kern_* tool to its handler. Keys must
// stay in lockstep with the tools registration table.
var dispatchTable map[string]dispatchFunc

func init() {
	dispatchTable = map[string]dispatchFunc{
		"kern_meta":             simple((*Server).handleMeta),
		"kern_optimize":         simple((*Server).handleOptimize),
		"kern_fetch_raw_anchor": simple((*Server).handleFetchAnchor),
		"kern_memory":           simple((*Server).handleMemory),
		"kern_mask_pii":         simple((*Server).handleMaskPII),
		"kern_doc":              simple((*Server).handleDoc),
		"kern_swap":             simple((*Server).handleSwap),
		"kern_heal":             (*Server).handleHeal,
		"kern_compact_file":     simple((*Server).handleCompact),
		"kern_fit_context":      simple((*Server).handleFitContext),
		"kern_buddy":            simple((*Server).handleBuddy),
		"kern_project_map":      simple((*Server).handleProjectMap),
		"kern_evidence":         simple((*Server).handleEvidence),
		"kern_stats":            simple((*Server).handleStats),
		"kern_semcache":         simple((*Server).handleSemcache),
		"kern_ast_search":       simple((*Server).handleAstSearch),
		"kern_search":           simple((*Server).handleSearch),
		"kern_why":              simple((*Server).handleWhy),
		"kern_context":          simple((*Server).handleContext),
		"kern_review":           simple((*Server).handleReview),
		"kern_path":             simple((*Server).handlePath),
		"kern_arch":             simple((*Server).handleArch),
		"kern_graph":            simple((*Server).handleGraph),
		"kern_explore":          simple((*Server).handleExplore),
		"kern_probe":            simple((*Server).handleProbe),
		"kern_plan_context":     simple((*Server).handlePlanContext),
		"kern_exec":             simple((*Server).handleExec),
		"kern_plan":             simple((*Server).handlePlan),
		"kern_verify":           (*Server).handleVerify,
		"kern_impact":           simple((*Server).handleImpact),
		"kern_run":              simple((*Server).handleRun),
	}
}

// dispatchTool routes a prechecked tool name to its handler. Every registered
// kern_* tool has an entry in dispatchTable; runTool applies allowlist and
// root validation via precheckTool before dispatching (RBAC enforcement for
// org-assigned agents died with the org plane, 2026-10-18 pivot cut).
// the same gate — no invocation path can reach a handler without its role
// check.
func (s *Server) dispatchTool(ctx context.Context, id string, name string, args map[string]any) (string, error) {
	if name == "" {
		return "", fmt.Errorf("%w: missing tool name (tools/call requires a \"name\" parameter)", domain.ErrToolUnknown)
	}
	h, ok := dispatchTable[name]
	if !ok {
		return "", fmt.Errorf("%w: %s", domain.ErrToolUnknown, name)
	}
	// RBAC enforcement was enterprise-plane (org-assigned roles gating
	// per-tool access) and died with the org plane (2026-10-18 pivot cut).
	// Callers without an assigned role keep the loopback-client trust model.
	return h(s, ctx, id, args)
}

// agentIDFor returns the calling agent identity for a tool call: the explicit
// agent_id argument when present, else the built-in default agent — the same
// scoping precheckTool's safety-budget accounting uses, so enforcement and
// governance agree on who is calling.
func agentIDFor(args map[string]any) string {
	if id := argString(args, "agent_id"); id != "" {
		return id
	}
	return governance.DefaultAgentID
}

// CallTool executes any registered MCP tool by name with the given argument map.
func (s *Server) CallTool(ctx context.Context, name string, args map[string]any) (string, error) {
	if args == nil {
		args = map[string]any{}
	}
	return s.dispatchTool(ctx, "cli", name, args)
}

// CallToolGoverned executes any registered MCP tool by name through the FULL
// governed dispatch path a JSON-RPC tools/call takes: the pre-tool-use hook
// (the KERN_MCP_ROOTS confinement gate), runTool's preamble (KERN_TOOLS
// allowlist, checkRootArg root confinement, validateRoot, safety budget,
// string-argument coercion, audit + metrics) and the RBAC agent check at the
// dispatch funnel. It returns the raw tool output; a governed denial or an
// unknown tool surfaces as an error.
//
// CallTool — the trusted-loopback shorthand the CLI uses — skips the
// allowlist and root confinement. CallToolGoverned is the passthrough entry
// external callers (the SDK catalog client, REST routes) MUST use: it is a
// passthrough, never a governance bypass, and it fails closed exactly like an
// MCP client's tools/call.
func (s *Server) CallToolGoverned(ctx context.Context, name string, args map[string]any) (string, error) {
	if args == nil {
		args = map[string]any{}
	}
	if s.preTool != nil {
		if err := s.preTool(name, args); err != nil {
			return "", fmt.Errorf("pre-tool-use denied: %w", err)
		}
	}
	return s.runTool(ctx, "sdk", "", name, args)
}

// CallToolGovernedResult is CallToolGoverned speaking the conditional-fetch
// protocol (C2/F3, ADR-0012): the call runs under a per-call indexScope, so
// an eligible response carries its etag and a caller passing back an etag it
// was previously handed gets a structured unchanged verdict instead of the
// full text. REST routes and the SDK surface these as response fields —
// never the bare "unchanged (etag ...)" one-liner masquerading as tool
// output, which is why the raw CallToolGoverned (string-only callers) keeps
// the bare-ctx full-text contract (F-10). The working-set registry records
// the serve exactly like a JSON-RPC tools/call, so the trust-but-verify gate
// (C1/P1) accepts this caller's next etag claim.
func (s *Server) CallToolGovernedResult(ctx context.Context, name string, args map[string]any) (domain.ToolCallResult, error) {
	if args == nil {
		args = map[string]any{}
	}
	if s.preTool != nil {
		if err := s.preTool(name, args); err != nil {
			return domain.ToolCallResult{}, fmt.Errorf("pre-tool-use denied: %w", err)
		}
	}
	// Per-call scope, same construction toolCallResponse uses
	// (server_validation.go): maybeShortCircuit stamps etag/unchanged on it
	// and the D1 replay path stamps the stored etag.
	scope := &indexScope{}
	ctx = context.WithValue(ctx, indexScopeKey{}, scope)
	text, err := s.runTool(ctx, "sdk", "", name, args)
	res := domain.ToolCallResult{Output: text}
	// Mirror toolCallResponse's attachment gate (HIGH-1): composed steps
	// re-enter runTool under this same scope, so a non-eligible outer tool
	// must not inherit an inner step's etag.
	if scope.etag != "" && etag.Eligible(name) {
		res.Etag = scope.etag
		res.Unchanged = scope.unchanged
		ident := ""
		if scope.ix != nil && scope.ix.Identity != nil {
			ident = indexIdentityString(scope.ix.Identity)
		}
		etag.Default.Record(argString(args, "agent_id"), etag.Entry{
			Tool:     name,
			Args:     etag.CanonicalArgsKey(args),
			ETag:     scope.etag,
			Stamp:    time.Now(),
			Identity: ident,
			View:     serveView(name, args),
		})
	}
	return res, err
}
