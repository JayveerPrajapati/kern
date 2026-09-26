// Package rbac owns the agent role-based access control matrix (kern_agent_role_rbac).
package rbac

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"

	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/mcp/mcpargs"
	"github.com/JayveerPrajapati/kern/internal/orgapprovals"
)

var (
	rbacMu     sync.RWMutex
	agentRoles = map[string]string{}
	// rolesOnce defers the persisted-assignment restore to first use instead
	// of package init() (which did filesystem I/O + global mutation at import
	// time); see loadRoles.
	rolesOnce sync.Once
	// assignMu serializes the assign snapshot→save→commit sequence (finding 4).
	assignMu sync.Mutex

	// primaryRoot: root loadRoles() restored RBAC from (process cwd); other roots persist, never swap in-memory.
	primaryRoot string
)

// resolveOrgWins mirrors orgapprovals.ResolveRole via the org-role cache
// (finding 5): the cache lives in orgapprovals WITH the store it caches
// (R3), and AssignOrgRole invalidates it directly after every save, so the
// mtime+size check only has to cover cross-process writes and hand-edits.
func resolveOrgWins(projectRole string, projectAssigned bool, agentID string) (role string, assigned bool) {
	if orgRoot := governance.OrgRoot(); orgRoot != "" {
		roles, err := orgapprovals.CachedOrgRolesFor(orgRoot)
		if err == nil {
			if r, ok := roles[agentID]; ok && r != "" {
				return r, true
			}
		}
	}
	if projectAssigned {
		return projectRole, true
	}
	return "", false
}

// ResetMemory clears agent role assignments (tests only; "default" stays unseeded).
func ResetMemory() {
	loadRoles() // fire the one-shot restore first so a later use cannot reload after the reset
	rbacMu.Lock()
	defer rbacMu.Unlock()
	agentRoles = map[string]string{}
}

// loadRoles restores persisted assignments on first RBAC use (finding 8); a
// corrupt store fails closed. Replaces the former init(): same capture, same
// fail-closed log, but lazy — no filesystem I/O or global mutation at package
// import time. Every function that reads agentRoles/primaryRoot must call it
// first; the sync.Once guarantees the restore runs exactly once per process.
func loadRoles() {
	rolesOnce.Do(func() {
		primaryRoot = governance.ProcessRoot()
		if primaryRoot == "" {
			return
		}
		roles, err := governance.LoadRBACRoles(primaryRoot)
		if err != nil {
			log.Printf("kern rbac: role store unreadable, starting with no assignments (fail closed): %v", err)
			return
		}
		rbacMu.Lock()
		defer rbacMu.Unlock()
		for id, role := range roles {
			agentRoles[id] = role
		}
	})
}

// CheckAgentTool enforces RBAC at the tool-dispatch choke point. Resolution
// (Stage 2, org-wins): an org role beats the project role; else the project
// role; else unassigned — legacy permit-all unless KERN_RBAC_DEFAULT_DENY=1
// maps it to read-only "reviewer" (taxonomy: governance rbac_defs.go).
func CheckAgentTool(agentID, toolName string) (allowed bool, reason string) {
	loadRoles()
	rbacMu.RLock()
	projectRole, projectAssigned := agentRoles[agentID]
	rbacMu.RUnlock()
	roleName, assigned := resolveOrgWins(projectRole, projectAssigned, agentID)
	if !assigned {
		// Legacy loopback trust; default-deny maps unassigned agents to "reviewer".
		if os.Getenv("KERN_RBAC_DEFAULT_DENY") == "1" {
			def, ok := governance.LookupRole("reviewer")
			if !ok {
				return false, fmt.Sprintf("agent %q has no assigned role and default-deny is enabled", agentID)
			}
			return governance.ToolAllowedByDef(def, toolName)
		}
		return true, ""
	}
	def, ok := governance.LookupRole(roleName)
	if !ok {
		// assign validates role names; fail closed rather than allow on a config error.
		return false, fmt.Sprintf("agent %q is assigned unknown role %q", agentID, roleName)
	}
	return governance.ToolAllowedByDef(def, toolName)
}

// Handle evaluates or configures agent role RBAC rules; assign serializes under assignMu, never rbacMu.
func Handle(ctx context.Context, args map[string]any) (string, error) {
	loadRoles()
	action := strings.ToLower(strings.TrimSpace(mcpargs.ArgString(args, "action")))
	toolName := strings.TrimSpace(mcpargs.ArgString(args, "tool"))
	if action == "" {
		if toolName != "" {
			action = "evaluate"
		} else {
			action = "roles"
		}
	}

	format := strings.ToLower(mcpargs.ArgString(args, "format"))

	switch action {
	case "roles":
		// The taxonomy is immutable after package init; no lock needed.
		var list []governance.RoleDefinition
		for _, r := range governance.RoleDefinitions() {
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
		// 'assign' is NOT self-service — fails closed without KERN_ALLOW_RBAC_ASSIGN / KERN_RBAC_DEFAULT_DENY.
		if os.Getenv("KERN_ALLOW_RBAC_ASSIGN") != "1" && os.Getenv("KERN_RBAC_DEFAULT_DENY") != "1" {
			return "", fmt.Errorf("kern_agent_role_rbac: 'assign' is disabled (fails closed); set KERN_ALLOW_RBAC_ASSIGN=1 to enable role assignment")
		}
		agentID := mcpargs.ArgString(args, "agent_id")
		if agentID == "" {
			return "", fmt.Errorf("kern_agent_role_rbac: 'agent_id' required to assign role")
		}
		roleName := strings.ToLower(strings.TrimSpace(mcpargs.ArgString(args, "role")))
		if _, ok := governance.LookupRole(roleName); !ok {
			return "", fmt.Errorf("kern_agent_role_rbac: unknown role %q", roleName)
		}
		// P13 stage 2: scope=org persists to the org role store (org-wins), never the project map.
		if strings.EqualFold(mcpargs.ArgString(args, "scope"), "org") || mcpargs.ArgBool(args, "org") {
			orgRoot := governance.OrgRoot()
			if orgRoot == "" {
				return "", fmt.Errorf("kern_agent_role_rbac: org-scope assign requires %s", governance.OrgRootEnv)
			}
			if err := orgapprovals.AssignOrgRole(orgRoot, agentID, roleName); err != nil {
				return "", fmt.Errorf("kern_agent_role_rbac: assign org role %q=%q: %w", agentID, roleName, err)
			}
			// AssignOrgRole self-invalidates the org-role cache after its
			// atomic save (R3): the very next CheckAgentTool re-reads the
			// store with zero staleness.
			return fmt.Sprintf("✅ Assigned org role %q to agent %q (org %s)", roleName, agentID, orgRoot), nil
		}
		// Persist-first with the DISK store as snapshot base; same-root commit ACTIVATES roles.
		root := mcpargs.ArgString(args, "root")
		if root == "" {
			root, _ = os.Getwd()
		}
		if root == "" {
			return "", fmt.Errorf("kern_agent_role_rbac: cannot determine project root to persist role assignments")
		}
		assignMu.Lock()
		defer assignMu.Unlock()
		next, err := governance.LoadRBACRoles(root)
		if err != nil {
			if !governance.SameRoot(root, primaryRoot) {
				// Foreign root's store unreadable: fail closed, never leak the primary map into it.
				return "", fmt.Errorf("kern_agent_role_rbac: cannot read role store for root %s: %w", root, err)
			}
			rbacMu.Lock()
			next = make(map[string]string, len(agentRoles)+1)
			for id, r := range agentRoles {
				next[id] = r
			}
			rbacMu.Unlock()
		}
		next[agentID] = roleName
		if err := governance.SaveRBACRoles(root, next); err != nil {
			return "", fmt.Errorf("kern_agent_role_rbac: assign %q=%q: %w", agentID, roleName, err)
		}
		if !governance.SameRoot(root, primaryRoot) {
			// Cross-root assign: persist only, never swap this process's in-memory map.
			return fmt.Sprintf("✅ Assigned role %q to agent %q (persisted for %s; enforced by processes rooted there after their next restart or assignment)", roleName, agentID, root), nil
		}
		rbacMu.Lock()
		agentRoles = next
		rbacMu.Unlock()
		return fmt.Sprintf("✅ Assigned role %q to agent %q", roleName, agentID), nil

	case "evaluate", "check":
		agentID := mcpargs.ArgString(args, "agent_id")
		if agentID == "" {
			agentID = "anonymous"
		}
		roleName := strings.ToLower(strings.TrimSpace(mcpargs.ArgString(args, "role")))
		if roleName == "" {
			// Org-wins resolution, then the project assignment, then defaults.
			rbacMu.RLock()
			projectRole, projectAssigned := agentRoles[agentID]
			rbacMu.RUnlock()
			if orgRole, orgAssigned := resolveOrgWins(projectRole, projectAssigned, agentID); orgAssigned {
				roleName = orgRole
			} else if os.Getenv("KERN_RBAC_DEFAULT_DENY") == "1" {
				roleName = "reviewer" // matches enforcement: unassigned == read-only reviewer
			} else {
				roleName = "developer" // legacy default role
			}
		}

		def, ok := governance.LookupRole(roleName)
		if !ok {
			return "", fmt.Errorf("kern_agent_role_rbac: unknown role %q", roleName)
		}

		if toolName == "" {
			return "", fmt.Errorf("kern_agent_role_rbac: 'tool' required for evaluation")
		}

		allowed, reason := governance.ToolAllowedByDef(def, toolName)

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
