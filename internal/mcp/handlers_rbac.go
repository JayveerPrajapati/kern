package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

type RoleDefinition struct {
	Role         string   `json:"role"`
	Description  string   `json:"description"`
	AllowedTools []string `json:"allowed_tools"` // Glob or tool names; "*" for all
	DeniedTools  []string `json:"denied_tools"`
	CanExecute   bool     `json:"can_execute"`
	CanWrite     bool     `json:"can_write"`
}

var (
	rbacMu     sync.RWMutex
	agentRoles = map[string]string{
		"default": "developer",
	}

	roleDefinitions = map[string]RoleDefinition{
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
			AllowedTools: []string{"kern_compact_file", "kern_project_map", "kern_code_graph", "kern_explore", "kern_search", "kern_ast_search", "kern_explain", "kern_policy_dsl", "kern_what_if", "kern_impact", "kern_pre_edit", "kern_arch", "kern_health", "kern_memory*"},
			DeniedTools:  []string{"kern_exec", "kern_fix", "kern_safe_delete", "kern_run_build"},
			CanExecute:   false,
			CanWrite:     false,
		},
		"developer": {
			Role:         "developer",
			Description:  "Full development access: read, edit, build, test, compose",
			AllowedTools: []string{"*"},
			DeniedTools:  []string{"kern_safe_delete"},
			CanExecute:   true,
			CanWrite:     true,
		},
		"junior_dev": {
			Role:         "junior_dev",
			Description:  "Guided development with restricted execution and safe edits",
			AllowedTools: []string{"kern_compact_file", "kern_project_map", "kern_search", "kern_pre_edit", "kern_semantic_diff", "kern_prompt_fill", "kern_explain", "kern_run_build"},
			DeniedTools:  []string{"kern_exec", "kern_fix", "kern_safe_delete", "kern_sandbox", "kern_lock"},
			CanExecute:   false,
			CanWrite:     false,
		},
		"reviewer": {
			Role:         "reviewer",
			Description:  "Read-only code review, semantic diffing, and citation verification",
			AllowedTools: []string{"kern_compact_file", "kern_project_map", "kern_search", "kern_diff_files", "kern_semantic_diff", "kern_evidence_anchor", "kern_explain", "kern_policy_dsl"},
			DeniedTools:  []string{"kern_exec", "kern_fix", "kern_safe_delete", "kern_run_build", "kern_lock"},
			CanExecute:   false,
			CanWrite:     false,
		},
		"auditor": {
			Role:         "auditor",
			Description:  "Security & governance compliance verification, audit receipts inspection",
			AllowedTools: []string{"kern_audit", "kern_security", "kern_policy_dsl", "kern_evidence_anchor", "kern_health", "kern_explain"},
			DeniedTools:  []string{"kern_exec", "kern_fix", "kern_run_build", "kern_safe_delete"},
			CanExecute:   false,
			CanWrite:     false,
		},
	}
)

func (s *Server) handleAgentRoleRBAC(ctx context.Context, args map[string]any) (string, error) {
	action := strings.ToLower(strings.TrimSpace(argString(args, "action")))
	toolName := strings.TrimSpace(argString(args, "tool"))
	if action == "" {
		if toolName != "" {
			action = "evaluate"
		} else {
			action = "roles"
		}
	}

	rbacMu.Lock()
	defer rbacMu.Unlock()

	format := strings.ToLower(argString(args, "format"))

	switch action {
	case "roles":
		var list []RoleDefinition
		for _, r := range roleDefinitions {
			list = append(list, r)
		}
		if format == "json" {
			data, _ := json.MarshalIndent(list, "", "  ")
			return string(data), nil
		}
		var sb strings.Builder
		sb.WriteString("## Agent Role-Based Access Control (RBAC) Matrix\n\n")
		sb.WriteString("| Role | Description | Can Write | Can Exec |\n")
		sb.WriteString("|---|---|---|---|\n")
		for _, r := range list {
			sb.WriteString(fmt.Sprintf("| **%s** | %s | %v | %v |\n", r.Role, r.Description, r.CanWrite, r.CanExecute))
		}
		return sb.String(), nil

	case "assign":
		agentID := argString(args, "agent_id")
		if agentID == "" {
			return "", fmt.Errorf("kern_agent_role_rbac: 'agent_id' required to assign role")
		}
		roleName := strings.ToLower(strings.TrimSpace(argString(args, "role")))
		if _, ok := roleDefinitions[roleName]; !ok {
			return "", fmt.Errorf("kern_agent_role_rbac: unknown role %q", roleName)
		}
		agentRoles[agentID] = roleName
		return fmt.Sprintf("✅ Assigned role %q to agent %q", roleName, agentID), nil

	case "evaluate", "check":
		agentID := argString(args, "agent_id")
		if agentID == "" {
			agentID = "anonymous"
		}
		roleName := strings.ToLower(strings.TrimSpace(argString(args, "role")))
		if roleName == "" {
			if assigned, ok := agentRoles[agentID]; ok {
				roleName = assigned
			} else {
				roleName = "developer" // default role
			}
		}

		def, ok := roleDefinitions[roleName]
		if !ok {
			return "", fmt.Errorf("kern_agent_role_rbac: unknown role %q", roleName)
		}

		if toolName == "" {
			return "", fmt.Errorf("kern_agent_role_rbac: 'tool' required for evaluation")
		}

		// Evaluate permissions
		allowed := false
		reason := ""

		// Check explicit denies first
		for _, d := range def.DeniedTools {
			if d == toolName || d == "*" {
				allowed = false
				reason = fmt.Sprintf("Role %q explicitly denies tool %q", roleName, toolName)
				break
			}
		}

		// If not denied, check allows
		if reason == "" {
			for _, a := range def.AllowedTools {
				if a == "*" || a == toolName || (strings.HasSuffix(a, "*") && strings.HasPrefix(toolName, strings.TrimSuffix(a, "*"))) {
					allowed = true
					break
				}
			}
			if !allowed {
				reason = fmt.Sprintf("Role %q does not grant permission to invoke tool %q", roleName, toolName)
			}
		}

		verdict := "✅ ALLOWED"
		if !allowed {
			verdict = "🛑 DENIED"
		}

		if format == "json" {
			data, _ := json.MarshalIndent(map[string]any{
				"allowed":  allowed,
				"agent_id": agentID,
				"role":     roleName,
				"tool":     toolName,
				"reason":   reason,
			}, "", "  ")
			return string(data), nil
		}

		report := fmt.Sprintf("**RBAC Verdict:** %s\n- Agent: `%s`\n- Role: `%s`\n- Tool: `%s`\n",
			verdict, agentID, roleName, toolName)
		if !allowed {
			report += fmt.Sprintf("- Denial Reason: %s\n- Recommendation: Request role elevation to 'developer' or 'admin'.\n", reason)
		}

		return report, nil

	default:
		return "", fmt.Errorf("kern_agent_role_rbac: unsupported action %q", action)
	}
}
