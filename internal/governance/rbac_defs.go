// Shared RBAC role taxonomy. The agent tool-access role definitions live here
// — not in internal/mcp/rbac — because that package is at its LOC cap (the
// same reason ReviewerAllowedTools was moved here). internal/mcp/rbac owns
// the enforcement state (in-memory assignment map, assign/evaluate actions)
// and consults this taxonomy through the accessors below.

package governance

import (
	"fmt"
	"strings"
)

// RoleDefinition is an RBAC role: a named set of allowed/denied tools plus
// execution and write capabilities.
type RoleDefinition struct {
	Role         string   `json:"role"`
	Description  string   `json:"description"`
	AllowedTools []string `json:"allowed_tools"` // Glob or tool names; "*" for all
	DeniedTools  []string `json:"denied_tools"`
	CanExecute   bool     `json:"can_execute"`
	CanWrite     bool     `json:"can_write"`
}

// roleDefinitions is the immutable built-in role taxonomy. It is consulted by
// the mcp/rbac enforcement and assign paths via RoleDefinitions/LookupRole;
// never mutate it after package init.
var roleDefinitions = map[string]RoleDefinition{
	"admin": {
		Role:         "admin",
		Description:  "Unrestricted access across all tools and execution capabilities",
		AllowedTools: []string{"*"},
		CanExecute:   true,
		CanWrite:     true,
	},
	"architect": {
		Role:         "architect",
		Description:  "Read-only architectural analysis, planning, and policy evaluation",
		AllowedTools: []string{"kern_compact_file", "kern_project_map", "kern_graph", "kern_explore", "kern_search", "kern_ast_search", "kern_explain", "kern_policy_dsl", "kern_what_if", "kern_impact", "kern_pre_edit", "kern_arch", "kern_health", "kern_memory*"},
		DeniedTools:  []string{"kern_exec", "kern_fix", "kern_safe_delete", "kern_validate"},
		CanExecute:   false,
		CanWrite:     false,
	},
	"developer": {
		Role:         "developer",
		Description:  "Full development access: read, edit, build, test, compose",
		AllowedTools: []string{"*"},
		CanExecute:   true,
		CanWrite:     true,
	},
	"junior_dev": {
		Role:         "junior_dev",
		Description:  "Guided development with restricted execution and safe edits",
		AllowedTools: []string{"kern_compact_file", "kern_project_map", "kern_search", "kern_pre_edit", "kern_semantic_diff", "kern_prompt_fill", "kern_explain", "kern_validate"},
		DeniedTools:  []string{"kern_exec", "kern_fix", "kern_safe_delete", "kern_sandbox", "kern_lock"},
		CanExecute:   false,
		CanWrite:     false,
	},
	"reviewer": {
		Role:         "reviewer",
		Description:  "Read-only analysis, review, and reporting across the full read-only MCP surface (audit iteration-2: the previous 8-tool list neutered the default workflow)",
		AllowedTools: ReviewerAllowedTools,
		DeniedTools:  []string{"kern_exec", "kern_execute", "kern_sandbox", "kern_deploy"},
		CanExecute:   false,
		CanWrite:     false,
	},
	"auditor": {
		Role:         "auditor",
		Description:  "Security & governance compliance verification, audit receipts inspection",
		AllowedTools: []string{"kern_audit", "kern_security", "kern_policy_dsl", "kern_evidence_anchor", "kern_health", "kern_explain"},
		DeniedTools:  []string{"kern_exec", "kern_fix", "kern_validate", "kern_safe_delete"},
		CanExecute:   false,
		CanWrite:     false,
	},
}

// RoleDefinitions returns the built-in role taxonomy. The map is immutable
// after package init; callers must treat it as read-only.
func RoleDefinitions() map[string]RoleDefinition { return roleDefinitions }

// LookupRole returns the definition for a role name, and whether the role
// exists.
func LookupRole(name string) (RoleDefinition, bool) {
	def, ok := roleDefinitions[name]
	return def, ok
}

// ToolAllowedByDef checks one tool against a role definition: DeniedTools
// (exact or "*") always win; AllowedTools match exact, "*", or glob-suffix.
func ToolAllowedByDef(def RoleDefinition, toolName string) (allowed bool, reason string) {
	for _, d := range def.DeniedTools {
		if d == toolName || d == "*" {
			return false, fmt.Sprintf("Role %q explicitly denies tool %q", def.Role, toolName)
		}
	}
	for _, a := range def.AllowedTools {
		if a == "*" || a == toolName || (strings.HasSuffix(a, "*") && strings.HasPrefix(toolName, strings.TrimSuffix(a, "*"))) {
			return true, ""
		}
	}
	return false, fmt.Sprintf("Role %q does not grant permission to invoke tool %q", def.Role, toolName)
}
