package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/enterprise"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/index"
)

// orgUsage is the help text for kern org.
const orgUsage = `usage: kern org [--project NAME=PATH]... <subcommand> [args...]
Enterprise org admin surface: manage registered projects, agent identities,
teams, org memory, the shared audit log, and cross-project search.
With no --project flags, the current directory is registered as a single
project named after its base directory.
Subcommands:
  projects                    list registered projects (name<TAB>root)
  agents                      list agent identities (id<TAB>name<TAB>type)
  agents register ID NAME     register an agent (--type TYPE, --perm RESOURCE:ACTION)
  teams                       list teams (id<TAB>name<TAB>members<TAB>projects)
  teams show ID               show a team
  teams create ID NAME        create a team (--project P, --member M, repeatable)
  teams remove ID             remove a team
  memory                      list org memory (id<TAB>content)
  memory add CONTENT          add an org memory (--type T)
  audit                       print the shared org audit log (id<TAB>action<TAB>resource<TAB>result)
  search QUERY                cross-project symbol search (repo/symbol<TAB>score)
Flags:
  --project NAME=PATH  register a project (repeatable)
  --json               emit JSON instead of tab-separated text
All subcommands accept --json for machine-readable output.
`

// runOrg implements `kern org`. It collects the org-level flags up to the
// first positional argument (the subcommand), registers the projects, then
// dispatches. Subcommands with their own flags (agents register, teams create,
// memory add) parse those themselves, since parseFlags does not know
// --perm/--member/--type and teams create's --project takes bare names.
func runOrg(rest []string) {
	var projects []string
	jsonOut := false
	sub := ""
	var subRest []string
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case "--project":
			if i+1 >= len(rest) {
				fatalUsage("org: --project requires NAME=PATH")
			}
			projects = append(projects, rest[i+1])
			i++
		case "--json":
			jsonOut = true
		case "--help", "-h":
			fmt.Fprint(os.Stderr, orgUsage)
			return
		default:
			// First non-flag argument is the subcommand; the rest belong to it.
			sub = rest[i]
			subRest = rest[i+1:]
			i = len(rest)
		}
	}
	if sub == "" {
		fmt.Fprint(os.Stderr, orgUsage)
		return
	}
	srv := enterprise.New()
	projs, err := resolveServeProjects(projects, ".")
	if err != nil {
		fatalUsage("org: %v", err)
	}
	for _, p := range projs {
		if err := srv.Register(p.name, p.root); err != nil {
			fatalUsage("org: %v", err)
		}
	}
	switch sub {
	case "projects":
		runOrgProjects(srv, subRest, jsonOut)
	case "agents":
		runOrgAgents(srv, subRest, jsonOut)
	case "teams":
		runOrgTeams(srv, subRest, jsonOut)
	case "memory":
		runOrgMemory(srv, subRest, jsonOut)
	case "audit":
		runOrgAudit(srv, subRest, jsonOut)
	case "search":
		runOrgSearch(srv, subRest, jsonOut)
	default:
		fatalUsage("org: unknown subcommand %q\n%s", sub, orgUsage)
	}
}

// runOrgProjects lists the registered projects (name<TAB>root).
func runOrgProjects(srv *enterprise.Server, rest []string, jsonOut bool) {
	f, _, err := parseFlags(rest)
	if err != nil {
		fatalUsage("org projects: %v", err)
	}
	projects := srv.Projects()
	if jsonOut || f.json {
		type projJSON struct {
			Name string `json:"name"`
			Root string `json:"root"`
		}
		out := make([]projJSON, 0, len(projects))
		for _, p := range projects {
			out = append(out, projJSON{Name: p.Name, Root: p.Root})
		}
		printJSON(map[string]any{"projects": out, "count": len(out)})
		return
	}
	for _, p := range projects {
		fmt.Printf("%s\t%s\n", p.Name, p.Root)
	}
}

// runOrgAgents lists agent identities (id<TAB>name<TAB>type) or dispatches to
// runOrgAgentRegister when the first arg is "register".
func runOrgAgents(srv *enterprise.Server, rest []string, jsonOut bool) {
	if len(rest) > 0 && rest[0] == "register" {
		runOrgAgentRegister(srv, rest[1:])
		return
	}
	f, _, err := parseFlags(rest)
	if err != nil {
		fatalUsage("org agents: %v", err)
	}
	agents := srv.Agents()
	// In local (non-enterprise) mode the enterprise server's registry is
	// process-local, so merge agents persisted by earlier `kern org agents
	// register` runs from the current project's store. Only persisted
	// identities are merged — incidental in-memory registrations (e.g. the
	// built-in default agent) are not part of the org surface.
	if cwd, cerr := os.Getwd(); cerr == nil {
		persisted := governance.PersistedAgents(cwd)
		seen := make(map[string]bool, len(agents))
		for _, a := range agents {
			seen[a.ID] = true
		}
		for _, a := range persisted {
			if !seen[a.ID] {
				agents = append(agents, a)
			}
		}
	}
	if jsonOut || f.json {
		type agentJSON struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			Type string `json:"type"`
		}
		out := make([]agentJSON, 0, len(agents))
		for _, a := range agents {
			out = append(out, agentJSON{ID: a.ID, Name: a.Name, Type: a.Type})
		}
		printJSON(map[string]any{"agents": out, "count": len(out)})
		return
	}
	for _, a := range agents {
		fmt.Printf("%s\t%s\t%s\n", a.ID, a.Name, a.Type)
	}
}

// runOrgAgentRegister implements `kern org agents register <id> <name>`
// with repeatable --perm RESOURCE:ACTION and an optional --type (default
// "default").
func runOrgAgentRegister(srv *enterprise.Server, rest []string) {
	agentType := "default"
	var perms []governance.Permission
	id, name := "", ""
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case "--type":
			if i+1 < len(rest) {
				agentType = rest[i+1]
				i++
			}
		case "--perm":
			if i+1 < len(rest) {
				perms = append(perms, parseOrgPermission(rest[i+1]))
				i++
			}
		default:
			if id == "" {
				id = rest[i]
			} else if name == "" {
				name = rest[i]
			}
		}
	}
	if id == "" || name == "" {
		fatalUsage("org agents register: usage: kern org agents register <id> <name> [--type TYPE] [--perm RESOURCE:ACTION]...")
	}
	if err := srv.RegisterAgent(governance.NewAgent(id, name, agentType, perms)); err != nil {
		fatal("org agents register: %v", err)
	}
	// The enterprise server's registry is in-memory and dies with this
	// process. Register into the governance registry too and persist to
	// the current project's .kern/agents.json so later processes
	// (kern authorize-context, kern org agents) see the identity.
	agent := governance.NewAgent(id, name, agentType, perms)
	if err := governance.RegisterAgent(agent); err != nil {
		fatal("org agents register: %v", err)
	}
	if cwd, cerr := os.Getwd(); cerr == nil {
		if err := governance.PersistAgent(cwd, agent); err != nil {
			fatal("org agents register: persist: %v", err)
		}
	}
	fmt.Printf("registered agent %s\n", id)
}

// parseOrgPermission parses a --perm RESOURCE:ACTION value.
func parseOrgPermission(s string) governance.Permission {
	res, act, ok := strings.Cut(s, ":")
	if !ok || strings.TrimSpace(res) == "" || strings.TrimSpace(act) == "" {
		fatalUsage("org agents register: --perm must be RESOURCE:ACTION, got %q", s)
	}
	return governance.Permission{Resource: strings.TrimSpace(res), Action: strings.TrimSpace(act)}
}

// runOrgTeams lists teams (id<TAB>name<TAB>members<TAB>projects) or dispatches
// to the show/create/remove sub-subcommands.
func runOrgTeams(srv *enterprise.Server, rest []string, jsonOut bool) {
	if len(rest) > 0 {
		switch rest[0] {
		case "show":
			runOrgTeamShow(srv, rest[1:])
			return
		case "create":
			runOrgTeamCreate(srv, rest[1:])
			return
		case "remove":
			runOrgTeamRemove(srv, rest[1:])
			return
		}
	}
	f, _, err := parseFlags(rest)
	if err != nil {
		fatalUsage("org teams: %v", err)
	}
	teams := srv.Teams()
	if jsonOut || f.json {
		type teamJSON struct {
			ID       string   `json:"id"`
			Name     string   `json:"name"`
			Projects []string `json:"projects"`
			Members  []string `json:"members"`
		}
		out := make([]teamJSON, 0, len(teams))
		for _, t := range teams {
			out = append(out, teamJSON{ID: t.ID, Name: t.Name, Projects: t.Projects, Members: t.Members})
		}
		printJSON(map[string]any{"teams": out, "count": len(out)})
		return
	}
	for _, t := range teams {
		fmt.Printf("%s\t%s\t%s\t%s\n", t.ID, t.Name, strings.Join(t.Members, ","), strings.Join(t.Projects, ","))
	}
}

// runOrgTeamShow implements `kern org teams show <id>`.
func runOrgTeamShow(srv *enterprise.Server, rest []string) {
	if len(rest) != 1 {
		fatalUsage("org teams show: usage: kern org teams show <id>")
	}
	t, ok := srv.Team(rest[0])
	if !ok {
		fatal("org teams show: team %s not found", rest[0])
	}
	fmt.Printf("ID: %s\nName: %s\nProjects: %s\nMembers: %s\n",
		t.ID, t.Name, strings.Join(t.Projects, ", "), strings.Join(t.Members, ", "))
}

// runOrgTeamCreate implements `kern org teams create <id> <name>` with
// repeatable --project P and --member M flags.
func runOrgTeamCreate(srv *enterprise.Server, rest []string) {
	var projects, members []string
	id, name := "", ""
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case "--project":
			if i+1 < len(rest) {
				projects = append(projects, rest[i+1])
				i++
			}
		case "--member":
			if i+1 < len(rest) {
				members = append(members, rest[i+1])
				i++
			}
		default:
			if id == "" {
				id = rest[i]
			} else if name == "" {
				name = rest[i]
			}
		}
	}
	if id == "" || name == "" {
		fatalUsage("org teams create: usage: kern org teams create <id> <name> [--project P]... [--member M]...")
	}
	if err := srv.CreateTeam(enterprise.OrgTeam{ID: id, Name: name, Projects: projects, Members: members}); err != nil {
		fatal("org teams create: %v", err)
	}
	fmt.Printf("created team %s\n", id)
}

// runOrgTeamRemove implements `kern org teams remove <id>`.
func runOrgTeamRemove(srv *enterprise.Server, rest []string) {
	if len(rest) != 1 {
		fatalUsage("org teams remove: usage: kern org teams remove <id>")
	}
	if err := srv.RemoveTeam(rest[0]); err != nil {
		fatal("org teams remove: %v", err)
	}
	fmt.Printf("removed team %s\n", rest[0])
}

// runOrgMemory lists org memory (id<TAB>content) or dispatches to
// runOrgMemoryAdd when the first arg is "add".
func runOrgMemory(srv *enterprise.Server, rest []string, jsonOut bool) {
	if len(rest) > 0 && rest[0] == "add" {
		runOrgMemoryAdd(srv, rest[1:])
		return
	}
	f, _, err := parseFlags(rest)
	if err != nil {
		fatalUsage("org memory: %v", err)
	}
	ms, err := srv.OrgMemory().List("")
	if err != nil {
		fatal("org memory: %v", err)
	}
	if jsonOut || f.json {
		type memJSON struct {
			ID      string `json:"id"`
			Type    string `json:"type,omitempty"`
			Content string `json:"content"`
		}
		out := make([]memJSON, 0, len(ms))
		for _, m := range ms {
			out = append(out, memJSON{ID: m.ID, Type: string(m.Type), Content: m.Content})
		}
		printJSON(map[string]any{"memories": out, "count": len(out)})
		return
	}
	for _, m := range ms {
		fmt.Printf("%s\t%s\n", m.ID, m.Content)
	}
}

// runOrgMemoryAdd implements `kern org memory add <content> [--type T]`.
func runOrgMemoryAdd(srv *enterprise.Server, rest []string) {
	memType := ""
	content := ""
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case "--type":
			if i+1 < len(rest) {
				memType = rest[i+1]
				i++
			}
		default:
			if content == "" {
				content = rest[i]
			}
		}
	}
	if content == "" {
		fatalUsage("org memory add: usage: kern org memory add <content> [--type T]")
	}
	m, err := srv.OrgMemory().Add(domain.Memory{Content: content, Type: domain.MemoryType(memType)})
	if err != nil {
		fatal("org memory add: %v", err)
	}
	fmt.Printf("added memory %s\n", m.ID)
}

// runOrgAudit prints the shared org audit log
// (id<TAB>action<TAB>resource<TAB>result).
func runOrgAudit(srv *enterprise.Server, rest []string, jsonOut bool) {
	f, _, err := parseFlags(rest)
	if err != nil {
		fatalUsage("org audit: %v", err)
	}
	entries := srv.OrgAudit().All()
	if jsonOut || f.json {
		type auditJSON struct {
			ID        string    `json:"id"`
			Action    string    `json:"action"`
			Resource  string    `json:"resource"`
			Result    string    `json:"result"`
			AgentID   string    `json:"agent_id,omitempty"`
			Timestamp time.Time `json:"timestamp,omitempty"`
		}
		out := make([]auditJSON, 0, len(entries))
		for _, e := range entries {
			out = append(out, auditJSON{
				ID: e.ID, Action: e.Action, Resource: e.Resource, Result: e.Result,
				AgentID: e.AgentID, Timestamp: e.Timestamp,
			})
		}
		printJSON(map[string]any{"entries": out, "count": len(out)})
		return
	}
	for _, e := range entries {
		fmt.Printf("%s\t%s\t%s\t%s\n", e.ID, e.Action, e.Resource, e.Result)
	}
}

// runOrgSearch implements `kern org search <query>`: cross-project symbol
// search printing repo/symbol<TAB>score lines.
func runOrgSearch(srv *enterprise.Server, rest []string, jsonOut bool) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("org search: %v", err)
	}
	if len(args) < 1 {
		fatalUsage("org search: usage: kern org search <query>")
	}
	hits := srv.OrgSearch(args[0], 20)
	if jsonOut || f.json {
		type hitJSON struct {
			Repo   string       `json:"repo"`
			Root   string       `json:"root"`
			Symbol index.Symbol `json:"symbol"`
			Score  int          `json:"score"`
		}
		out := make([]hitJSON, 0, len(hits))
		for _, h := range hits {
			out = append(out, hitJSON{Repo: h.Repo, Root: h.Root, Symbol: h.Symbol, Score: h.Score})
		}
		printJSON(map[string]any{"hits": out, "count": len(out)})
		return
	}
	for _, h := range hits {
		fmt.Printf("%s/%s\t%d\n", h.Repo, h.Symbol.Name, h.Score)
	}
}
