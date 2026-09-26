// Org-wide RBAC layer (Feature Batch G): org-action permissions over the
// role model. The agent tool-access role taxonomy (admin/architect/developer/
// junior_dev/reviewer/auditor) lives in rbac_defs.go in this package — moved
// here when internal/mcp/rbac hit its LOC cap — and this file carries the
// canonical org role constants (org-admin/org-member) and bridges the
// taxonomy role NAMES by string so the org control plane reuses it instead
// of inventing a parallel one. The RBAC check is deterministic: org-admin
// (and the "admin" role, which is unrestricted) may perform every org action;
// any member-level role may only list users; default/unknown roles may
// perform no org actions.

package governance

import "fmt"

// Org role constants (the org-wide control plane tiers).
const (
	// RoleOrgAdmin is the org administrator: every org action (user
	// management, approve/reject) is allowed.
	RoleOrgAdmin = "org-admin"
	// RoleOrgMember is an ordinary org member: read-only user visibility
	// (user-list) only.
	RoleOrgMember = "org-member"
)

// Org actions governed by the RBAC layer. These are the action strings the
// kern_org_* tool bodies and the web approvals endpoints check against.
const (
	OrgActionUserAdd     = "user-add"
	OrgActionUserList    = "user-list"
	OrgActionUserRole    = "user-role"
	OrgActionUserDisable = "user-disable"
	OrgActionUserAudit   = "user-audit"
	OrgActionApprove     = "approve"
	OrgActionReject      = "reject"
	// Org resource actions (the remaining kern_org_* tools): mutations
	// (register agents, create/remove teams, write memory) require
	// org-admin; reads (list projects/agents/teams/memory/tasks, show a
	// team, cross-project search, org audit) are member-tier.
	OrgActionProjectList   = "project-list"
	OrgActionAgentList     = "agent-list"
	OrgActionAgentRegister = "agent-register"
	OrgActionTeamList      = "team-list"
	OrgActionTeamShow      = "team-show"
	OrgActionTeamCreate    = "team-create"
	OrgActionTeamRemove    = "team-remove"
	OrgActionMemoryList    = "memory-list"
	OrgActionMemoryAdd     = "memory-add"
	OrgActionTaskList      = "task-list"
	OrgActionOrgSearch     = "org-search"
	OrgActionOrgAudit      = "org-audit"
)

// orgAdminActions is the full set of org actions an org-admin may perform.
// Kept as a set so a new action fails closed (denied) until it is explicitly
// granted here.
var orgAdminActions = map[string]bool{
	OrgActionUserAdd:       true,
	OrgActionUserList:      true,
	OrgActionUserRole:      true,
	OrgActionUserDisable:   true,
	OrgActionUserAudit:     true,
	OrgActionApprove:       true,
	OrgActionReject:        true,
	OrgActionProjectList:   true,
	OrgActionAgentList:     true,
	OrgActionAgentRegister: true,
	OrgActionTeamList:      true,
	OrgActionTeamShow:      true,
	OrgActionTeamCreate:    true,
	OrgActionTeamRemove:    true,
	OrgActionMemoryList:    true,
	OrgActionMemoryAdd:     true,
	OrgActionTaskList:      true,
	OrgActionOrgSearch:     true,
	OrgActionOrgAudit:      true,
}

// orgMemberReadActions is the read-only org action set any member-level role
// may perform: org-wide visibility (projects, agents, teams, memory, tasks,
// cross-project search, org audit) plus the pre-existing user-list. Every
// mutation — user management, approve/reject, agent registration, team
// create/remove, memory writes — stays org-admin only.
var orgMemberReadActions = map[string]bool{
	OrgActionUserList:    true,
	OrgActionProjectList: true,
	OrgActionAgentList:   true,
	OrgActionTeamList:    true,
	OrgActionTeamShow:    true,
	OrgActionMemoryList:  true,
	OrgActionTaskList:    true,
	OrgActionOrgSearch:   true,
	OrgActionOrgAudit:    true,
}

// memberRoles is the set of member-level role names. It includes the
// org-member constant plus every role in the internal/mcp/rbac agent role
// taxonomy (matched by name), so a user registered with role "architect",
// "developer", "junior_dev", "reviewer" or "auditor" gets member-tier org
// permissions without a parallel taxonomy.
var memberRoles = map[string]bool{
	RoleOrgMember: true,
	"architect":   true,
	"developer":   true,
	"junior_dev":  true,
	"reviewer":    true,
	"auditor":     true,
}

// RoleAllowsOrgAction reports whether the given role may perform the given
// org action. Deterministic mapping:
//   - org-admin (and the unrestricted "admin" agent role) allows all org
//     actions;
//   - any member-level role allows the read-only org actions (visibility:
//     list/show/search/audit);
//   - the default role and unknown/empty roles allow no org actions.
func RoleAllowsOrgAction(role, action string) bool {
	switch {
	case role == RoleOrgAdmin || role == "admin":
		return orgAdminActions[action]
	case memberRoles[role]:
		return orgMemberReadActions[action]
	default:
		return false
	}
}

// RequireOrgRole returns a clear denial error when role is not allowed to
// perform the org action. It is the enforcement helper used by the org leaf
// handlers and the web approvals endpoints.
func RequireOrgRole(role, action string) error {
	if RoleAllowsOrgAction(role, action) {
		return nil
	}
	return fmt.Errorf("governance: role %q not allowed to perform org action %q (org-admin may; member roles may perform read-only org actions)", role, action)
}
