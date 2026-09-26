package org

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// orgRBACFixture sets up an org root with the given agent->role assignments
// (written to <org-root>/.kern/org-rbac.json) and arms the org-mode env
// pairing (KERN_ORG_ROOT + KERN_RBAC_DEFAULT_DENY=1) so the entry-point org
// tools — which build their own enterprise.Server per call — resolve roles
// exactly like production. Returns the org root.
func orgRBACFixture(t *testing.T, roles map[string]string) string {
	t.Helper()
	root := t.TempDir()
	kdir := filepath.Join(root, ".kern")
	if err := os.MkdirAll(kdir, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(roles)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(kdir, "org-rbac.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KERN_ORG_ROOT", root)
	t.Setenv("KERN_RBAC_DEFAULT_DENY", "1")
	return root
}

func TestProjectNameFromRoot(t *testing.T) {
	if got := projectNameFromRoot("/a/b/c"); got != "c" {
		t.Errorf("projectNameFromRoot(/a/b/c) = %q, want c", got)
	}
	if got := projectNameFromRoot("/"); got == "" {
		t.Error("projectNameFromRoot(/) = empty, want abs base fallback")
	}
	if got := projectNameFromRoot(""); got == "" {
		t.Error("projectNameFromRoot(empty) = empty, want abs base fallback")
	}
}

func TestNewOrgSrvValidation(t *testing.T) {
	srv, err := OrgServer(map[string]any{})
	if err != nil {
		t.Fatalf("org server: %v", err)
	}
	if _, err := newOrgSrv(srv, "", "/tmp"); err == nil {
		t.Error("empty name: want error, got nil")
	}
	if _, err := newOrgSrv(srv, "name", ""); err == nil {
		t.Error("empty root: want error, got nil")
	}
}

func TestOrgServerProjectPairs(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()
	srv, err := OrgServer(map[string]any{"projects": "alpha=" + dirA + ",beta=" + dirB})
	if err != nil {
		t.Fatalf("org server (pairs): %v", err)
	}
	projs := srv.Projects()
	if len(projs) != 2 {
		t.Fatalf("expected 2 projects, got %d", len(projs))
	}
	byName := map[string]string{}
	for _, p := range projs {
		byName[p.Name] = p.Root
	}
	if byName["alpha"] != dirA || byName["beta"] != dirB {
		t.Errorf("unexpected project map: %v", byName)
	}

	// Invalid pairs fail loud.
	for _, bad := range []map[string]any{
		{"projects": "solo"},                    // no '='
		{"projects": "=empty"},                  // empty name
		{"projects": "name="},                   // empty path
		{"projects": "alpha=" + dirA + ",beta"}, // second pair malformed
	} {
		if _, err := OrgServer(bad); err == nil {
			t.Errorf("OrgServer(%v): want error, got nil", bad)
		}
	}
}

func TestSplitCSV(t *testing.T) {
	got := splitCSV(" a , b,  ,c ")
	want := []string{"a", "b", "c"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Errorf("splitCSV = %v, want %v", got, want)
	}
	if got := splitCSV(" , , "); got != nil && len(got) != 0 {
		t.Errorf("splitCSV(all-empty) = %v, want empty", got)
	}
	if got := splitCSV(""); got != nil && len(got) != 0 {
		t.Errorf("splitCSV(empty) = %v, want empty", got)
	}
}

func TestOrgJSON(t *testing.T) {
	out, err := orgJSON(map[string]any{"count": 1, "name": "x"})
	if err != nil {
		t.Fatalf("orgJSON error: %v", err)
	}
	var back map[string]any
	if err := json.Unmarshal([]byte(out), &back); err != nil {
		t.Fatalf("orgJSON not valid JSON: %v", err)
	}
	if back["count"] != float64(1) {
		t.Errorf("orgJSON count = %v, want 1", back["count"])
	}
	// Unmarshalable value -> loud error.
	if _, err := orgJSON(map[string]any{"bad": make(chan int)}); err == nil {
		t.Error("orgJSON with channel: want error, got nil")
	}
}

func TestProjectsShape(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	orgRBACFixture(t, map[string]string{"root": "org-admin"})
	out, err := Projects(context.Background(), map[string]any{"root": root, "actor_id": "root"})
	if err != nil {
		t.Fatalf("Projects error: %v", err)
	}
	var resp struct {
		Projects []struct {
			Name string `json:"name"`
			Root string `json:"root"`
		} `json:"projects"`
		Count int `json:"count"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("decode projects: %v; raw=%s", err, out)
	}
	if resp.Count != 1 || len(resp.Projects) != 1 {
		t.Fatalf("expected 1 project, got %+v", resp)
	}
	if resp.Projects[0].Name != filepath.Base(root) || resp.Projects[0].Root != root {
		t.Errorf("unexpected project view: %+v", resp.Projects[0])
	}
}

func TestAgentsUnknownAction(t *testing.T) {
	if _, err := Agents(context.Background(), map[string]any{"action": "bogus"}); err == nil {
		t.Error("unknown agents action: want error, got nil")
	} else if !strings.Contains(err.Error(), "unknown action") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestTeamsUnknownAction(t *testing.T) {
	if _, err := Teams(context.Background(), map[string]any{"action": "bogus"}); err == nil {
		t.Error("unknown teams action: want error, got nil")
	} else if !strings.Contains(err.Error(), "unknown action") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestMemoryDispatch(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	srv, err := OrgServer(map[string]any{"root": t.TempDir()})
	if err != nil {
		t.Fatalf("org server: %v", err)
	}
	if err := srv.AddUserBy("root", "org-admin", "bootstrap"); err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	base := map[string]any{"actor_id": "root"}
	// Unknown action errors.
	if _, err := Memory(context.Background(), map[string]any{"action": "bogus"}); err == nil {
		t.Error("unknown memory action: want error, got nil")
	}
	// Add without content errors.
	if _, err := OrgMemoryAdd(srv, base); err == nil {
		t.Error("add without content: want error, got nil")
	}
	// Add+list round trip.
	content := "leaf memory " + t.Name()
	add, err := OrgMemoryAdd(srv, with(base, "content", content))
	if err != nil {
		t.Fatalf("add memory: %v", err)
	}
	if !strings.Contains(add, content) {
		t.Errorf("add result missing content: %s", add)
	}
	list, err := OrgMemoryList(srv, base)
	if err != nil {
		t.Fatalf("list memories: %v", err)
	}
	if !strings.Contains(list, content) {
		t.Errorf("list missing added memory: %s", list)
	}
}

func TestTasksEmptyShape(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	orgRBACFixture(t, map[string]string{"root": "org-admin"})
	out, err := Tasks(context.Background(), map[string]any{"root": t.TempDir(), "actor_id": "root"})
	if err != nil {
		t.Fatalf("Tasks error: %v", err)
	}
	var resp struct {
		Projects map[string][]map[string]any `json:"projects"`
		Total    int                         `json:"total"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("decode tasks: %v; raw=%s", err, out)
	}
	if resp.Total != 0 {
		t.Errorf("expected total 0, got %d", resp.Total)
	}
}

func TestSearchRequiresQ(t *testing.T) {
	if _, err := Search(context.Background(), map[string]any{}); err == nil {
		t.Error("Search without q: want error, got nil")
	} else if !strings.Contains(err.Error(), "q is required") {
		t.Errorf("unexpected error: %v", err)
	}
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	orgRBACFixture(t, map[string]string{"root": "org-admin"})
	out, err := Search(context.Background(), map[string]any{"q": "nothing", "root": t.TempDir(), "actor_id": "root"})
	if err != nil {
		t.Fatalf("Search error: %v", err)
	}
	var resp struct {
		Hits  []map[string]any `json:"hits"`
		Count int              `json:"count"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("decode search: %v; raw=%s", err, out)
	}
	if resp.Hits == nil {
		t.Error("expected hits array (possibly empty)")
	}
}

func TestAuditShape(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	orgRBACFixture(t, map[string]string{"root": "org-admin"})
	out, err := Audit(context.Background(), map[string]any{"root": t.TempDir(), "actor_id": "root"})
	if err != nil {
		t.Fatalf("Audit error: %v", err)
	}
	var resp struct {
		Entries []map[string]any `json:"entries"`
		Count   int              `json:"count"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("decode audit: %v; raw=%s", err, out)
	}
	if resp.Count != 0 || len(resp.Entries) != 0 {
		t.Errorf("expected empty audit, got %+v", resp)
	}
}

// TestOrgToolsRBAC pins the per-action RBAC layer across the org resource
// tools (the recon finding: only kern_org_user enforced it). Admin passes
// mutations and reads; a member passes reads but is denied mutations with a
// clear denial naming the action; an unassigned actor is denied on both.
// Entry points build their own enterprise.Server per call, so roles come
// from the org-rbac.json store exactly like production (orgRBACFixture).
func TestOrgToolsRBAC(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	orgRBACFixture(t, map[string]string{"root": "org-admin", "member": "org-member"})
	ctx := context.Background()

	// org-admin passes every mutation.
	mutations := []struct {
		name string
		call func() error
	}{
		{"agents register", func() error {
			_, err := Agents(ctx, map[string]any{"action": "register", "id": "a1", "name": "Agent One", "actor_id": "root"})
			return err
		}},
		{"teams create", func() error {
			_, err := Teams(ctx, map[string]any{"action": "create", "id": "t1", "name": "Team One", "actor_id": "root"})
			return err
		}},
		{"memory add", func() error {
			_, err := Memory(ctx, map[string]any{"action": "add", "content": "org note", "actor_id": "root"})
			return err
		}},
	}
	for _, m := range mutations {
		if err := m.call(); err != nil {
			t.Errorf("org-admin %s: %v", m.name, err)
		}
	}

	// org-member: denied on every mutation — the denial names the action.
	for _, m := range mutations {
		args := map[string]any{"actor_id": "member"}
		switch {
		case m.name == "agents register":
			args["action"], args["id"], args["name"] = "register", "a2", "Agent Two"
		case m.name == "teams create":
			args["action"], args["id"], args["name"] = "create", "t2", "Team Two"
		case m.name == "memory add":
			args["action"], args["content"] = "add", "member note"
		}
		_, err := callOrg(ctx, m.name, args)
		if err == nil {
			t.Errorf("member %s: want RBAC denial, got nil", m.name)
			continue
		}
		if !strings.Contains(err.Error(), "not allowed to perform org action") {
			t.Errorf("member %s: expected RBAC denial naming the action, got: %v", m.name, err)
		}
	}
	// Team removal is a mutation too.
	if _, err := Teams(ctx, map[string]any{"action": "remove", "id": "t1", "actor_id": "member"}); err == nil ||
		!strings.Contains(err.Error(), "not allowed to perform org action") {
		t.Errorf("member teams remove: want RBAC denial, got: %v", err)
	}

	// org-member passes every read.
	reads := []struct {
		name string
		call func() error
	}{
		{"projects", func() error {
			_, err := Projects(ctx, map[string]any{"root": t.TempDir(), "actor_id": "member"})
			return err
		}},
		{"agents list", func() error {
			_, err := Agents(ctx, map[string]any{"action": "list", "actor_id": "member"})
			return err
		}},
		{"teams list", func() error {
			_, err := Teams(ctx, map[string]any{"action": "list", "actor_id": "member"})
			return err
		}},
		{"teams show", func() error {
			// RBAC must pass; the fresh per-call server has no team, so the
			// error must be "not found", never an RBAC denial.
			_, err := Teams(ctx, map[string]any{"action": "show", "id": "t1", "actor_id": "member"})
			if err != nil && strings.Contains(err.Error(), "not allowed to perform org action") {
				return err
			}
			return nil
		}},
		{"memory list", func() error {
			_, err := Memory(ctx, map[string]any{"action": "list", "actor_id": "member"})
			return err
		}},
		{"tasks", func() error {
			_, err := Tasks(ctx, map[string]any{"root": t.TempDir(), "actor_id": "member"})
			return err
		}},
		{"search", func() error {
			_, err := Search(ctx, map[string]any{"q": "nothing", "root": t.TempDir(), "actor_id": "member"})
			return err
		}},
		{"audit", func() error {
			_, err := Audit(ctx, map[string]any{"root": t.TempDir(), "actor_id": "member"})
			return err
		}},
	}
	for _, r := range reads {
		if err := r.call(); err != nil {
			t.Errorf("org-member %s: %v", r.name, err)
		}
	}

	// Unassigned actor: fail-closed denial on a read and a mutation.
	if _, err := Projects(ctx, map[string]any{"root": t.TempDir(), "actor_id": "ghost"}); err == nil ||
		!strings.Contains(err.Error(), "not a registered org user") {
		t.Errorf("unassigned projects: want fail-closed denial, got: %v", err)
	}
	if _, err := Agents(ctx, map[string]any{"action": "register", "id": "a3", "name": "Ghost", "actor_id": "ghost"}); err == nil ||
		!strings.Contains(err.Error(), "not a registered org user") {
		t.Errorf("unassigned agents register: want fail-closed denial, got: %v", err)
	}
}

// callOrg dispatches one org tool entry point by name (test helper).
func callOrg(ctx context.Context, tool string, args map[string]any) (string, error) {
	switch tool {
	case "agents register":
		return Agents(ctx, args)
	case "teams create":
		return Teams(ctx, args)
	case "memory add":
		return Memory(ctx, args)
	}
	return "", nil
}

// TestOrgToolsLeafRBACDenial pins RBAC at the leaf level with a seeded
// server (mirroring the kern_org_user denial test): a member's denied
// mutation must not take effect, member reads stay open, and unassigned
// actors are denied.
func TestOrgToolsLeafRBACDenial(t *testing.T) {
	srv, err := OrgServer(map[string]any{})
	if err != nil {
		t.Fatalf("OrgServer: %v", err)
	}
	if err := srv.AddUserBy("root", "org-admin", "bootstrap"); err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	if err := srv.AddUserBy("member", "developer", "root"); err != nil {
		t.Fatalf("seed member: %v", err)
	}
	// A member may list agents...
	if _, err := OrgAgentsList(srv, map[string]any{"actor_id": "member"}); err != nil {
		t.Errorf("member agents list: %v, want nil", err)
	}
	// ...but may not register one — and the denied mutation must not create it.
	if _, err := OrgAgentsRegister(srv, map[string]any{"actor_id": "member", "id": "mallory", "name": "Mallory"}); err == nil {
		t.Fatal("member agents register: want RBAC denial, got nil")
	}
	for _, a := range srv.Agents() {
		if a.ID == "mallory" {
			t.Error("denied agents register must not have created the agent")
		}
	}
	// Unassigned actor: fail-closed denial on a read.
	if _, err := OrgAgentsList(srv, map[string]any{"actor_id": "ghost"}); err == nil {
		t.Error("unknown actor agents list: want denial, got nil")
	}
	// Missing actor_id: validation error.
	if _, err := OrgAgentsList(srv, map[string]any{}); err == nil {
		t.Error("missing actor_id: want error, got nil")
	}
}
