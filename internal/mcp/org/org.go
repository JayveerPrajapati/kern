// Package org owns the org-family tool bodies (kern_org_projects,
// kern_org_agents, kern_org_teams, kern_org_memory, kern_org_tasks,
// kern_org_search, kern_org_audit) as plain functions over the resolved
// enterprise server. The whole family is Server-independent: it only ever
// needed the tool args and the enterprise package, so it moved wholesale.
package org

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/intel"

	"github.com/JayveerPrajapati/kern/internal/enterprise"
	"github.com/JayveerPrajapati/kern/internal/mcp/mcpargs"
)

// projectNameFromRoot derives a project name from a directory path. It mirrors
// cmd/kern's projectNameFromRoot so the MCP org tools and the CLI name the
// default single project identically.
func projectNameFromRoot(root string) string {
	name := filepath.Base(root)
	if name == "." || name == string(filepath.Separator) || name == "" {
		if abs, err := filepath.Abs(root); err == nil {
			name = filepath.Base(abs)
		}
	}
	return name
}

// newOrgSrv registers a single project on srv, validating the name and root
// before the registration is attempted.
func newOrgSrv(srv *enterprise.Server, name, root string) (*enterprise.Server, error) {
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("enterprise: project name must not be empty")
	}
	if strings.TrimSpace(root) == "" {
		return nil, fmt.Errorf("enterprise: invalid project root %q", root)
	}
	if err := srv.Register(name, root); err != nil {
		return nil, err
	}
	return srv, nil
}

// orgServer builds an enterprise.Server for the org-admin tools. It registers
// either the projects from the optional `projects` arg — comma-separated
// NAME=PATH pairs, each validated (empty name or bad root is an error) — or a
// single default project named after the absolute `root` (default ".").
func OrgServer(args map[string]any) (*enterprise.Server, error) {
	root := mcpargs.ArgString(args, "root")
	if root == "" {
		root = "."
	}
	srv := enterprise.New()
	projArg := mcpargs.ArgString(args, "projects")
	if projArg == "" {
		return newOrgSrv(srv, projectNameFromRoot(root), root)
	}
	for _, pair := range strings.Split(projArg, ",") {
		pair = strings.TrimSpace(pair)
		name, path, ok := strings.Cut(pair, "=")
		name = strings.TrimSpace(name)
		path = strings.TrimSpace(path)
		if !ok || name == "" {
			return nil, fmt.Errorf("enterprise: invalid project pair %q (want NAME=PATH)", pair)
		}
		if path == "" {
			return nil, fmt.Errorf("enterprise: invalid project root %q (want NAME=PATH)", pair)
		}
		if err := srv.Register(name, path); err != nil {
			return nil, err
		}
	}
	return srv, nil
}

// orgTeamsServer builds an enterprise server for team creation: the default
// single project from root is registered so team project references can
// validate, but the `projects` arg is reserved for the team's membership list
// (plain project names), not NAME=PATH registration pairs.
func orgTeamsServer(args map[string]any) (*enterprise.Server, error) {
	root := mcpargs.ArgString(args, "root")
	if root == "" {
		root = "."
	}
	return newOrgSrv(enterprise.New(), projectNameFromRoot(root), root)
}

// orgJSON renders a result map as an indented JSON string, the same shape
// other handlers return (e.g. handleEvidenceAnchor).
func orgJSON(v map[string]any) (string, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", fmt.Errorf("enterprise: marshal result: %w", err)
	}
	return string(data), nil
}

// splitCSV splits a comma-separated argument into trimmed non-empty parts.
func splitCSV(v string) []string {
	var out []string
	for _, part := range strings.Split(v, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// handleOrgProjects implements kern_org_projects (C11): list registered
// projects as {projects:[{name,root}],count}.
func Projects(ctx context.Context, args map[string]any) (string, error) {
	srv, err := OrgServer(args)
	if err != nil {
		return "", err
	}
	type projectView struct {
		Name string `json:"name"`
		Root string `json:"root"`
	}
	projects := srv.Projects()
	view := make([]projectView, 0, len(projects))
	for _, p := range projects {
		view = append(view, projectView{Name: p.Name, Root: p.Root})
	}
	return orgJSON(map[string]any{"projects": view, "count": len(view)})
}

// handleOrgAgents implements kern_org_agents (C11): action=list (default)
// returns {agents:[{id,name,type}],count}; action=register creates an agent
// from id/name (type defaults to "default") and returns it.
func Agents(ctx context.Context, args map[string]any) (string, error) {
	action := mcpargs.ArgString(args, "action")
	if action == "" {
		action = "list"
	}
	srv, err := OrgServer(args)
	if err != nil {
		return "", err
	}
	switch action {
	case "register":
		return OrgAgentsRegister(srv, args)
	case "list":
		return OrgAgentsList(srv, args)
	default:
		return "", fmt.Errorf("kern_org_agents: unknown action %q (list|register)", action)
	}
}

func OrgAgentsList(srv *enterprise.Server, args map[string]any) (string, error) {
	type agentView struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Type string `json:"type"`
	}
	agents := srv.Agents()
	view := make([]agentView, 0, len(agents))
	for _, a := range agents {
		view = append(view, agentView{ID: a.ID, Name: a.Name, Type: a.Type})
	}
	return orgJSON(map[string]any{"agents": view, "count": len(view)})
}

func OrgAgentsRegister(srv *enterprise.Server, args map[string]any) (string, error) {
	id := mcpargs.ArgString(args, "id")
	name := mcpargs.ArgString(args, "name")
	if id == "" {
		return "", fmt.Errorf("kern_org_agents: id is required for action 'register'")
	}
	if name == "" {
		return "", fmt.Errorf("kern_org_agents: name is required for action 'register'")
	}
	agentType := mcpargs.ArgString(args, "type")
	if agentType == "" {
		agentType = "default"
	}
	agent := governance.NewAgent(id, name, agentType, nil)
	if err := srv.RegisterAgent(agent); err != nil {
		return "", err // duplicate id -> "enterprise: agent %q already registered" (409 semantics)
	}
	return orgJSON(map[string]any{"id": agent.ID, "name": agent.Name, "type": agent.Type})
}

// handleOrgTeams implements kern_org_teams (C11): action=list (default) |
// show | create | remove over the org team registry.
func Teams(ctx context.Context, args map[string]any) (string, error) {
	action := mcpargs.ArgString(args, "action")
	if action == "" {
		action = "list"
	}
	switch action {
	case "create":
		srv, err := orgTeamsServer(args)
		if err != nil {
			return "", err
		}
		return OrgTeamsCreate(srv, args)
	case "show":
		srv, err := OrgServer(args)
		if err != nil {
			return "", err
		}
		return OrgTeamsShow(srv, args)
	case "remove":
		srv, err := OrgServer(args)
		if err != nil {
			return "", err
		}
		return OrgTeamsRemove(srv, args)
	case "list":
		srv, err := OrgServer(args)
		if err != nil {
			return "", err
		}
		return OrgTeamsList(srv, args)
	default:
		return "", fmt.Errorf("kern_org_teams: unknown action %q (list|show|create|remove)", action)
	}
}

func OrgTeamsList(srv *enterprise.Server, args map[string]any) (string, error) {
	type teamView struct {
		ID       string   `json:"id"`
		Name     string   `json:"name"`
		Projects []string `json:"projects"`
		Members  []string `json:"members"`
	}
	teams := srv.Teams()
	view := make([]teamView, 0, len(teams))
	for _, t := range teams {
		view = append(view, teamView{ID: t.ID, Name: t.Name, Projects: t.Projects, Members: t.Members})
	}
	return orgJSON(map[string]any{"teams": view, "count": len(view)})
}

func OrgTeamsShow(srv *enterprise.Server, args map[string]any) (string, error) {
	id := mcpargs.ArgString(args, "id")
	if id == "" {
		return "", fmt.Errorf("kern_org_teams: id is required for action 'show'")
	}
	team, ok := srv.Team(id)
	if !ok {
		return "", fmt.Errorf("enterprise: team %q not found", id)
	}
	return orgJSON(map[string]any{
		"id":       team.ID,
		"name":     team.Name,
		"projects": team.Projects,
		"members":  team.Members,
	})
}

func OrgTeamsCreate(srv *enterprise.Server, args map[string]any) (string, error) {
	id := mcpargs.ArgString(args, "id")
	name := mcpargs.ArgString(args, "name")
	if id == "" {
		return "", fmt.Errorf("kern_org_teams: id is required for action 'create'")
	}
	if name == "" {
		return "", fmt.Errorf("kern_org_teams: name is required for action 'create'")
	}
	team := enterprise.OrgTeam{ID: id, Name: name}
	if ps := mcpargs.ArgString(args, "projects"); ps != "" {
		team.Projects = splitCSV(ps)
	}
	if ms := mcpargs.ArgString(args, "members"); ms != "" {
		team.Members = splitCSV(ms)
	}
	if err := srv.CreateTeam(team); err != nil {
		return "", err // validation errors (unknown project/agent) surface as-is
	}
	return orgJSON(map[string]any{
		"id":       team.ID,
		"name":     team.Name,
		"projects": team.Projects,
		"members":  team.Members,
	})
}

func OrgTeamsRemove(srv *enterprise.Server, args map[string]any) (string, error) {
	id := mcpargs.ArgString(args, "id")
	if id == "" {
		return "", fmt.Errorf("kern_org_teams: id is required for action 'remove'")
	}
	if err := srv.RemoveTeam(id); err != nil {
		return "", err // unknown team -> "enterprise: team %q not found"
	}
	return orgJSON(map[string]any{"removed": id})
}

// handleOrgMemory implements kern_org_memory (C11): action=list (default)
// returns {memories:[{id,content,type}],count}; action=add stores a memory
// from content with optional type and returns the added memory.
func Memory(ctx context.Context, args map[string]any) (string, error) {
	action := mcpargs.ArgString(args, "action")
	if action == "" {
		action = "list"
	}
	srv, err := OrgServer(args)
	if err != nil {
		return "", err
	}
	switch action {
	case "add":
		return OrgMemoryAdd(srv, args)
	case "list":
		return OrgMemoryList(srv, args)
	default:
		return "", fmt.Errorf("kern_org_memory: unknown action %q (list|add)", action)
	}
}

func OrgMemoryList(srv *enterprise.Server, args map[string]any) (string, error) {
	type memoryView struct {
		ID      string `json:"id"`
		Content string `json:"content"`
		Type    string `json:"type"`
	}
	memories, err := srv.OrgMemory().List("")
	if err != nil {
		return "", fmt.Errorf("kern_org_memory: list: %w", err)
	}
	view := make([]memoryView, 0, len(memories))
	for _, m := range memories {
		view = append(view, memoryView{ID: m.ID, Content: m.Content, Type: string(m.Type)})
	}
	return orgJSON(map[string]any{"memories": view, "count": len(view)})
}

func OrgMemoryAdd(srv *enterprise.Server, args map[string]any) (string, error) {
	content := mcpargs.ArgString(args, "content")
	if content == "" {
		return "", fmt.Errorf("kern_org_memory: content is required for action 'add'")
	}
	m, err := srv.OrgMemory().Add(domain.Memory{
		Content: content,
		Type:    domain.MemoryType(mcpargs.ArgString(args, "type")),
	})
	if err != nil {
		return "", fmt.Errorf("kern_org_memory: add: %w", err)
	}
	return orgJSON(map[string]any{"id": m.ID, "content": m.Content, "type": string(m.Type)})
}

// handleOrgTasks implements kern_org_tasks (C11): aggregate task visibility
// across registered projects as {projects:{name:[{id,state,intent,type}]},
// total}. Tasks exist only for projects whose app has been built; a fresh
// enterprise server reports an empty map.
func Tasks(ctx context.Context, args map[string]any) (string, error) {
	srv, err := OrgServer(args)
	if err != nil {
		return "", err
	}
	projects := srv.OrgTasks()
	total := 0
	for _, tasks := range projects {
		total += len(tasks)
	}
	return orgJSON(map[string]any{"projects": projects, "total": total})
}

// handleOrgSearch implements kern_org_search (C11): cross-project symbol
// search via the multi-repo registry, returning {hits:[{repo,root,symbol,
// score}],count} for the top 20 matches.
func Search(ctx context.Context, args map[string]any) (string, error) {
	q := mcpargs.ArgString(args, "q")
	if q == "" {
		return "", fmt.Errorf("kern_org_search: q is required")
	}
	srv, err := OrgServer(args)
	if err != nil {
		return "", err
	}
	hits := srv.OrgSearch(q, 20)
	if hits == nil {
		hits = []intel.RepoHit{}
	}
	return orgJSON(map[string]any{"hits": hits, "count": len(hits)})
}

// handleOrgAudit implements kern_org_audit (C11): the org-level audit log as
// {entries:[...],count}. Entry field names match AuditEntry's real JSON field
// names as-is (ID, Timestamp, AgentID, Action, Resource, Result).
func Audit(ctx context.Context, args map[string]any) (string, error) {
	srv, err := OrgServer(args)
	if err != nil {
		return "", err
	}
	type entryView struct {
		ID        string    `json:"ID"`
		Timestamp time.Time `json:"Timestamp"`
		AgentID   string    `json:"AgentID"`
		Action    string    `json:"Action"`
		Resource  string    `json:"Resource"`
		Result    string    `json:"Result"`
	}
	entries := srv.OrgAudit().All()
	view := make([]entryView, 0, len(entries))
	for _, e := range entries {
		view = append(view, entryView{
			ID:        e.ID,
			Timestamp: e.Timestamp,
			AgentID:   e.AgentID,
			Action:    e.Action,
			Resource:  e.Resource,
			Result:    e.Result,
		})
	}
	return orgJSON(map[string]any{"entries": view, "count": len(view)})
}
