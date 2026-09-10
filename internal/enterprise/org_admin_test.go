package enterprise

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/governance"
)

// dashboardBody drives serveOrgDashboard directly with httptest. Auth is
// enforced in ServeHTTP (requireAuth), not at the handler level, so calling
// the handler directly is the intended way to test the rendered page.
func dashboardBody(t *testing.T, s *Server) string {
	t.Helper()
	rr := httptest.NewRecorder()
	s.serveOrgDashboard(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("serveOrgDashboard status = %d, want 200", rr.Code)
	}
	return rr.Body.String()
}

func TestOrgAdminDashboardRendersProjectsAgentsTeams(t *testing.T) {
	s := New()
	if err := s.Register("proj-a", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if err := s.Register("proj-b", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterAgent(governance.NewAgent("agent-1", "Agent One", "coder", nil)); err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterAgent(governance.NewAgent("agent-2", "Agent Two", "reviewer", nil)); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateTeam(OrgTeam{
		ID:       "team-1",
		Name:     "Platform",
		Projects: []string{"proj-a", "proj-b"},
		Members:  []string{"agent-1", "agent-2"},
	}); err != nil {
		t.Fatal(err)
	}

	body := dashboardBody(t, s)

	// Projects: names and per-project console links.
	for _, want := range []string{"proj-a", "proj-b", `href="/proj-a/"`, `href="/proj-b/"`} {
		if !strings.Contains(body, want) {
			t.Errorf("org admin page missing %q", want)
		}
	}
	// Agents: IDs, names, and types.
	for _, want := range []string{"agent-1", "agent-2", "Agent One", "coder", "reviewer"} {
		if !strings.Contains(body, want) {
			t.Errorf("org admin page missing %q", want)
		}
	}
	// Team: ID, name, and comma-joined members/projects inline.
	for _, want := range []string{"team-1", "Platform", "agent-1, agent-2", "proj-a, proj-b"} {
		if !strings.Contains(body, want) {
			t.Errorf("org admin page missing %q", want)
		}
	}
	// Footer nav to the org JSON endpoints.
	for _, want := range []string{"/org/audit", "/org/policies", "/org/memory", "/org/tasks", "/org/search", "/org/agents", "/org/teams"} {
		if !strings.Contains(body, want) {
			t.Errorf("org admin page missing footer link %q", want)
		}
	}
}

func TestOrgAdminDashboardEmptyStates(t *testing.T) {
	s := New()
	body := dashboardBody(t, s)
	for _, want := range []string{"no projects registered", "no agents registered", "no teams registered"} {
		if !strings.Contains(body, want) {
			t.Errorf("empty org admin page missing %q", want)
		}
	}
}

func TestOrgAdminDashboardEscapesNames(t *testing.T) {
	s := New()
	if err := s.Register(`<script>`, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterAgent(governance.NewAgent(`a&b`, `A<b>c`, "coder", nil)); err != nil {
		t.Fatal(err)
	}
	body := dashboardBody(t, s)
	if strings.Contains(body, "<script>") {
		t.Error("org admin page must escape project names")
	}
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Error("org admin page should contain the escaped project name")
	}
	if !strings.Contains(body, "a&amp;b") || !strings.Contains(body, "A&lt;b&gt;c") {
		t.Error("org admin page should escape agent IDs and names")
	}
}
