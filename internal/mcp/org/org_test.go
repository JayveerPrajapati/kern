package org

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

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
	out, err := Projects(context.Background(), map[string]any{"root": root})
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
	// Unknown action errors.
	if _, err := Memory(context.Background(), map[string]any{"action": "bogus"}); err == nil {
		t.Error("unknown memory action: want error, got nil")
	}
	// Add without content errors.
	if _, err := OrgMemoryAdd(srv, map[string]any{}); err == nil {
		t.Error("add without content: want error, got nil")
	}
	// Add+list round trip.
	content := "leaf memory " + t.Name()
	add, err := OrgMemoryAdd(srv, map[string]any{"content": content})
	if err != nil {
		t.Fatalf("add memory: %v", err)
	}
	if !strings.Contains(add, content) {
		t.Errorf("add result missing content: %s", add)
	}
	list, err := OrgMemoryList(srv, map[string]any{})
	if err != nil {
		t.Fatalf("list memories: %v", err)
	}
	if !strings.Contains(list, content) {
		t.Errorf("list missing added memory: %s", list)
	}
}

func TestTasksEmptyShape(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	out, err := Tasks(context.Background(), map[string]any{"root": t.TempDir()})
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
	out, err := Search(context.Background(), map[string]any{"q": "nothing", "root": t.TempDir()})
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
	out, err := Audit(context.Background(), map[string]any{"root": t.TempDir()})
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
