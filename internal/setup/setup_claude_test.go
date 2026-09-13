package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFakeClaude installs a fake `claude` CLI on PATH that mimics the real
// `claude mcp add` behavior against a temp HOME: the first `mcp add` registers
// kern in ~/.claude.json, and any later `mcp add` while kern is present fails
// with "MCP server kern already exists in local config" — exactly the failure
// F-001 reproduced on a second `kern setup` run. Every `mcp add` invocation is
// appended to a counter file so tests can assert the add ran exactly (or never)
// as expected. Returns the counter file path.
func writeFakeClaude(t *testing.T, home string) string {
	t.Helper()
	binDir := t.TempDir()
	script := `#!/bin/sh
cfg="$HOME/.claude.json"
count="$HOME/.fake-claude-add-count"
if [ "$1" = "mcp" ] && [ "$2" = "add" ]; then
  printf 'x\n' >> "$count"
  if grep -q '"kern"' "$cfg" 2>/dev/null; then
    echo "MCP server kern already exists in local config" >&2
    exit 1
  fi
  mkdir -p "$(dirname "$cfg")"
  printf '{\n  "mcpServers": {\n    "kern": {\n      "type": "stdio",\n      "command": "kern-mcp"\n    }\n  }\n}\n' > "$cfg"
  echo "Added kern MCP server"
  exit 0
fi
exit 0
`
	if err := os.WriteFile(filepath.Join(binDir, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return filepath.Join(home, ".fake-claude-add-count")
}

func fakeClaudeAddCount(t *testing.T, countPath string) int {
	t.Helper()
	b, err := os.ReadFile(countPath)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(b), "\n")
}

// TestWireClaudeIdempotent verifies F-001: wiring claude twice against the same
// temp HOME must not error on the second run. The first run registers kern via
// `claude mcp add`; the second must detect the existing registration in the
// local claude config and skip the add (reporting "already registered") instead
// of letting `claude mcp add` fail and exit 1.
func TestWireClaudeIdempotent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	countPath := writeFakeClaude(t, home)

	first := wireClaude("kern-mcp")
	if !first.Installed {
		t.Fatalf("first wireClaude failed: %+v", first)
	}

	second := wireClaude("kern-mcp")
	if !second.Installed {
		t.Fatalf("second wireClaude must not error when kern is already registered, got: %+v", second)
	}
	if strings.Contains(second.Note, "failed") {
		t.Fatalf("second wireClaude reported a failure: %+v", second)
	}
	if !strings.Contains(second.Note, "already registered") {
		t.Fatalf("second wireClaude should report 'already registered', got: %+v", second)
	}

	// The fake `claude mcp add` must have run exactly once: the second run
	// skipped it via the local-config check instead of invoking it.
	if got := fakeClaudeAddCount(t, countPath); got != 1 {
		t.Fatalf("claude mcp add invoked %d times, want 1 (second run must skip)", got)
	}

	// The local config must contain the kern registration.
	b, err := os.ReadFile(filepath.Join(home, ".claude.json"))
	if err != nil {
		t.Fatalf("claude config not written: %v", err)
	}
	if !strings.Contains(string(b), `"kern"`) {
		t.Fatalf("claude config missing kern entry:\n%s", b)
	}
}

// TestWireClaudeSkipsWhenAlreadyRegistered verifies the skip path directly:
// when the local claude config already registers kern, wireClaude must not
// invoke `claude mcp add` at all (the fake would fail with "already exists").
func TestWireClaudeSkipsWhenAlreadyRegistered(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	countPath := writeFakeClaude(t, home)

	// Pre-seed the local claude config with kern registered.
	cfg := filepath.Join(home, ".claude.json")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, []byte(`{"mcpServers":{"kern":{"type":"stdio","command":"kern-mcp"}}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	st := wireClaude("kern-mcp")
	if !st.Installed {
		t.Fatalf("wireClaude with pre-registered kern failed: %+v", st)
	}
	if !strings.Contains(st.Note, "already registered") {
		t.Fatalf("expected 'already registered' note, got: %+v", st)
	}
	if got := fakeClaudeAddCount(t, countPath); got != 0 {
		t.Fatalf("claude mcp add invoked %d times, want 0 when kern already registered", got)
	}
}

// TestWireIdempotentWithClaude runs the full Wire flow (the path `kern setup`
// exercises) twice against a temp HOME + repo and asserts the second run
// reports no failure for claude — the F-001 exit-1 repro at the Wire level.
func TestWireIdempotentWithClaude(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeFakeClaude(t, home)
	root := t.TempDir()

	first := Wire(root, []string{"claude"}, false)
	if !allInstalled(first, "claude") {
		t.Fatalf("first Wire claude not installed: %+v", first)
	}

	second := Wire(root, []string{"claude"}, false)
	if !allInstalled(second, "claude") {
		t.Fatalf("second Wire must stay idempotent for claude, got failures: %+v", second)
	}
}
