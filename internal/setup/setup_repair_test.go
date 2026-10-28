package setup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestRepairClaudeKernCommands covers the live adoption failure: older setup
// runs (and `claude mcp add` from a terminal) wrote bare "kern-mcp" into the
// machine-local ~/.claude.json — both user-global and per-project — which a
// GUI-launched Claude cannot resolve. Repair must rewrite every kern entry to
// the absolute command, drop a stale exec opt-in, and preserve other servers.
func TestRepairClaudeKernCommands(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, ".claude.json")
	body := `{
  "theme": "dark",
  "mcpServers": {
    "kern": {"command": "kern-mcp", "env": {"KERN_ALLOW_EXEC": "1"}},
    "other": {"command": "/bin/other"}
  },
  "projects": {
    "/repo/a": {"mcpServers": {"kern": {"command": "kern-mcp"}}},
    "/repo/b": {"mcpServers": {"unrelated": {"command": "/bin/x"}}}
  }
}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := repairClaudeKernCommands("/abs/bin/kern-mcp"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	servers := m["mcpServers"].(map[string]any)
	kern := servers["kern"].(map[string]any)
	if kern["command"] != "/abs/bin/kern-mcp" {
		t.Fatalf("global kern command = %v, want absolute", kern["command"])
	}
	if env, _ := kern["env"].(map[string]any); len(env) != 0 {
		t.Fatalf("stale KERN_ALLOW_EXEC survived: %v", env)
	}
	if servers["other"].(map[string]any)["command"] != "/bin/other" {
		t.Fatalf("unrelated server was modified: %v", servers["other"])
	}
	projects := m["projects"].(map[string]any)
	pa := projects["/repo/a"].(map[string]any)["mcpServers"].(map[string]any)["kern"].(map[string]any)
	if pa["command"] != "/abs/bin/kern-mcp" {
		t.Fatalf("project-scoped kern command = %v, want absolute", pa["command"])
	}
	pb := projects["/repo/b"].(map[string]any)["mcpServers"].(map[string]any)["unrelated"].(map[string]any)
	if pb["command"] != "/bin/x" {
		t.Fatalf("unrelated project entry was modified: %v", pb)
	}
	if _, err := os.Stat(path + ".bak"); err != nil {
		t.Fatalf("repair must back up the original config: %v", err)
	}
}

// TestAdapterEntriesDoNotEnableExec pins the fail-closed contract: setup must
// never write KERN_ALLOW_EXEC=1 into an agent config. Exec is operator opt-in.
func TestAdapterEntriesDoNotEnableExec(t *testing.T) {
	for name, entry := range map[string]map[string]any{
		"stdio": stdioEntry("/abs/kern-mcp"),
		"cmd":   cmdEntry("/abs/kern-mcp"),
	} {
		env, _ := entry["env"].(map[string]any)
		if _, has := env["KERN_ALLOW_EXEC"]; has {
			t.Fatalf("%s entry enables exec by default: %v", name, env)
		}
	}
}
