package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLaunchIntegrity pins the adoption diagnosis: a bare kern-mcp in a global
// config is a silent failure in GUI-launched hosts, an absolute existing path
// is healthy, and an absolute path that no longer exists is a hard failure.
func TestLaunchIntegrity(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if level, _ := LaunchIntegrity(); level != "ok" {
		t.Fatalf("no configs: level = %q, want ok", level)
	}

	claude := filepath.Join(home, ".claude.json")
	if err := os.WriteFile(claude, []byte(`{"mcpServers":{"kern":{"command":"kern-mcp"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	level, detail := LaunchIntegrity()
	if level != "warn" || !strings.Contains(detail, "bare") {
		t.Fatalf("bare command: level=%q detail=%q, want warn mentioning bare", level, detail)
	}

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(claude, []byte(`{"mcpServers":{"kern":{"command":"`+exe+`"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if level, _ := LaunchIntegrity(); level != "ok" {
		t.Fatalf("absolute existing command: level = %q, want ok", level)
	}

	missing := filepath.Join(home, "does-not-exist", "kern-mcp")
	if err := os.WriteFile(claude, []byte(`{"mcpServers":{"kern":{"command":"`+missing+`"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if level, _ := LaunchIntegrity(); level != "fail" {
		t.Fatalf("missing absolute command: level = %q, want fail", level)
	}
}

// TestLaunchIntegrityCodexTOML covers the TOML shape: only the command inside
// [mcp_servers.kern] counts, and a later table must not leak into it.
func TestLaunchIntegrityCodexTOML(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "[mcp_servers.kern]\ncommand = \"kern-mcp\"\n\n[mcp_servers.other]\ncommand = \"/bin/other\"\n"
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	level, detail := LaunchIntegrity()
	if level != "warn" || !strings.Contains(detail, "codex") {
		t.Fatalf("codex bare: level=%q detail=%q, want warn mentioning codex", level, detail)
	}
}

// TestSteeringBriefIsShared pins the single-source steering text that every
// host's channel delivers (session hook, MCP instructions, rules).
func TestSteeringBriefIsShared(t *testing.T) {
	b := SteeringBrief("/tmp/repo")
	for _, want := range []string{"kern_explore", "kern_impact", "kern_verify", "kern_buddy", "kern_meta", "/tmp/repo"} {
		if !strings.Contains(b, want) {
			t.Fatalf("steering brief missing %q:\n%s", want, b)
		}
	}
}
