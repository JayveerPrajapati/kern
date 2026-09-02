package setup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeAgentsFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCustomAdapterWire(t *testing.T) {
	root := t.TempDir()
	writeAgentsFile(t, filepath.Join(root, ".kern", "agents.json"), `[
		{
			"name": "myagent",
			"path": "agentcfg/config.json",
			"key": "mcpServers"
		}
	]`)
	statuses := Wire(root, nil, false)
	var found bool
	for _, s := range statuses {
		if s.Agent == "myagent" {
			found = true
			if !s.Installed {
				t.Errorf("myagent status: %v", s.Note)
			}
		}
	}
	if !found {
		t.Fatalf("custom adapter missing from Wire statuses: %+v", statuses)
	}
	// Relative path resolved against root; kern entry merged under key.
	data, err := os.ReadFile(filepath.Join(root, "agentcfg", "config.json"))
	if err != nil {
		t.Fatalf("custom config not written: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("custom config invalid: %v", err)
	}
	servers, _ := m["mcpServers"].(map[string]any)
	if servers == nil || servers["kern"] == nil {
		t.Errorf("kern entry missing from custom config: %s", data)
	}
}

func TestCustomAdapterOverridesBuiltin(t *testing.T) {
	root := t.TempDir()
	writeAgentsFile(t, filepath.Join(root, ".kern", "agents.json"), `[
		{"name": "zed", "path": "zed-override/settings.json", "key": "context_servers", "entry": "cmd"}
	]`)
	all, errs := effectiveAdapters(root)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(all) != len(adapters) {
		t.Fatalf("override should replace, not append: got %d adapters, want %d", len(all), len(adapters))
	}
	var a adapter
	for _, cand := range all {
		if cand.name == "zed" {
			a = cand
		}
	}
	if got := a.path(root); got != filepath.Join(root, "zed-override", "settings.json") {
		t.Errorf("override path = %q", got)
	}
	if a.key != "context_servers" || a.scope != "global" {
		t.Errorf("override fields: key=%q scope=%q", a.key, a.scope)
	}
	if e := a.entry("bin"); e["type"] != nil {
		t.Errorf("cmd entry should not carry a type field: %v", e)
	}
}

func TestCustomAdapterMergeOrder(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	writeAgentsFile(t, filepath.Join(home, ".config", "kern", "agents.json"), `[
		{"name": "shared", "path": "user/path.json", "key": "mcpServers"},
		{"name": "useronly", "path": "user/only.json", "key": "mcpServers"}
	]`)
	writeAgentsFile(t, filepath.Join(root, ".kern", "agents.json"), `[
		{"name": "shared", "path": "project/path.json", "key": "servers"}
	]`)
	all, errs := effectiveAdapters(root)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	var shared, useronly adapter
	counts := map[string]int{}
	for _, a := range all {
		counts[a.name]++
		if a.name == "shared" {
			shared = a
		}
		if a.name == "useronly" {
			useronly = a
		}
	}
	if counts["shared"] != 1 {
		t.Errorf("clash should replace, got %d shared entries", counts["shared"])
	}
	if shared.path(root) != filepath.Join(root, "project", "path.json") {
		t.Errorf("project file should win the clash, got %q", shared.path(root))
	}
	if useronly.path(root) == "" {
		t.Errorf("user-only adapter missing")
	}
}

func TestCustomAdapterValidationErrors(t *testing.T) {
	root := t.TempDir()
	writeAgentsFile(t, filepath.Join(root, ".kern", "agents.json"), `[
		{"name": "", "path": "x.json", "key": "mcpServers"},
		{"name": "ok", "path": "ok.json", "key": "mcpServers", "entry": "bogus"},
		{"name": "badscope", "path": "y.json", "key": "mcpServers", "scope": "moon"},
		{"name": "nokey", "path": "z.json"}
	]`)
	_, errs := effectiveAdapters(root)
	if len(errs) != 4 {
		t.Fatalf("want 4 validation errors, got %d: %v", len(errs), errs)
	}
	joined := ""
	for _, e := range errs {
		joined += e.Error() + "\n"
	}
	for _, want := range []string{"name is required", `entry must be "stdio" or "cmd"`, `scope must be "global" or "repo"`, "key is required"} {
		if !strings.Contains(joined, want) {
			t.Errorf("errors missing %q in:\n%s", want, joined)
		}
	}

	// Unknown fields are rejected.
	writeAgentsFile(t, filepath.Join(root, ".kern", "agents.json"), `[{"name": "x", "path": "x.json", "key": "k", "surprise": 1}]`)
	_, errs = effectiveAdapters(root)
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "surprise") {
		t.Errorf("unknown field should be rejected, got %v", errs)
	}

	// Invalid JSON is rejected.
	writeAgentsFile(t, filepath.Join(root, ".kern", "agents.json"), `{not json`)
	_, errs = effectiveAdapters(root)
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "not a valid agents file") {
		t.Errorf("malformed file should error, got %v", errs)
	}
}

func TestCustomAdapterErrorsDoNotAbortWire(t *testing.T) {
	root := t.TempDir()
	writeAgentsFile(t, filepath.Join(root, ".kern", "agents.json"), `[{"name": "broken"}]`)
	statuses := Wire(root, nil, false)
	var sawErr, sawMCP bool
	for _, s := range statuses {
		if s.Agent == "custom adapters" && strings.Contains(s.Note, "path is required") {
			sawErr = true
		}
		if strings.Contains(s.Agent, "mcp") {
			sawMCP = true
		}
	}
	if !sawErr {
		t.Errorf("validation error not surfaced in statuses")
	}
	if !sawMCP {
		t.Errorf("malformed custom file aborted the rest of setup")
	}
}

func TestCustomAdapterCheck(t *testing.T) {
	root := t.TempDir()
	writeAgentsFile(t, filepath.Join(root, ".kern", "agents.json"), `[{"name": "myagent", "path": "cfg.json", "key": "mcpServers"}]`)
	statuses := Check(root)
	var found bool
	for _, s := range statuses {
		if s.Agent == "myagent" {
			found = true
			if s.Path != filepath.Join(root, "cfg.json") {
				t.Errorf("check path = %q", s.Path)
			}
		}
	}
	if !found {
		t.Errorf("custom adapter missing from Check statuses")
	}
}

func TestCustomAdapterPathExpansion(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("KERN_TEST_DIR", "expanded")
	cases := []struct {
		in   string
		want string
	}{
		{"~/.myagent/cfg.json", filepath.Join(home, ".myagent", "cfg.json")},
		{"~/x/$KERN_TEST_DIR/cfg.json", filepath.Join(home, "x", "expanded", "cfg.json")},
		{"${KERN_TEST_DIR}/cfg.json", filepath.Join(root, "expanded", "cfg.json")},
		{"relative/cfg.json", filepath.Join(root, "relative", "cfg.json")},
	}
	for _, c := range cases {
		if got := expandCustomPath(c.in, root); got != c.want {
			t.Errorf("expandCustomPath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
