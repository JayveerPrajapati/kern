package enterprise

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/orgapprovals"
)

// TestRegisterAgentBindsOrgRole covers the P13 stage-2 registry binding: an
// identity with a Role registers the agent AND persists the org role to
// <org-root>/.kern/org-rbac.json; the web console's UserRole lookup then
// resolves it (org-wins).
func TestRegisterAgentBindsOrgRole(t *testing.T) {
	orgRoot := t.TempDir()
	t.Setenv("KERN_RBAC_DEFAULT_DENY", "1")
	s := mustNew(t).WithOrgRoot(orgRoot)

	a := governance.NewAgent("agent-1", "Agent One", "coder", nil)
	a.Role = "admin"
	if err := s.RegisterAgent(a); err != nil {
		t.Fatalf("RegisterAgent: %v", err)
	}
	roles, err := orgapprovals.LoadOrgRBACRoles(orgRoot)
	if err != nil {
		t.Fatalf("LoadOrgRBACRoles: %v", err)
	}
	if roles["agent-1"] != "admin" {
		t.Fatalf("org role store = %v, want agent-1=admin", roles)
	}
	if role, ok := s.UserRole("agent-1"); !ok || role != "admin" {
		t.Fatalf("UserRole = %q,%v, want admin,true", role, ok)
	}
}

// TestUserRoleConsultsOrgRoles covers the org-wins posture of the web
// console's role lookup: an org role bound via RegisterAgent overrides the
// user-registry role, and falls back to it once the org binding is cleared.
func TestUserRoleConsultsOrgRoles(t *testing.T) {
	orgRoot := t.TempDir()
	t.Setenv("KERN_RBAC_DEFAULT_DENY", "1")
	s := mustNew(t).WithOrgRoot(orgRoot)
	if err := s.AddUserBy("dual-user", "reviewer", "test"); err != nil {
		t.Fatalf("AddUserBy: %v", err)
	}
	// Org role binding wins over the user-registry role.
	if err := orgapprovals.AssignOrgRole(orgRoot, "dual-user", "admin"); err != nil {
		t.Fatalf("AssignOrgRole: %v", err)
	}
	if role, ok := s.UserRole("dual-user"); !ok || role != "admin" {
		t.Fatalf("UserRole = %q,%v, want admin,true (org wins)", role, ok)
	}
	// Cleared org binding → the user-registry role stands.
	if err := orgapprovals.AssignOrgRole(orgRoot, "dual-user", ""); err != nil {
		t.Fatalf("clear org role: %v", err)
	}
	if role, ok := s.UserRole("dual-user"); !ok || role != "reviewer" {
		t.Fatalf("UserRole after clearing org role = %q,%v, want reviewer,true", role, ok)
	}
	// No org root configured: org roles are not consulted at all (the user
	// registry stays authoritative — byte-for-byte backward compat).
	s2 := mustNew(t)
	if err := s2.AddUserBy("plain", "auditor", "test"); err != nil {
		t.Fatalf("AddUserBy(s2): %v", err)
	}
	if role, ok := s2.UserRole("plain"); !ok || role != "auditor" {
		t.Fatalf("no-org-root UserRole = %q,%v, want auditor,true", role, ok)
	}
}

// TestServeOrgAgentsRoleBinding covers the REST touchpoint: POST /org/agents
// with an optional "role" binds it in the org role store, and GET /org/agents
// displays the binding.
func TestServeOrgAgentsRoleBinding(t *testing.T) {
	orgRoot := t.TempDir()
	t.Setenv("KERN_RBAC_DEFAULT_DENY", "1")
	s := mustNew(t).WithOrgRoot(orgRoot)

	body := `{"id":"agent-1","name":"Agent One","type":"coder","role":"admin"}`
	req := authedRequest(t, "POST", "/org/agents")
	req.Body = io.NopCloser(strings.NewReader(body))
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("POST /org/agents = %d, want 201 (body: %s)", rr.Code, rr.Body.String())
	}
	roles, err := orgapprovals.LoadOrgRBACRoles(orgRoot)
	if err != nil {
		t.Fatalf("LoadOrgRBACRoles: %v", err)
	}
	if roles["agent-1"] != "admin" {
		t.Fatalf("org role store = %v, want agent-1=admin", roles)
	}

	greq := authedRequest(t, "GET", "/org/agents")
	grr := httptest.NewRecorder()
	s.ServeHTTP(grr, greq)
	var got struct {
		Agents []struct {
			ID   string `json:"id"`
			Role string `json:"role"`
		} `json:"agents"`
	}
	if err := json.Unmarshal(grr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode GET /org/agents: %v", err)
	}
	if len(got.Agents) != 1 || got.Agents[0].ID != "agent-1" || got.Agents[0].Role != "admin" {
		t.Fatalf("GET /org/agents = %+v, want agent-1 with role admin", got.Agents)
	}
}
