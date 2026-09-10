package mcp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/governance"
)

// orgProjectsResp is the decoded shape of kern_org_projects.
type orgProjectsResp struct {
	Projects []struct {
		Name string `json:"name"`
		Root string `json:"root"`
	} `json:"projects"`
	Count int `json:"count"`
}

// TestHandleOrgProjects_DefaultRoot: with no projects arg, the default single
// project is named after the absolute root's base name.
func TestHandleOrgProjects_DefaultRoot(t *testing.T) {
	s := newTestServer()
	root := t.TempDir()
	res, err := s.handleOrgProjects(context.Background(), map[string]any{"root": root})
	if err != nil {
		t.Fatalf("org projects (default root) error: %v", err)
	}
	var resp orgProjectsResp
	if err := json.Unmarshal([]byte(res), &resp); err != nil {
		t.Fatalf("decode projects: %v; raw=%s", err, res)
	}
	if resp.Count != 1 {
		t.Fatalf("expected 1 project, got %d", resp.Count)
	}
	p := resp.Projects[0]
	if p.Name != filepath.Base(root) {
		t.Errorf("expected project name %q, got %q", filepath.Base(root), p.Name)
	}
	if p.Root != root {
		t.Errorf("expected project root %q, got %q", root, p.Root)
	}
}

// TestHandleOrgProjects_ProjectPairs: comma-separated NAME=PATH pairs register
// multiple projects; an invalid pair (no '=') and an empty name are errors.
func TestHandleOrgProjects_ProjectPairs(t *testing.T) {
	s := newTestServer()
	dirA := t.TempDir()
	dirB := t.TempDir()

	res, err := s.handleOrgProjects(context.Background(), map[string]any{
		"projects": "alpha=" + dirA + ",beta=" + dirB,
	})
	if err != nil {
		t.Fatalf("org projects (pairs) error: %v", err)
	}
	var resp orgProjectsResp
	if err := json.Unmarshal([]byte(res), &resp); err != nil {
		t.Fatalf("decode projects: %v; raw=%s", err, res)
	}
	if resp.Count != 2 {
		t.Fatalf("expected 2 projects, got %d", resp.Count)
	}
	byName := map[string]string{}
	for _, p := range resp.Projects {
		byName[p.Name] = p.Root
	}
	if byName["alpha"] != dirA || byName["beta"] != dirB {
		t.Errorf("unexpected project map: %v", byName)
	}

	if _, err := s.handleOrgProjects(context.Background(), map[string]any{"projects": "solo"}); err == nil {
		t.Error("expected invalid pair (no '=') to error")
	} else if !strings.Contains(err.Error(), "invalid project pair") {
		t.Errorf("expected invalid-pair error, got: %v", err)
	}

	if _, err := s.handleOrgProjects(context.Background(), map[string]any{"projects": "=empty"}); err == nil {
		t.Error("expected empty-name pair to error")
	}
}

// TestHandleOrgAgents_RegisterListDuplicate: a shared server registers an
// agent, rejects a duplicate id, and lists the agent back.
func TestHandleOrgAgents_RegisterListDuplicate(t *testing.T) {
	srv, err := orgServer(map[string]any{})
	if err != nil {
		t.Fatalf("org server: %v", err)
	}

	res, err := orgAgentsRegister(srv, map[string]any{"id": "a1", "name": "Alpha"})
	if err != nil {
		t.Fatalf("register agent error: %v", err)
	}
	var created map[string]any
	if err := json.Unmarshal([]byte(res), &created); err != nil {
		t.Fatalf("decode created agent: %v; raw=%s", err, res)
	}
	if created["id"] != "a1" || created["name"] != "Alpha" || created["type"] != "default" {
		t.Errorf("unexpected created agent: %v", created)
	}

	// Duplicate id -> 409-style error.
	if _, err := orgAgentsRegister(srv, map[string]any{"id": "a1", "name": "Alpha"}); err == nil {
		t.Fatal("expected duplicate agent registration to error")
	} else if !strings.Contains(err.Error(), "already registered") {
		t.Errorf("expected already-registered error, got: %v", err)
	}

	// List round-trip.
	list, err := orgAgentsList(srv, map[string]any{})
	if err != nil {
		t.Fatalf("list agents error: %v", err)
	}
	var listResp struct {
		Agents []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			Type string `json:"type"`
		} `json:"agents"`
		Count int `json:"count"`
	}
	if err := json.Unmarshal([]byte(list), &listResp); err != nil {
		t.Fatalf("decode agents: %v; raw=%s", err, list)
	}
	if listResp.Count != 1 || len(listResp.Agents) != 1 || listResp.Agents[0].ID != "a1" {
		t.Errorf("unexpected agent list: %+v", listResp)
	}

	// Handler-level: register requires id and name.
	s := newTestServer()
	if _, err := s.handleOrgAgents(context.Background(), map[string]any{"action": "register"}); err == nil {
		t.Error("expected register without id to error")
	}
	if _, err := s.handleOrgAgents(context.Background(), map[string]any{"action": "register", "id": "x"}); err == nil {
		t.Error("expected register without name to error")
	}
}

// TestHandleOrgTeams_RoundTrip: create -> show -> list -> remove round-trip
// against a shared server, plus unknown-project validation and 404-style show.
func TestHandleOrgTeams_RoundTrip(t *testing.T) {
	root := t.TempDir()
	srv, err := orgServer(map[string]any{"root": root})
	if err != nil {
		t.Fatalf("org server: %v", err)
	}
	// Register an agent so a team member reference validates.
	if err := srv.RegisterAgent(governance.NewAgent("a1", "Alpha", "default", nil)); err != nil {
		t.Fatalf("register agent: %v", err)
	}
	projName := filepath.Base(root)

	// Create: project reference to the registered default project is valid.
	res, err := orgTeamsCreate(srv, map[string]any{
		"id":       "t1",
		"name":     "Team One",
		"projects": projName,
		"members":  "a1",
	})
	if err != nil {
		t.Fatalf("create team error: %v", err)
	}
	var team map[string]any
	if err := json.Unmarshal([]byte(res), &team); err != nil {
		t.Fatalf("decode created team: %v; raw=%s", err, res)
	}
	if team["id"] != "t1" || team["name"] != "Team One" {
		t.Errorf("unexpected created team: %v", team)
	}

	// Show.
	show, err := orgTeamsShow(srv, map[string]any{"id": "t1"})
	if err != nil {
		t.Fatalf("show team error: %v", err)
	}
	if !strings.Contains(show, `"id": "t1"`) {
		t.Errorf("show did not return team t1: %s", show)
	}

	// List.
	list, err := orgTeamsList(srv, map[string]any{})
	if err != nil {
		t.Fatalf("list teams error: %v", err)
	}
	var listResp struct {
		Teams []map[string]any `json:"teams"`
		Count int              `json:"count"`
	}
	if err := json.Unmarshal([]byte(list), &listResp); err != nil {
		t.Fatalf("decode teams: %v; raw=%s", err, list)
	}
	if listResp.Count != 1 || len(listResp.Teams) != 1 {
		t.Errorf("expected 1 team, got %+v", listResp)
	}

	// Unknown-project validation error.
	if _, err := orgTeamsCreate(srv, map[string]any{"id": "t2", "name": "Bad", "projects": "nope"}); err == nil {
		t.Fatal("expected unknown-project validation error")
	} else if !strings.Contains(err.Error(), "unknown project") {
		t.Errorf("expected unknown-project error, got: %v", err)
	}

	// Unknown-agent validation error.
	if _, err := orgTeamsCreate(srv, map[string]any{"id": "t3", "name": "Bad", "members": "ghost"}); err == nil {
		t.Fatal("expected unknown-agent validation error")
	} else if !strings.Contains(err.Error(), "unknown agent") {
		t.Errorf("expected unknown-agent error, got: %v", err)
	}

	// Remove.
	rem, err := orgTeamsRemove(srv, map[string]any{"id": "t1"})
	if err != nil {
		t.Fatalf("remove team error: %v", err)
	}
	if !strings.Contains(rem, `"removed": "t1"`) {
		t.Errorf("unexpected remove result: %s", rem)
	}

	// Show of a missing team -> 404-style error string.
	if _, err := orgTeamsShow(srv, map[string]any{"id": "t1"}); err == nil {
		t.Fatal("expected show of removed team to error")
	} else if !strings.Contains(err.Error(), `team "t1" not found`) {
		t.Errorf("expected team-not-found error, got: %v", err)
	}

	// Handler-level: create requires id and name.
	s := newTestServer()
	if _, err := s.handleOrgTeams(context.Background(), map[string]any{"action": "create"}); err == nil {
		t.Error("expected create without id/name to error")
	}
}

// TestHandleOrgMemory_AddList: a memory added via the org store appears in the
// org list; add without content is an error.
func TestHandleOrgMemory_AddList(t *testing.T) {
	srv, err := orgServer(map[string]any{"root": t.TempDir()})
	if err != nil {
		t.Fatalf("org server: %v", err)
	}
	content := "org memory content " + t.Name()

	add, err := orgMemoryAdd(srv, map[string]any{"content": content, "type": "lesson"})
	if err != nil {
		t.Fatalf("add memory error: %v", err)
	}
	var added map[string]any
	if err := json.Unmarshal([]byte(add), &added); err != nil {
		t.Fatalf("decode added memory: %v; raw=%s", err, add)
	}
	if added["content"] != content || added["type"] != "lesson" || added["id"] == "" {
		t.Errorf("unexpected added memory: %v", added)
	}

	list, err := orgMemoryList(srv, map[string]any{})
	if err != nil {
		t.Fatalf("list memories error: %v", err)
	}
	var listResp struct {
		Memories []struct {
			ID      string `json:"id"`
			Content string `json:"content"`
			Type    string `json:"type"`
		} `json:"memories"`
		Count int `json:"count"`
	}
	if err := json.Unmarshal([]byte(list), &listResp); err != nil {
		t.Fatalf("decode memories: %v; raw=%s", err, list)
	}
	found := false
	for _, m := range listResp.Memories {
		if m.Content == content {
			found = true
			if m.Type != "lesson" {
				t.Errorf("expected type lesson, got %q", m.Type)
			}
		}
	}
	if !found {
		t.Errorf("added memory %q not found in org list", content)
	}

	if _, err := orgMemoryAdd(srv, map[string]any{}); err == nil {
		t.Error("expected add without content to error")
	}
}

// TestHandleOrgAudit_EmptyShape: a fresh org reports the audit entries shape
// with an empty list.
func TestHandleOrgAudit_EmptyShape(t *testing.T) {
	s := newTestServer()
	res, err := s.handleOrgAudit(context.Background(), map[string]any{"root": t.TempDir()})
	if err != nil {
		t.Fatalf("org audit error: %v", err)
	}
	var resp struct {
		Entries []map[string]any `json:"entries"`
		Count   int              `json:"count"`
	}
	if err := json.Unmarshal([]byte(res), &resp); err != nil {
		t.Fatalf("decode audit: %v; raw=%s", err, res)
	}
	if resp.Count != 0 || len(resp.Entries) != 0 {
		t.Errorf("expected empty audit entries, got %+v", resp)
	}
}

// TestHandleOrgSearch_RequiresQ: kern_org_search errors without q and returns
// the hits shape when q is present.
func TestHandleOrgSearch_RequiresQ(t *testing.T) {
	s := newTestServer()
	if _, err := s.handleOrgSearch(context.Background(), map[string]any{"root": t.TempDir()}); err == nil {
		t.Fatal("expected search without q to error")
	} else if !strings.Contains(err.Error(), "q is required") {
		t.Errorf("expected q-required error, got: %v", err)
	}

	res, err := s.handleOrgSearch(context.Background(), map[string]any{"q": "nothing", "root": t.TempDir()})
	if err != nil {
		t.Fatalf("org search error: %v", err)
	}
	var resp struct {
		Hits  []map[string]any `json:"hits"`
		Count int              `json:"count"`
	}
	if err := json.Unmarshal([]byte(res), &resp); err != nil {
		t.Fatalf("decode search: %v; raw=%s", err, res)
	}
	if resp.Hits == nil {
		t.Error("expected hits to be an array (possibly empty)")
	}
}
