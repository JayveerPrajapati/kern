package setup

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- A. writeGuardScript (project-scoped guard install, 0% coverage before) ---

func TestWriteGuardScriptWritesExecutableAsset(t *testing.T) {
	root := t.TempDir()
	p, err := writeGuardScript(root)
	if err != nil {
		t.Fatalf("writeGuardScript: %v", err)
	}
	want := filepath.Join(root, ".kern", "hooks", "kern-guard.sh")
	if p != want {
		t.Fatalf("path = %q, want %q", p, want)
	}
	fi, err := os.Stat(want)
	if err != nil {
		t.Fatalf("guard script not written: %v", err)
	}
	if fi.Mode().Perm()&0o111 == 0 {
		t.Fatalf("guard script not executable (intended 0755): %v", fi.Mode())
	}
	b, err := os.ReadFile(want)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != kernGuardScript {
		t.Fatal("written script does not byte-match the embedded asset")
	}
}

func TestWriteGuardScriptIdempotent(t *testing.T) {
	root := t.TempDir()
	p1, err := writeGuardScript(root)
	if err != nil {
		t.Fatalf("first write: %v", err)
	}
	p2, err := writeGuardScript(root)
	if err != nil {
		t.Fatalf("second write: %v", err)
	}
	if p1 != p2 {
		t.Fatalf("paths differ: %q vs %q", p1, p2)
	}
	b1, err := os.ReadFile(p1)
	if err != nil {
		t.Fatal(err)
	}
	b2, err := os.ReadFile(p2)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b1, b2) {
		t.Fatal("second run produced different content")
	}
}

func TestWriteGuardScriptErrorsWhenTargetNotCreatable(t *testing.T) {
	root := t.TempDir()
	// Make <root>/.kern a FILE so MkdirAll(<root>/.kern/hooks) fails with
	// ENOTDIR — a robust way to force the error path without needing root.
	if err := os.WriteFile(filepath.Join(root, ".kern"), []byte("occupied"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := writeGuardScript(root); err == nil {
		t.Fatal("expected error when <root>/.kern/hooks cannot be created, got nil")
	}
}

// --- B. mergeJSON path safety (setup_config.go) ---

func TestMergeJSONAllowsDotDotTraversal(t *testing.T) {
	// KNOWN-GAP SENTINEL: mergeJSON calls os.MkdirAll(filepath.Dir(path))
	// with no confinement, so a path containing `..` escapes the intended
	// base directory. This test pins the CURRENT behavior (the write lands
	// outside the base dir); a future fix that rejects traversal will fail
	// it, which is the point. This is not an endorsement of the behavior.
	parent := t.TempDir()
	base := filepath.Join(parent, "proj")
	path := filepath.Join(base, "..", "escape.json")
	if err := mergeJSON(path, "mcp", map[string]any{"command": "/x/kern-mcp"}); err != nil {
		t.Fatalf("mergeJSON: %v", err)
	}
	if _, err := os.Stat(filepath.Join(parent, "escape.json")); err != nil {
		t.Fatalf("file written outside base via `..` is the current behavior; want it present: %v", err)
	}
	if _, err := os.Stat(filepath.Join(base, "escape.json")); err == nil {
		t.Fatal("file must not be inside base")
	}
}

func TestMergeJSONPreservesPermissionsAndBackupMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret.json")
	if err := os.WriteFile(path, []byte(`{"token":"abc"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := mergeJSON(path, "mcp", map[string]any{"kern": map[string]any{"command": "/x/kern-mcp"}}); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("0600 not preserved across merge, got %v", fi.Mode().Perm())
	}
	bi, err := os.Stat(path + ".bak")
	if err != nil {
		t.Fatal(err)
	}
	if bi.Mode().Perm() != 0o600 {
		t.Fatalf("backup mode not preserved, got %v", bi.Mode().Perm())
	}
}

func TestMergeJSONNewFileDefaultsTo0600(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "new.json")
	if err := mergeJSON(path, "mcp", map[string]any{"kern": map[string]any{"command": "/x/kern-mcp"}}); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("new file should default to 0600, got %v", fi.Mode().Perm())
	}
}

func TestMergeJSONNonMapKernEntryDoesNotPanic(t *testing.T) {
	// A pre-existing "kern" entry that is NOT a map (e.g. a plain string)
	// must not panic: the `, _` type assertions yield nil and the stale entry
	// is replaced with the real map.
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.json")
	if err := os.WriteFile(path, []byte(`{"mcp":{"kern":"foo"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := mergeJSON(path, "mcp", map[string]any{"command": "/x/kern-mcp"}); err != nil {
		t.Fatalf("mergeJSON must not panic on a non-map kern entry: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("written file not valid JSON: %v\n%s", err, b)
	}
	mcpM, _ := m["mcp"].(map[string]any)
	kernM, _ := mcpM["kern"].(map[string]any)
	if kernM == nil {
		t.Fatal("non-map kern entry was not repaired into a map")
	}
	if kernM["command"] != "/x/kern-mcp" {
		t.Fatalf("kern entry not repaired with the new command: %v", kernM)
	}
}

func TestMergeJSONBackupOverwritesPriorBak(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.json")
	if err := os.WriteFile(path, []byte(`{"v":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".bak", []byte("STALE"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := mergeJSON(path, "mcp", map[string]any{"kern": map[string]any{"command": "/x/kern-mcp"}}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path + ".bak")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "STALE") {
		t.Fatal("prior .bak was not overwritten")
	}
	if !strings.Contains(string(b), `{"v":1}`) {
		t.Fatalf("backup must be the raw pre-merge content, got %q", b)
	}
}

// --- C. wireCodex TOML injection (setup_codex.go) ---

func TestWireCodexRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		bin  string
		want string
	}{
		{"unix-path", "/usr/local/bin/kern-mcp", `command = "/usr/local/bin/kern-mcp"`},
		// Backslashes must be doubled for TOML (Windows-style bin path).
		{"backslash-path", `C:\kern\bin\kern-mcp.exe`, `command = "C:\\kern\\bin\\kern-mcp.exe"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("HOME", dir)
			st := wireCodex(tc.bin)
			if !st.Installed {
				t.Fatalf("wireCodex failed: %s", st.Note)
			}
			b, err := os.ReadFile(filepath.Join(dir, ".codex", "config.toml"))
			if err != nil {
				t.Fatal(err)
			}
			content := string(b)
			if !strings.Contains(content, "[mcp_servers.kern]") {
				t.Fatalf("kern MCP section missing:\n%s", content)
			}
			if !strings.Contains(content, tc.want) {
				t.Fatalf("command entry missing %q:\n%s", tc.want, content)
			}
		})
	}
}

func TestWireCodexQuoteInjection(t *testing.T) {
	// KNOWN-GAP SENTINEL: wireCodex escapes backslashes but NOT double
	// quotes, so a bin path containing `"` is written verbatim and produces
	// malformed TOML (command = "/x/kern"evil/mcp"). This pins the CURRENT
	// behavior; a future fix that escapes quotes will fail this test, which
	// is the point. This is not an endorsement of the behavior.
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	bin := `/x/kern"evil/mcp`
	st := wireCodex(bin)
	if !st.Installed {
		t.Fatalf("wireCodex failed: %s", st.Note)
	}
	b, err := os.ReadFile(filepath.Join(dir, ".codex", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(b)
	if !strings.Contains(content, bin) {
		t.Fatalf("bin with quote not written verbatim (current behavior):\n%s", content)
	}
	if strings.Contains(content, `\"`) {
		t.Fatal("quotes are currently NOT escaped — saw \\\" unexpectedly")
	}
}

func TestWireCodexAlreadyRegisteredSkipsRewrite(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	path := filepath.Join(dir, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("[mcp_servers.kern]\ncommand = \"/custom/bin/kern-mcp\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	st := wireCodex("/ignored/bin")
	if !st.Installed {
		t.Fatalf("already-registered codex should report installed: %s", st.Note)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// The pre-existing entry must be left untouched (no append, no rewrite).
	if strings.Contains(string(b), "/ignored/bin") {
		t.Fatalf("already-registered config must not be rewritten:\n%s", b)
	}
	if strings.Count(string(b), "[mcp_servers.kern]") != 1 {
		t.Fatalf("kern MCP section duplicated:\n%s", b)
	}
}

// --- D. globalConfig fallback (setup_config.go) ---

func TestGlobalConfigFallsBackToDotWhenHomeMissing(t *testing.T) {
	// os.UserHomeDir() errors when $HOME is empty; globalConfig falls back to
	// home="." so it returns .config/<sub> instead of panicking or returning
	// an empty path.
	t.Setenv("HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	got := globalConfig("kern", "config.json")("")
	want := filepath.Join(".config", "kern", "config.json")
	if got != want {
		t.Fatalf("globalConfig fallback: got %q, want %q", got, want)
	}
}

// --- E. Wire with unknown/invalid/duplicate agent IDs (setup.go) ---

func TestWireUnknownAgentSkipsSilently(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	sts := Wire(dir, []string{"nonexistent"}, false)
	for _, s := range sts {
		if s.Agent == "nonexistent" {
			t.Fatal("unknown agent must not produce a status entry")
		}
	}
	// Universal files are still written regardless of the agent list.
	for _, f := range []string{".mcp.json", "AGENTS.md"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Fatalf("missing %s: %v", f, err)
		}
	}
}

func TestWireEmptyAgentIDNoPanic(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	sts := Wire(dir, []string{""}, false)
	if len(sts) == 0 {
		t.Fatal("expected statuses for the universal files")
	}
}

func TestWireDuplicateAgentIdempotent(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	sts := Wire(dir, []string{"opencode", "opencode"}, false)
	if !allInstalled(sts, "opencode") {
		t.Fatalf("opencode not installed: %+v", sts)
	}
	b, err := os.ReadFile(filepath.Join(dir, "opencode.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(b), `"kern"`) != 1 {
		t.Fatalf("duplicate agent id double-wrote the kern entry: %s", b)
	}
	if _, err := os.Stat(filepath.Join(dir, ".opencode", "plugins", "kern.ts")); err != nil {
		t.Fatalf("opencode plugin missing: %v", err)
	}
}
