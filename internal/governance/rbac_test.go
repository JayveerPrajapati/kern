package governance

import (
	"strings"
	"testing"
)

// TestRoleAllowsOrgAction pins the deterministic org-action permission
// mapping: org-admin (and the unrestricted "admin" agent role) may perform
// every org action; member-level roles may perform the read-only org
// actions (visibility: list/show/search/audit); default/unknown roles may
// perform none.
func TestRoleAllowsOrgAction(t *testing.T) {
	allActions := []string{
		OrgActionUserAdd, OrgActionUserList, OrgActionUserRole,
		OrgActionUserDisable, OrgActionUserAudit, OrgActionApprove, OrgActionReject,
		OrgActionProjectList, OrgActionAgentList, OrgActionAgentRegister,
		OrgActionTeamList, OrgActionTeamShow, OrgActionTeamCreate, OrgActionTeamRemove,
		OrgActionMemoryList, OrgActionMemoryAdd, OrgActionTaskList,
		OrgActionOrgSearch, OrgActionOrgAudit,
	}
	// org-admin: every action.
	for _, action := range allActions {
		if !RoleAllowsOrgAction(RoleOrgAdmin, action) {
			t.Errorf("RoleAllowsOrgAction(org-admin, %q) = false, want true", action)
		}
	}
	// The mcp/rbac "admin" role is unrestricted -> org-admin tier.
	for _, action := range allActions {
		if !RoleAllowsOrgAction("admin", action) {
			t.Errorf("RoleAllowsOrgAction(admin, %q) = false, want true", action)
		}
	}
	// Member-level roles: every read-only org action allowed; every
	// mutation denied.
	memberRoles := []string{
		RoleOrgMember, "architect", "developer", "junior_dev", "reviewer", "auditor",
	}
	memberReads := map[string]bool{
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
	for _, role := range memberRoles {
		for _, action := range allActions {
			want := memberReads[action]
			if got := RoleAllowsOrgAction(role, action); got != want {
				t.Errorf("RoleAllowsOrgAction(%s, %q) = %v, want %v (members: read-only org actions)", role, action, got, want)
			}
		}
	}
	// Default/unknown/empty roles: no org actions.
	for _, role := range []string{"", "default", "bogus"} {
		for _, action := range allActions {
			if RoleAllowsOrgAction(role, action) {
				t.Errorf("RoleAllowsOrgAction(%q, %q) = true, want false", role, action)
			}
		}
	}
	// An unknown action fails closed even for org-admin.
	if RoleAllowsOrgAction(RoleOrgAdmin, "unknown-action") {
		t.Error("RoleAllowsOrgAction(org-admin, unknown-action) = true, want false (fail-closed)")
	}
}

// TestRequireOrgRole pins the enforcement helper: allowed roles pass, denied
// roles return a clear error naming the role and the denied action.
func TestRequireOrgRole(t *testing.T) {
	if err := RequireOrgRole(RoleOrgAdmin, OrgActionUserDisable); err != nil {
		t.Errorf("RequireOrgRole(org-admin, user-disable) = %v, want nil", err)
	}
	if err := RequireOrgRole(RoleOrgAdmin, OrgActionAgentRegister); err != nil {
		t.Errorf("RequireOrgRole(org-admin, agent-register) = %v, want nil", err)
	}
	if err := RequireOrgRole("developer", OrgActionUserList); err != nil {
		t.Errorf("RequireOrgRole(developer, user-list) = %v, want nil", err)
	}
	if err := RequireOrgRole("developer", OrgActionOrgSearch); err != nil {
		t.Errorf("RequireOrgRole(developer, org-search) = %v, want nil", err)
	}
	err := RequireOrgRole("developer", OrgActionUserAdd)
	if err == nil {
		t.Fatal("RequireOrgRole(developer, user-add) = nil, want denial error")
	}
	if !strings.Contains(err.Error(), "developer") || !strings.Contains(err.Error(), OrgActionUserAdd) {
		t.Errorf("denial error %q must name the role and the denied action", err)
	}
	err = RequireOrgRole(RoleOrgMember, OrgActionTeamCreate)
	if err == nil {
		t.Fatal("RequireOrgRole(org-member, team-create) = nil, want denial error")
	}
	if !strings.Contains(err.Error(), OrgActionTeamCreate) {
		t.Errorf("denial error %q must name the denied action", err)
	}
	err = RequireOrgRole("", OrgActionApprove)
	if err == nil {
		t.Fatal("RequireOrgRole(empty role, approve) = nil, want denial error")
	}
}
