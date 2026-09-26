package governance

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/JayveerPrajapati/kern/internal/cache"
)

// ReviewerAllowedTools is the read-only tool surface granted to the built-in
// "reviewer" role: every tool that analyzes, searches, explains, or reports
// without executing, writing, mutating, or deploying. Enumerated from the
// actual MCP catalog (internal/mcp/catalog/tools.go); exec/write/mutate/
// deploy tools (kern_exec, kern_execute, kern_sandbox, kern_deploy, kern_heal,
// kern_rename, kern_refactor_transaction, kern_ast_transform,
// kern_semantic_merge, kern_modernize, kern_validate*, kern_verify,
// kern_run/loop/workflow/orchestrate/compose/stream, kern_lock/unlock,
// kern_memory_add, kern_learn, kern_approve, kern_doc_index/fetch,
// kern_evidence, kern_semcache, kern_precache, kern_incident, kern_safe_delete,
// kern_onboard, kern_mcp_call, kern_agent_role_rbac, agent-mgmt, org-mgmt,
// kern_skill, kern_synthesize_test, kern_mutation_test) are deliberately
// absent and therefore denied. Lives here (not in internal/mcp/rbac) because
// the rbac package is at its LOC cap; the role definition references this
// slice directly.
var ReviewerAllowedTools = []string{
	"kern_agent_fingerprint", "kern_agents", "kern_analyze", "kern_arch",
	"kern_ast_search", "kern_audit", "kern_authorize_context", "kern_bridges",
	"kern_buddy", "kern_changes", "kern_check_draft", "kern_churn",
	"kern_cochange", "kern_commitmsg", "kern_communities", "kern_compact_file",
	"kern_context", "kern_context_budget", "kern_context_envelope", "kern_context_watch",
	"kern_correlate", "kern_cross_repo_impact", "kern_cycles", "kern_dead",
	"kern_diff_files", "kern_doc_search", "kern_entry_points", "kern_evidence_anchor",
	"kern_explain", "kern_explain_finding", "kern_explore", "kern_fetch_raw_anchor",
	"kern_fit_context", "kern_flight", "kern_fragility_hotspots", "kern_frameworks",
	"kern_fts_search", "kern_fw_trace", "kern_graph", "kern_guard_check",
	"kern_health", "kern_hubs", "kern_impact", "kern_inherits",
	"kern_larges", "kern_llm_providers", "kern_lock_status", "kern_lsp_bridge",
	"kern_mask_pii", "kern_memory_list", "kern_memory_ranked", "kern_memory_recall",
	"kern_meta", "kern_near", "kern_optimize_log", "kern_optimize_output",
	"kern_optimize_prompt", "kern_org_audit", "kern_org_search", "kern_pack",
	"kern_path", "kern_plan", "kern_plan_context", "kern_policy_dsl",
	"kern_pre_edit", "kern_probe", "kern_project_map", "kern_prompt_fill",
	"kern_prose", "kern_repair_diagnostics", "kern_repair_guidance", "kern_repo_search",
	"kern_resolve", "kern_retrieve", "kern_review", "kern_runtime",
	"kern_schema_validate", "kern_search", "kern_security", "kern_semantic_diff",
	"kern_snapshot", "kern_stats", "kern_surprising", "kern_swap",
	"kern_taint", "kern_test_gaps", "kern_trace", "kern_usage_guide",
	"kern_verify_output", "kern_what_if", "kern_why",
}

// RBACRolesPath returns the per-project RBAC role-assignment store at
// <root>/.kern/rbac.json. It lives next to the other governance stores
// (agents.json, approvals.json) and is gitignored by the standard
// "kern generated" section. Unlike agent identities there is no in-memory
// registry to seed it: assignments are written only by the RBAC assign
// action, so the file is the single source of truth across processes.
func RBACRolesPath(root string) string {
	return filepath.Join(root, ".kern", "rbac.json")
}

// SaveRBACRoles atomically persists the agent->role assignment map to
// <root>/.kern/rbac.json, using the same write discipline as the agent
// identity store: process-wide per-path lock + cross-process flock, then a
// unique temp-file + atomic rename, owner-only (0o600). Role assignments are
// load-bearing for the default-deny security posture, so a failed write is
// surfaced (fail loud), never silently swallowed: an assignment that exists
// only in memory is exactly the "resets to unassigned on restart" class of
// regression this store prevents. A nil map is persisted as an empty object.
func SaveRBACRoles(root string, roles map[string]string) error {
	path := RBACRolesPath(root)
	fl, err := cache.LockFile(path)
	if err != nil {
		return fmt.Errorf("governance: lock rbac store: %w", err)
	}
	defer fl.Unlock()
	pl := cache.PathLock(path)
	pl.Lock()
	defer pl.Unlock()

	if roles == nil {
		roles = map[string]string{}
	}
	data, err := json.MarshalIndent(roles, "", "  ")
	if err != nil {
		return fmt.Errorf("governance: encode rbac roles: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("governance: create rbac store dir: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".rbac-tmp-*")
	if err != nil {
		return fmt.Errorf("governance: create rbac store temp: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("governance: write rbac store temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("governance: close rbac store temp: %w", err)
	}
	// Owner-only (0o600): role assignments reveal which agents hold elevated
	// privileges; other local users must not read them (mirrors saveLocked
	// in store.go).
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("governance: chmod rbac store: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("governance: rename rbac store: %w", err)
	}
	return nil
}

// LoadRBACRoles reads the persisted agent->role assignment map from
// <root>/.kern/rbac.json. A missing store (fresh project) or an empty file
// is not an error and returns an empty map. A corrupt store FAILS CLOSED
// with an error instead of returning a partial map: the caller warns and
// starts with no assignments, which is the safe default (unassigned agents
// keep the legacy permit-all trust model, or reviewer under
// KERN_RBAC_DEFAULT_DENY) — never a half-restored privilege set.
func LoadRBACRoles(root string) (map[string]string, error) {
	data, err := os.ReadFile(RBACRolesPath(root))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return map[string]string{}, nil
		}
		return nil, fmt.Errorf("governance: read rbac store: %w", err)
	}
	if len(data) == 0 {
		return map[string]string{}, nil
	}
	var roles map[string]string
	if err := json.Unmarshal(data, &roles); err != nil {
		return nil, fmt.Errorf("governance: decode rbac store: %w", err)
	}
	return roles, nil
}

// ProcessRoot returns the process working directory — the project root the
// RBAC package treats as its own (init loads its in-memory enforcement map
// from it) — or "" when it cannot be determined.
func ProcessRoot() string {
	root, err := os.Getwd()
	if err != nil {
		return ""
	}
	return root
}

// SameRoot reports whether two project roots are the same directory — the
// comparison assign uses to tell a process's primary RBAC root (the one its
// in-memory enforcement map is rooted at) from a caller-supplied foreign
// root. Both roots must resolve (EvalSymlinks: same-file semantics) or the
// cleaned paths are compared; empty roots never match.
func SameRoot(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	ra, ea := filepath.EvalSymlinks(a)
	rb, eb := filepath.EvalSymlinks(b)
	if ea == nil && eb == nil {
		return filepath.Clean(ra) == filepath.Clean(rb)
	}
	return filepath.Clean(a) == filepath.Clean(b)
}
