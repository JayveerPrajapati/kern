package setup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMergeJSONAddsKern(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "opencode.json")
	if err := mergeJSON(path, "mcp", map[string]any{"type": "local", "command": []string{"/x/kern-mcp"}, "enabled": true}); err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	b, _ := os.ReadFile(path)
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	mcp, _ := m["mcp"].(map[string]any)
	if _, ok := mcp["kern"]; !ok {
		t.Fatal("kern entry missing after merge")
	}
}

func TestMergeJSONIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "opencode.json")
	entry := map[string]any{"type": "local", "command": []string{"/x/kern-mcp"}, "enabled": true}
	if err := mergeJSON(path, "mcp", entry); err != nil {
		t.Fatal(err)
	}
	// Second merge must not duplicate or replace the entry.
	if err := mergeJSON(path, "mcp", entry); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if strings.Count(string(b), "/x/kern-mcp") != 1 {
		t.Fatalf("entry duplicated: %s", b)
	}
}

func TestMergeJSONPreservesOtherKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "opencode.jsonc")
	if err := os.WriteFile(path, []byte(`{"$schema":"https://opencode.ai/config.json","model":"fast"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := mergeJSON(path, "mcp", map[string]any{"type": "local"}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	for _, want := range []string{`"$schema"`, `"model": "fast"`, `"kern"`} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("merge dropped %s: %s", want, b)
		}
	}
}

func TestMergeJSONInvalid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	os.WriteFile(path, []byte("not json {"), 0o644)
	if err := mergeJSON(path, "mcp", map[string]any{}); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestWireCreatesProjectFiles(t *testing.T) {
	dir := t.TempDir()
	sts := Wire(dir, []string{"mcp", "opencode"})
	if !allInstalled(sts, "mcp") {
		t.Fatalf("mcp not installed: %+v", sts)
	}
	for _, f := range []string{".mcp.json", "opencode.json", ".opencode/plugins/kern.ts", "AGENTS.md"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Fatalf("missing %s: %v", f, err)
		}
	}
}

func TestWireIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	Wire(dir, []string{"mcp", "opencode"})
	Wire(dir, []string{"mcp", "opencode"})
	b, _ := os.ReadFile(filepath.Join(dir, ".mcp.json"))
	if strings.Count(string(b), `"kern":`) != 1 {
		t.Fatalf("duplicated kern entries: %s", b)
	}
	if strings.Count(string(b), "kern-mcp") != 1 {
		t.Fatalf("duplicated kern-mcp command: %s", b)
	}
	b2, _ := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	if strings.Count(string(b2), "kern usage rules") != 1 {
		t.Fatalf("duplicated AGENTS rules: %s", b2)
	}
}

func TestCheckReports(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	sts := Check(dir)
	if len(sts) == 0 {
		t.Fatal("Check returned nothing")
	}
	for _, s := range sts {
		if s.Installed {
			t.Fatalf("fresh dir should not report installed: %+v", s)
		}
	}
}

func allInstalled(sts []Status, agent string) bool {
	for _, s := range sts {
		if s.Agent == agent && !s.Installed {
			return false
		}
	}
	return true
}
