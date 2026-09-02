// Tool dispatch table — routes every registered kern_* tool to its handler.
// Extracted from the dispatchTool switch (G-11 expensive tier): a data table
// replaces control-flow cases, and the dispatch<->registration parity
// invariant is enforced structurally by TestDispatchParityWithRegistration
// (map keys <-> tools table), no source parsing required.
//
// Two handler shapes exist: the majority take (ctx, args); three (sandbox,
// heal, run-build) also take the call id. dispatchFunc is the union shape and
// simple() adapts the plain handlers to it.

package mcp

import (
	"context"
	"fmt"
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
var dispatchTable = map[string]dispatchFunc{
	"kern_meta":              simple((*Server).handleMeta),
	"kern_optimize_prompt":   simple((*Server).handleOptimizePrompt),
	"kern_memory_add":        simple((*Server).handleMemoryAdd),
	"kern_memory_list":       simple((*Server).handleMemoryList),
	"kern_memory_recall":     simple((*Server).handleMemoryRecall),
	"kern_mask_pii":          simple((*Server).handleMaskPII),
	"kern_security":          simple((*Server).handleSecurity),
	"kern_safe_delete":       simple((*Server).handleSafeDelete),
	"kern_doc_search":        simple((*Server).handleDocSearch),
	"kern_doc_index":         simple((*Server).handleDocIndex),
	"kern_doc_fetch":         simple((*Server).handleDocFetch),
	"kern_commitmsg":         simple((*Server).handleCommitmsg),
	"kern_precache":          simple((*Server).handlePrecache),
	"kern_swap":              simple((*Server).handleSwap),
	"kern_sandbox":           (*Server).handleSandbox,
	"kern_diff_files":        simple((*Server).handleDiffFiles),
	"kern_heal":              (*Server).handleHeal,
	"kern_validate":          simple((*Server).handleValidate),
	"kern_schema_validate":   simple((*Server).handleSchemaValidate),
	"kern_verify_output":     simple((*Server).handleVerifyOutput),
	"kern_check_draft":       simple((*Server).handleCheckDraft),
	"kern_taint":             simple((*Server).handleTaint),
	"kern_compact_file":      simple((*Server).handleCompact),
	"kern_buddy":             simple((*Server).handleBuddy),
	"kern_project_map":       simple((*Server).handleProjectMap),
	"kern_pack":              simple((*Server).handlePack),
	"kern_run_build":         (*Server).handleRunBuild,
	"kern_optimize_log":      simple((*Server).handleOptimizeLog),
	"kern_optimize_output":   simple((*Server).handleOptimizeOutput),
	"kern_stats":             simple((*Server).handleStats),
	"kern_semcache":          simple((*Server).handleSemcache),
	"kern_context_budget":    simple((*Server).handleContextBudget),
	"kern_ast_search":        simple((*Server).handleAstSearch),
	"kern_frameworks":        simple((*Server).handleFrameworks),
	"kern_entry_points":      simple((*Server).handleEntryPoints),
	"kern_search":            simple((*Server).handleSearch),
	"kern_repo_search":       simple((*Server).handleRepoSearch),
	"kern_why":               simple((*Server).handleWhy),
	"kern_code_graph":        simple((*Server).handleCodeGraph),
	"kern_inherits":          simple((*Server).handleInherits),
	"kern_context":           simple((*Server).handleContext),
	"kern_changes":           simple((*Server).handleChanges),
	"kern_review":            simple((*Server).handleReview),
	"kern_hubs":              simple((*Server).handleHubs),
	"kern_test_gaps":         simple((*Server).handleTestGaps),
	"kern_path":              simple((*Server).handlePath),
	"kern_dead":              simple((*Server).handleDead),
	"kern_larges":            simple((*Server).handleLarges),
	"kern_arch":              simple((*Server).handleArch),
	"kern_communities":       simple((*Server).handleCommunities),
	"kern_churn":             simple((*Server).handleChurn),
	"kern_near":              simple((*Server).handleNear),
	"kern_walk":              simple((*Server).handleNear),
	"kern_graph":             simple((*Server).handleGraph),
	"kern_explore":           simple((*Server).handleExplore),
	"kern_fts_search":        simple((*Server).handleFtsSearch),
	"kern_bridges":           simple((*Server).handleBridges),
	"kern_cochange":          simple((*Server).handleCochange),
	"kern_probe":             simple((*Server).handleProbe),
	"kern_trace":             simple((*Server).handleTrace),
	"kern_lock":              simple((*Server).handleLock),
	"kern_unlock":            simple((*Server).handleUnlock),
	"kern_lock_status":       simple((*Server).handleLockStatus),
	"kern_usage_guide":       simple((*Server).handleUsageGuide),
	"kern_guard_check":       simple((*Server).handleGuardCheck),
	"kern_authorize_context": simple((*Server).handleAuthorizeContext),
	"kern_rename":            simple((*Server).handleRename),
	"kern_exec":              simple((*Server).handleExec),
	"kern_analyze":           simple((*Server).handleAnalyze),
	"kern_plan":              simple((*Server).handlePlan),
	"kern_execute":           simple((*Server).handleExecute),
	"kern_verify":            simple((*Server).handleVerify),
	"kern_incident":          simple((*Server).handleIncident),
	"kern_what_if":           simple((*Server).handleWhatIf),
	"kern_impact":            simple((*Server).handleImpact),
	"kern_memory":            simple((*Server).handleMemory),
	"kern_agents":            simple((*Server).handleAgents),
	"kern_loop":              simple((*Server).handleLoop),
	"kern_run":               simple((*Server).handleRun),
	"kern_workflow":          simple((*Server).handleWorkflow),
	"kern_onboard":           simple((*Server).handleOnboard),
	"kern_audit":             simple((*Server).handleAudit),
	"kern_approve":           simple((*Server).handleApprove),
	"kern_correlate":         simple((*Server).handleCorrelate),
	"kern_learn":             simple((*Server).handleLearn),
	"kern_modernize":         simple((*Server).handleModernize),
}

// dispatchTool routes a prechecked tool name to its handler. Every registered
// kern_* tool has an entry in dispatchTable; runTool applies allowlist and
// root validation via precheckTool before dispatching, so dispatchTool only
// ever sees a tool that passed the gate.
func (s *Server) dispatchTool(ctx context.Context, id string, name string, args map[string]any) (string, error) {
	h, ok := dispatchTable[name]
	if !ok {
		return "", fmt.Errorf("unknown tool: %s", name)
	}
	return h(s, ctx, id, args)
}
