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

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/mcp/rbac"
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
		"kern_llm_providers":         simple((*Server).handleLLMProviders),
		"kern_register_host_sampler": simple((*Server).handleRegisterHostSampler),
		"kern_validate_staged":       simple((*Server).handleValidateStaged),
		"kern_validate_proposed":     simple((*Server).handleValidateProposed),
		"kern_explain_finding":       simple((*Server).handleExplainFinding),
		"kern_meta":                  simple((*Server).handleMeta),
		"kern_optimize":              simple((*Server).handleOptimize),
		"kern_fetch_raw_anchor":      simple((*Server).handleFetchAnchor),
		"kern_memory":                simple((*Server).handleMemory),
		"kern_mask_pii":              simple((*Server).handleMaskPII),
		"kern_security":              simple((*Server).handleSecurity),
		"kern_safe_delete":           simple((*Server).handleSafeDelete),
		"kern_doc":                   simple((*Server).handleDoc),
		"kern_commitmsg":             simple((*Server).handleCommitmsg),
		"kern_precache":              simple((*Server).handlePrecache),
		"kern_swap":                  simple((*Server).handleSwap),
		"kern_sandbox":               (*Server).handleSandbox,
		"kern_diff_files":            simple((*Server).handleDiffFiles),
		"kern_heal":                  (*Server).handleHeal,
		"kern_repair":                simple((*Server).handleRepair),
		"kern_refactor_transaction":  simple((*Server).handleRefactorTransaction),
		"kern_validate":              simple((*Server).handleValidate),
		"kern_schema_validate":       simple((*Server).handleSchemaValidate),
		"kern_verify_output":         simple((*Server).handleVerifyOutput),
		"kern_check_draft":           simple((*Server).handleCheckDraft),
		"kern_taint":                 simple((*Server).handleTaint),
		"kern_compact_file":          simple((*Server).handleCompact),
		"kern_fit_context":           simple((*Server).handleFitContext),
		"kern_buddy":                 simple((*Server).handleBuddy),
		"kern_project_map":           simple((*Server).handleProjectMap),
		"kern_pack":                  simple((*Server).handlePack),
		"kern_runtime":               simple((*Server).handleRuntime),
		"kern_deploy":                simple((*Server).handleDeploy),
		"kern_evidence":              simple((*Server).handleEvidence),
		"kern_stats":                 simple((*Server).handleStats),
		"kern_semcache":              simple((*Server).handleSemcache),
		"kern_context_budget":        simple((*Server).handleContextBudget),
		"kern_ast_search":            simple((*Server).handleAstSearch),
		"kern_frameworks":            simple((*Server).handleFrameworks),
		"kern_fw_trace":              simple((*Server).handleFWTrace),
		"kern_entry_points":          simple((*Server).handleEntryPoints),
		"kern_search":                simple((*Server).handleSearch),
		"kern_prose":                 simple((*Server).handleProse),
		"kern_repo_search":           simple((*Server).handleRepoSearch),
		"kern_why":                   simple((*Server).handleWhy),
		"kern_inherits":              simple((*Server).handleInherits),
		"kern_context":               simple((*Server).handleContext),
		"kern_changes":               simple((*Server).handleChanges),
		"kern_review":                simple((*Server).handleReview),
		"kern_hubs":                  simple((*Server).handleHubs),
		"kern_test_gaps":             simple((*Server).handleTestGaps),
		"kern_mutation_test":         simple((*Server).handleMutationTest),
		"kern_path":                  simple((*Server).handlePath),
		"kern_dead":                  simple((*Server).handleDead),
		"kern_cycles":                simple((*Server).handleCycles),
		"kern_larges":                simple((*Server).handleLarges),
		"kern_arch":                  simple((*Server).handleArch),
		"kern_communities":           simple((*Server).handleCommunities),
		"kern_churn":                 simple((*Server).handleChurn),
		"kern_fragility_hotspots":    simple((*Server).handleFragilityHotspots),
		"kern_near":                  simple((*Server).handleNear),
		"kern_graph":                 simple((*Server).handleGraph),
		"kern_explore":               simple((*Server).handleExplore),
		"kern_fts_search":            simple((*Server).handleFtsSearch),
		"kern_bridges":               simple((*Server).handleBridges),
		"kern_lsp_bridge":            simple((*Server).handleLSPBridge),
		"kern_surprising":            simple((*Server).handleSurprising),
		"kern_snapshot":              simple((*Server).handleSnapshot),
		"kern_cochange":              simple((*Server).handleCochange),
		"kern_probe":                 simple((*Server).handleProbe),
		"kern_retrieve":              simple((*Server).handleRetrieve),
		"kern_resolve":               simple((*Server).handleResolve),
		"kern_context_envelope":      simple((*Server).handleContextEnvelope),
		"kern_plan_context":          simple((*Server).handlePlanContext),
		"kern_orchestrate":           simple((*Server).handleOrchestrate),
		"kern_skill":                 simple((*Server).handleSkill),
		"kern_trace":                 simple((*Server).handleTrace),
		"kern_lock":                  simple((*Server).handleLock),
		"kern_usage_guide":           simple((*Server).handleUsageGuide),
		"kern_guard_check":           simple((*Server).handleGuardCheck),
		"kern_authorize_context":     simple((*Server).handleAuthorizeContext),
		"kern_rename":                simple((*Server).handleRename),
		"kern_exec":                  simple((*Server).handleExec),
		"kern_analyze":               simple((*Server).handleAnalyze),
		"kern_plan":                  simple((*Server).handlePlan),
		"kern_execute":               simple((*Server).handleExecute),
		"kern_verify":                (*Server).handleVerify,
		"kern_incident":              simple((*Server).handleIncident),
		"kern_what_if":               simple((*Server).handleWhatIf),
		"kern_impact":                simple((*Server).handleImpact),
		"kern_flight":                simple((*Server).handleFlight),
		"kern_agents":                simple((*Server).handleAgents),
		"kern_do":                    simple((*Server).handleDo),
		"kern_loop":                  simple((*Server).handleLoop),
		"kern_run":                   simple((*Server).handleRun),
		"kern_workflow":              simple((*Server).handleWorkflow),
		"kern_onboard":               simple((*Server).handleOnboard),
		"kern_audit":                 simple((*Server).handleAudit),
		"kern_approve":               simple((*Server).handleApprove),
		"kern_correlate":             simple((*Server).handleCorrelate),
		"kern_learn":                 simple((*Server).handleLearn),
		"kern_modernize":             simple((*Server).handleModernize),
		"kern_health":                simple((*Server).handleHealth),
		"kern_compose":               simple((*Server).handleCompose),
		"kern_pre_edit":              simple((*Server).handlePreEdit),
		"kern_prompt_fill":           simple((*Server).handlePromptFill),
		"kern_semantic":              simple((*Server).handleSemantic),
		"kern_context_watch":         simple((*Server).handleContextWatch),
		"kern_explain":               simple((*Server).handleExplain),
		"kern_cross_repo_impact":     simple((*Server).handleCrossRepoImpact),
		"kern_policy_dsl":            simple((*Server).handlePolicyDSL),
		"kern_agent":                 simple((*Server).handleAgent),
		"kern_mcp_call":              simple((*Server).handleMcpCall),
		"kern_stream":                simple((*Server).handleStream),
		"kern_ast_transform":         simple((*Server).handleAstTransform),
		"kern_synthesize_test":       simple((*Server).handleSynthesizeTest),
		"kern_org":                   simple((*Server).handleOrg),
	}
}

// dispatchTool routes a prechecked tool name to its handler. Every registered
// kern_* tool has an entry in dispatchTable; runTool applies allowlist and
// root validation via precheckTool before dispatching, and RBAC enforcement
// (kern_agent action=rbac) runs here at the single dispatch funnel so direct
// tools/call, kern_meta sub-tool routing, compose steps and CallTool all pass
// the same gate — no invocation path can reach a handler without its role
// check.
func (s *Server) dispatchTool(ctx context.Context, id string, name string, args map[string]any) (string, error) {
	h, ok := dispatchTable[name]
	if !ok {
		return "", fmt.Errorf("%w: %s", domain.ErrToolUnknown, name)
	}
	// RBAC enforcement: an agent with an assigned role may only invoke tools
	// its role grants (AllowedTools; DeniedTools always win). Agents without
	// an assigned role keep the legacy loopback-client trust model unchanged
	// (backward compatible). Denial surfaces as a tool error before any
	// handler side effect runs.
	if allowed, reason := rbac.CheckAgentTool(agentIDFor(args), name); !allowed {
		return "", fmt.Errorf("%w: RBAC denied: %s", domain.ErrToolDenied, reason)
	}
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
