package agents

import (
	"github.com/JayveerPrajapati/kern/internal/agent"
)

// teamDef describes one specialist to build in the standard team.
type teamDef struct {
	role Role
	name string
	caps []string
}

// standardTeamDefs is the fixed standard team: planner, architect, coder,
// reviewer, security, tester, plus the sre role for deploy/observe steps. SRE
// is not added to the 6-stage pipeline (standardStages).
var standardTeamDefs = []teamDef{
	{role: RolePlanner, name: "planner", caps: []string{"source:read", "docs:read", "memory:read"}},
	{role: RoleArchitect, name: "architect", caps: []string{"source:read", "graph:read", "boundaries:read"}},
	{role: RoleCoder, name: "coder", caps: []string{"source:read", "source:write", "tests:read"}},
	{role: RoleReviewer, name: "reviewer", caps: []string{"source:read", "tests:read", "verify:run"}},
	{role: RoleSecurity, name: "security", caps: []string{"source:read", "security:run"}},
	{role: RoleTester, name: "tester", caps: []string{"tests:read", "tests:write", "test:run"}},
	{role: RoleSRE, name: "sre", caps: []string{"runtime:read", "ops:read", "deploy:read"}},
}

// StandardTeam builds the standard team and wires it into the agent
// runtime. It returns a [SpecialistRegistry] and an [agent.Registry] that share
// the same identity set (each specialist registered under its role name), so
// the team is usable both by the [Pipeline] and directly by the runtime.
func StandardTeam() (*SpecialistRegistry, *agent.Registry, error) {
	team := NewSpecialistRegistry()
	runtime := agent.NewRegistry()

	for _, def := range standardTeamDefs {
		s := NewSpecialist(def.role, def.name)
		s.Capabilities = append([]string{}, def.caps...)
		s.Agent.Capabilities = append([]string{}, def.caps...)

		if err := team.Register(s); err != nil {
			return nil, nil, err
		}
		if err := runtime.Register(s.Agent); err != nil {
			return nil, nil, err
		}
	}
	return team, runtime, nil
}
