package agents

// Role is a specialist agent role in the multi-agent system.
type Role string

const (
	RolePlanner   Role = "planner"
	RoleArchitect Role = "architect"
	RoleCoder     Role = "coder"
	RoleReviewer  Role = "reviewer"
	RoleSecurity  Role = "security"
	RoleTester    Role = "tester"
	RoleSRE       Role = "sre"
)

// RoleInfo describes a role's purpose and what it produces.
type RoleInfo struct {
	Role     Role
	Name     string
	Purpose  string
	Produces string // e.g. "plan", "code", "review"
	Consumes string // e.g. "task description", "plan"
	Autonomy string // L0-L5 range (see target-state autonomy levels)
}

// allRoles is the canonical catalog of the 7 standard roles, in fixed order.
var allRoles = []RoleInfo{
	{Role: RolePlanner, Name: "Planner", Purpose: "Analyze the request and produce an implementation plan.", Produces: "plan", Consumes: "task description", Autonomy: "L0-L3"},
	{Role: RoleArchitect, Name: "Architect", Purpose: "Design the change against the existing code graph and boundaries.", Produces: "design", Consumes: "plan", Autonomy: "L0-L3"},
	{Role: RoleCoder, Name: "Coder", Purpose: "Implement the planned change in source and tests.", Produces: "code", Consumes: "plan, design", Autonomy: "L2-L3"},
	{Role: RoleReviewer, Name: "Reviewer", Purpose: "Review the change for correctness and consistency.", Produces: "review", Consumes: "code", Autonomy: "L0-L2"},
	{Role: RoleSecurity, Name: "Security", Purpose: "Scan the change for security risks and policy violations.", Produces: "security report", Consumes: "code", Autonomy: "L0-L2"},
	{Role: RoleTester, Name: "Tester", Purpose: "Run and extend tests to verify the change.", Produces: "test results", Consumes: "code", Autonomy: "L0-L2"},
	{Role: RoleSRE, Name: "SRE", Purpose: "Assess runtime/deployability impact and production readiness.", Produces: "ops assessment", Consumes: "change", Autonomy: "L0-L4"},
}

// AllRoles returns the 7 standard roles with their info, as a copy so callers
// cannot mutate the package catalog.
func AllRoles() []RoleInfo {
	return append([]RoleInfo{}, allRoles...)
}

// ForRole returns the RoleInfo for a role, reporting whether it is standard.
func ForRole(r Role) (RoleInfo, bool) {
	for _, info := range allRoles {
		if info.Role == r {
			return info, true
		}
	}
	return RoleInfo{}, false
}
