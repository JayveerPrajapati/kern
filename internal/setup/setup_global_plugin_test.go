package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWireCodexEnablesHooksFeature verifies that wireCodex writes both the
// [mcp_servers.kern] entry and the [features] codex_hooks = true flag into
// ~/.codex/config.toml, and that a second call is idempotent (no duplicate
// [features] table, no duplicate codex_hooks line).
func TestWireCodexEnablesHooksFeature(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	first := wireCodex("kern-mcp")
	if !first.Installed {
		t.Fatalf("first wireCodex not installed: %+v", first)
	}

	b, err := os.ReadFile(first.Path)
	if err != nil {
		t.Fatalf("read config.toml: %v", err)
	}
	content := string(b)
	if !strings.Contains(content, "[mcp_servers.kern]") {
		t.Fatalf("config.toml missing [mcp_servers.kern]:\n%s", content)
	}
	if !strings.Contains(content, "codex_hooks = true") {
		t.Fatalf("config.toml missing codex_hooks = true:\n%s", content)
	}
	if !strings.Contains(content, "[features]") {
		t.Fatalf("config.toml missing [features] table:\n%s", content)
	}

	second := wireCodex("kern-mcp")
	if !second.Installed {
		t.Fatalf("second wireCodex not installed: %+v", second)
	}
	b, err = os.ReadFile(second.Path)
	if err != nil {
		t.Fatalf("re-read config.toml: %v", err)
	}
	content = string(b)
	if got := strings.Count(content, "[features]"); got != 1 {
		t.Fatalf("duplicate [features] table after re-run (%d):\n%s", got, content)
	}
	if got := strings.Count(content, "codex_hooks = true"); got != 1 {
		t.Fatalf("duplicate codex_hooks line after re-run (%d):\n%s", got, content)
	}
	if got := strings.Count(content, "[mcp_servers.kern]"); got != 1 {
		t.Fatalf("duplicate [mcp_servers.kern] after re-run (%d):\n%s", got, content)
	}
}

// TestWireCodexInsertsIntoExistingFeatures verifies that when config.toml
// already has a [features] table (without codex_hooks), wireCodex inserts the
// flag right after the [features] line instead of appending a duplicate table.
func TestWireCodexInsertsIntoExistingFeatures(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, ".codex", "config.toml")
	existing := "model = \"gpt-5\"\n[features]\noutput_style = \"full\"\n"
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}

	st := wireCodex("kern-mcp")
	if !st.Installed {
		t.Fatalf("wireCodex not installed: %+v", st)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(b)
	if got := strings.Count(content, "[features]"); got != 1 {
		t.Fatalf("duplicate [features] table (%d):\n%s", got, content)
	}
	if got := strings.Count(content, "codex_hooks = true"); got != 1 {
		t.Fatalf("codex_hooks line missing/duplicated (%d):\n%s", got, content)
	}
	lines := strings.Split(content, "\n")
	found := false
	for i, line := range lines {
		if strings.TrimSpace(line) == "[features]" {
			if i+1 >= len(lines) || !strings.Contains(lines[i+1], "codex_hooks = true") {
				t.Fatalf("codex_hooks not inserted right after [features] line:\n%s", content)
			}
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("[features] line not found:\n%s", content)
	}
	// Existing content preserved.
	if !strings.Contains(content, "output_style = \"full\"") {
		t.Fatalf("existing [features] content lost:\n%s", content)
	}
}

// TestWireGlobalPlugin verifies that wireGlobalPlugin installs the embedded
// plugin into both global plugin paths, that a re-run reports "current"
// without rewriting, and that a user-customized copy is left untouched.
func TestWireGlobalPlugin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	asset, err := PluginAsset()
	if err != nil {
		t.Fatalf("PluginAsset: %v", err)
	}

	first := wireGlobalPlugin()
	if !first.Installed {
		t.Fatalf("first wireGlobalPlugin not installed: %+v", first)
	}
	paths := GlobalPluginPaths()
	if len(paths) != 2 {
		t.Fatalf("expected 2 global plugin paths, got %d: %v", len(paths), paths)
	}
	for _, p := range paths {
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			t.Fatalf("read %s: %v", p, rerr)
		}
		if string(b) != string(asset) {
			t.Fatalf("%s differs from embedded asset (len %d vs %d)", p, len(b), len(asset))
		}
	}

	second := wireGlobalPlugin()
	if !second.Installed {
		t.Fatalf("second wireGlobalPlugin not installed: %+v", second)
	}
	if !strings.Contains(second.Note, "current") {
		t.Fatalf("expected re-run to report current, got: %s", second.Note)
	}

	// A user-modified copy must be left untouched.
	custom := []byte("// user edit\n")
	modPath := paths[0]
	if err := os.WriteFile(modPath, custom, 0o644); err != nil {
		t.Fatal(err)
	}
	third := wireGlobalPlugin()
	if !strings.Contains(third.Note, "customized") {
		t.Fatalf("expected customized notice, got: %s", third.Note)
	}
	b, rerr := os.ReadFile(modPath)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if string(b) != string(custom) {
		t.Fatalf("customized plugin was overwritten: got %q, want %q", b, custom)
	}
}

// TestCheckReportsGlobalPluginPaths verifies that Check reports a status entry
// for every global opencode plugin path.
func TestCheckReportsGlobalPluginPaths(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()

	statuses := Check(root)
	globals := GlobalPluginPaths()
	count := 0
	for _, s := range statuses {
		if strings.Contains(s.Agent, "opencode plugin (global)") {
			count++
			if !containsPath(globals, s.Path) {
				t.Fatalf("status path %q not in GlobalPluginPaths %v", s.Path, globals)
			}
		}
	}
	if count != len(globals) {
		t.Fatalf("expected %d 'opencode plugin (global)' statuses, got %d", len(globals), count)
	}
}

func containsPath(paths []string, p string) bool {
	for _, q := range paths {
		if q == p {
			return true
		}
	}
	return false
}
