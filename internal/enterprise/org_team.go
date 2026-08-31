// Org/team model ( gap P19.3). Adds a minimal but complete notion of
// teams that own projects and group agents as members, layered on top of the
// existing flat org (projects + agents). Additive only: web.App, domain.Team,
// and the single-project path are untouched.
package enterprise

import (
	"fmt"
	"sort"

	"github.com/JayveerPrajapati/kern/internal/governance"
)

// OrgTeam is a team within the org: a named group that owns/accesses a set of
// projects and is made up of agent members (by agent ID).
type OrgTeam struct {
	ID       string   // unique within the org
	Name     string   // human-friendly team name
	Projects []string // project names the team owns/accesses
	Members  []string // agent IDs that are team members
}

// CreateTeam registers a new team. Validation is fail-closed: an empty or
// duplicate ID is rejected, and any reference to an unknown project name or
// unknown agent ID errors rather than being silently accepted.
func (s *Server) CreateTeam(team OrgTeam) error {
	if team.ID == "" {
		return fmt.Errorf("enterprise: team ID must not be empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.teamRegistry[team.ID]; exists {
		return fmt.Errorf("enterprise: team %q already exists", team.ID)
	}
	for _, p := range team.Projects {
		if _, exists := s.projects[p]; !exists {
			return fmt.Errorf("enterprise: team %q references unknown project %q", team.ID, p)
		}
	}
	for _, a := range team.Members {
		if _, exists := s.orgAgents[a]; !exists {
			return fmt.Errorf("enterprise: team %q references unknown agent %q", team.ID, a)
		}
	}
	// Store a defensive copy so callers mutating their local slice don't alias
	// the registered team's state.
	cp := team
	cp.Projects = append([]string(nil), team.Projects...)
	cp.Members = append([]string(nil), team.Members...)
	s.teamRegistry[team.ID] = &cp
	return nil
}

// Team returns the team with the given ID and whether it exists.
func (s *Server) Team(id string) (*OrgTeam, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.teamRegistry[id]
	return t, ok
}

// Teams returns all teams, sorted by ID.
func (s *Server) Teams() []OrgTeam {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ids := make([]string, 0, len(s.teamRegistry))
	for id := range s.teamRegistry {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]OrgTeam, 0, len(ids))
	for _, id := range ids {
		out = append(out, *s.teamRegistry[id])
	}
	return out
}

// RemoveTeam deletes the team with the given ID. Returns an error if the team
// does not exist.
func (s *Server) RemoveTeam(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.teamRegistry[id]; !exists {
		return fmt.Errorf("enterprise: team %q not found", id)
	}
	delete(s.teamRegistry, id)
	return nil
}

// TeamAgents returns the member agents of a team, resolved via the org agent
// registry and sorted by ID. Returns nil (with ok=false) when the team is
// unknown; members that somehow reference unregistered agents are skipped.
func (s *Server) TeamAgents(teamID string) []*governance.AgentIdentity {
	s.mu.RLock()
	defer s.mu.RUnlock()
	team, exists := s.teamRegistry[teamID]
	if !exists {
		return nil
	}
	ids := make([]string, 0, len(team.Members))
	for _, a := range team.Members {
		if _, ok := s.orgAgents[a]; ok {
			ids = append(ids, a)
		}
	}
	sort.Strings(ids)
	out := make([]*governance.AgentIdentity, 0, len(ids))
	for _, id := range ids {
		out = append(out, s.orgAgents[id])
	}
	return out
}

// IsTeamMember reports whether agentID is a member of the team with teamID.
// Unknown teams are not a member, so this is always false for them.
func (s *Server) IsTeamMember(teamID, agentID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	team, exists := s.teamRegistry[teamID]
	if !exists {
		return false
	}
	for _, m := range team.Members {
		if m == agentID {
			return true
		}
	}
	return false
}

// AgentTeams returns the IDs of all teams the given agent belongs to, sorted.
func (s *Server) AgentTeams(agentID string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []string
	for id, team := range s.teamRegistry {
		for _, m := range team.Members {
			if m == agentID {
				out = append(out, id)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

// TeamProjects returns the project names owned by the team, or an empty (but
// non-nil) slice when the team is not found.
func (s *Server) TeamProjects(teamID string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	team, exists := s.teamRegistry[teamID]
	if !exists {
		return []string{}
	}
	return append([]string(nil), team.Projects...)
}
