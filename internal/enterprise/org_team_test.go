package enterprise

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/governance/identity"
)

// setupOrgWithTeamDeps returns a server with one project ("proj-a") and two
// agents ("agent-1", "agent-2") registered, ready for team tests.
func setupOrgWithTeamDeps(t *testing.T) *Server {
	t.Helper()
	t.Setenv("KERN_AUTH_TOKEN", testToken)
	s := New()
	if err := s.Register("proj-a", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterAgent(identity.NewAgent("agent-1", "Agent One", "worker", nil)); err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterAgent(identity.NewAgent("agent-2", "Agent Two", "worker", nil)); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestCreateTeam(t *testing.T) {
	s := setupOrgWithTeamDeps(t)
	err := s.CreateTeam(OrgTeam{
		ID:       "team-a",
		Name:     "Platform",
		Projects: []string{"proj-a"},
		Members:  []string{"agent-1", "agent-2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	team, ok := s.Team("team-a")
	if !ok {
		t.Fatal("expected team-a to exist")
	}
	if team.Name != "Platform" {
		t.Errorf("Name = %q, want Platform", team.Name)
	}
	if len(team.Members) != 2 {
		t.Errorf("Members = %v, want 2", team.Members)
	}
}

func TestCreateTeamErrors(t *testing.T) {
	t.Run("empty ID", func(t *testing.T) {
		s := New()
		if err := s.CreateTeam(OrgTeam{ID: ""}); err == nil {
			t.Error("expected error for empty ID")
		}
	})
	t.Run("duplicate ID", func(t *testing.T) {
		s := setupOrgWithTeamDeps(t)
		base := OrgTeam{ID: "team-a", Projects: []string{"proj-a"}, Members: []string{"agent-1"}}
		if err := s.CreateTeam(base); err != nil {
			t.Fatal(err)
		}
		if err := s.CreateTeam(base); err == nil {
			t.Error("expected error for duplicate team ID")
		}
	})
	t.Run("unknown project", func(t *testing.T) {
		s := setupOrgWithTeamDeps(t)
		err := s.CreateTeam(OrgTeam{ID: "t", Projects: []string{"nope"}})
		if err == nil {
			t.Fatal("expected error for unknown project")
		}
		if !strings.Contains(err.Error(), "unknown project") {
			t.Errorf("error = %q, want mention of unknown project", err)
		}
	})
	t.Run("unknown agent", func(t *testing.T) {
		s := setupOrgWithTeamDeps(t)
		err := s.CreateTeam(OrgTeam{ID: "t", Projects: []string{"proj-a"}, Members: []string{"ghost"}})
		if err == nil {
			t.Fatal("expected error for unknown agent")
		}
		if !strings.Contains(err.Error(), "unknown agent") {
			t.Errorf("error = %q, want mention of unknown agent", err)
		}
	})
	// Fail-closed: a failed create must not leave a partial team behind.
	s := setupOrgWithTeamDeps(t)
	if err := s.CreateTeam(OrgTeam{ID: "t", Projects: []string{"nope"}}); err == nil {
		t.Fatal("expected error")
	}
	if _, ok := s.Team("t"); ok {
		t.Error("failed create must not register team")
	}
}

func TestTeamsSortedAndRemove(t *testing.T) {
	s := setupOrgWithTeamDeps(t)
	for _, id := range []string{"team-b", "team-a", "team-c"} {
		if err := s.CreateTeam(OrgTeam{ID: id, Projects: []string{"proj-a"}, Members: []string{"agent-1"}}); err != nil {
			t.Fatal(err)
		}
	}
	teams := s.Teams()
	if len(teams) != 3 {
		t.Fatalf("Teams() = %d, want 3", len(teams))
	}
	if teams[0].ID != "team-a" || teams[1].ID != "team-b" || teams[2].ID != "team-c" {
		t.Errorf("Teams() not sorted by ID: %v", teams)
	}

	if err := s.RemoveTeam("team-b"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Team("team-b"); ok {
		t.Error("team-b should be removed")
	}
	if err := s.RemoveTeam("team-b"); err == nil {
		t.Error("expected error removing nonexistent team")
	}
}

func TestMembershipHelpers(t *testing.T) {
	s := setupOrgWithTeamDeps(t)
	if err := s.CreateTeam(OrgTeam{
		ID:       "team-a",
		Projects: []string{"proj-a"},
		Members:  []string{"agent-2", "agent-1"}, // intentionally unsorted
	}); err != nil {
		t.Fatal(err)
	}

	if !s.IsTeamMember("team-a", "agent-1") {
		t.Error("agent-1 should be a member")
	}
	if s.IsTeamMember("team-a", "ghost") {
		t.Error("ghost should not be a member")
	}
	if s.IsTeamMember("missing-team", "agent-1") {
		t.Error("unknown team should have no members")
	}

	agentTeams := s.AgentTeams("agent-1")
	if len(agentTeams) != 1 || agentTeams[0] != "team-a" {
		t.Errorf("AgentTeams(agent-1) = %v, want [team-a]", agentTeams)
	}
	if got := s.AgentTeams("ghost"); len(got) != 0 {
		t.Errorf("AgentTeams(ghost) = %v, want empty", got)
	}

	agents := s.TeamAgents("team-a")
	if len(agents) != 2 {
		t.Fatalf("TeamAgents() = %d, want 2", len(agents))
	}
	if agents[0].ID != "agent-1" || agents[1].ID != "agent-2" {
		t.Errorf("TeamAgents not sorted by ID: %v", agents)
	}

	projects := s.TeamProjects("team-a")
	if len(projects) != 1 || projects[0] != "proj-a" {
		t.Errorf("TeamProjects() = %v, want [proj-a]", projects)
	}
	missing := s.TeamProjects("missing-team")
	if missing == nil {
		t.Error("TeamProjects for unknown team must be empty slice, not nil")
	}
	if len(missing) != 0 {
		t.Errorf("TeamProjects(unknown) = %v, want empty", missing)
	}
}

func TestOrgTeamHTTP(t *testing.T) {
	s := setupOrgWithTeamDeps(t)

	t.Run("list empty", func(t *testing.T) {
		rr := httptest.NewRecorder()
		s.ServeHTTP(rr, authedRequest(t, "GET", "/org/teams"))
		if rr.Code != 200 {
			t.Fatalf("status = %d, want 200", rr.Code)
		}
		var resp struct {
			Teams []OrgTeam `json:"teams"`
			Count int       `json:"count"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		if resp.Count != 0 || len(resp.Teams) != 0 {
			t.Errorf("expected empty teams, got %+v", resp)
		}
	})

	t.Run("create", func(t *testing.T) {
		body := `{"id":"team-a","name":"Platform","projects":["proj-a"],"members":["agent-1"]}`
		req := httptest.NewRequest("POST", "/org/teams", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+testToken)
		rr := httptest.NewRecorder()
		s.ServeHTTP(rr, req)
		if rr.Code != http.StatusCreated {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusCreated)
		}
	})

	t.Run("create duplicate 409", func(t *testing.T) {
		body := `{"id":"team-a","name":"Platform","projects":["proj-a"],"members":["agent-1"]}`
		req := httptest.NewRequest("POST", "/org/teams", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+testToken)
		rr := httptest.NewRecorder()
		s.ServeHTTP(rr, req)
		if rr.Code != http.StatusConflict {
			t.Errorf("status = %d, want %d", rr.Code, http.StatusConflict)
		}
	})

	t.Run("create bad body 400", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/org/teams", strings.NewReader("{not json"))
		req.Header.Set("Authorization", "Bearer "+testToken)
		rr := httptest.NewRecorder()
		s.ServeHTTP(rr, req)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want %d", rr.Code, http.StatusBadRequest)
		}
	})

	t.Run("create unknown project 409", func(t *testing.T) {
		body := `{"id":"team-x","projects":["nope"],"members":["agent-1"]}`
		req := httptest.NewRequest("POST", "/org/teams", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+testToken)
		rr := httptest.NewRecorder()
		s.ServeHTTP(rr, req)
		if rr.Code != http.StatusConflict {
			t.Errorf("status = %d, want %d", rr.Code, http.StatusConflict)
		}
	})

	t.Run("list after create", func(t *testing.T) {
		rr := httptest.NewRecorder()
		s.ServeHTTP(rr, authedRequest(t, "GET", "/org/teams"))
		if rr.Code != 200 {
			t.Fatalf("status = %d, want 200", rr.Code)
		}
		var resp struct {
			Teams []OrgTeam `json:"teams"`
			Count int       `json:"count"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		if resp.Count != 1 || resp.Teams[0].ID != "team-a" {
			t.Errorf("expected 1 team team-a, got %+v", resp)
		}
	})

	t.Run("get one", func(t *testing.T) {
		rr := httptest.NewRecorder()
		s.ServeHTTP(rr, authedRequest(t, "GET", "/org/teams/team-a"))
		if rr.Code != 200 {
			t.Fatalf("status = %d, want 200", rr.Code)
		}
		if !strings.Contains(rr.Body.String(), "Platform") {
			t.Error("GET /org/teams/team-a should include the team name")
		}
	})

	t.Run("get missing 404", func(t *testing.T) {
		rr := httptest.NewRecorder()
		s.ServeHTTP(rr, authedRequest(t, "GET", "/org/teams/team-zz"))
		if rr.Code != http.StatusNotFound {
			t.Errorf("status = %d, want %d", rr.Code, http.StatusNotFound)
		}
	})

	t.Run("delete", func(t *testing.T) {
		rr := httptest.NewRecorder()
		s.ServeHTTP(rr, authedRequest(t, "DELETE", "/org/teams/team-a"))
		if rr.Code != http.StatusNoContent {
			t.Errorf("status = %d, want %d", rr.Code, http.StatusNoContent)
		}
		rr2 := httptest.NewRecorder()
		s.ServeHTTP(rr2, authedRequest(t, "GET", "/org/teams/team-a"))
		if rr2.Code != http.StatusNotFound {
			t.Errorf("after delete status = %d, want %d", rr2.Code, http.StatusNotFound)
		}
	})

	t.Run("delete missing 404", func(t *testing.T) {
		rr := httptest.NewRecorder()
		s.ServeHTTP(rr, authedRequest(t, "DELETE", "/org/teams/team-ghost"))
		if rr.Code != http.StatusNotFound {
			t.Errorf("status = %d, want %d", rr.Code, http.StatusNotFound)
		}
	})

	t.Run("requires auth", func(t *testing.T) {
		t.Setenv("KERN_AUTH_TOKEN", testToken)
		req := httptest.NewRequest("GET", "/org/teams", nil) // no auth header
		rr := httptest.NewRecorder()
		s.ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
		}
	})
}

func TestOrgAgentTeamsHTTP(t *testing.T) {
	s := setupOrgWithTeamDeps(t)
	if err := s.CreateTeam(OrgTeam{
		ID:       "team-a",
		Projects: []string{"proj-a"},
		Members:  []string{"agent-1"},
	}); err != nil {
		t.Fatal(err)
	}

	t.Run("known agent", func(t *testing.T) {
		rr := httptest.NewRecorder()
		s.ServeHTTP(rr, authedRequest(t, "GET", "/org/agents/agent-1/teams"))
		if rr.Code != 200 {
			t.Fatalf("status = %d, want 200", rr.Code)
		}
		if !strings.Contains(rr.Body.String(), "team-a") {
			t.Error("agent-1 teams should include team-a")
		}
	})

	t.Run("unknown agent 404", func(t *testing.T) {
		rr := httptest.NewRecorder()
		s.ServeHTTP(rr, authedRequest(t, "GET", "/org/agents/ghost/teams"))
		if rr.Code != http.StatusNotFound {
			t.Errorf("status = %d, want %d", rr.Code, http.StatusNotFound)
		}
	})
}

func TestServeHTTPOrgDashboardHasTeamsLink(t *testing.T) {
	s := New()
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, authedRequest(t, "GET", "/"))
	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `/org/teams`) {
		t.Error("dashboard should link to Org Teams")
	}
}
