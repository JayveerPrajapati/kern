package mcp

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestBlueprintFirewallToolsRegistered verifies the four blueprint change-
// firewall tools are callable through the kern server (merged catalog).
func TestBlueprintFirewallToolsRegistered(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("kern"); err != nil {
		t.Skip("kern binary not available (set PATH)")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"kern_validate_staged", "kern_explain_finding"} {
		req := toolsCallJSON(t, 88, name, map[string]any{"root": root})
		in := strings.NewReader(req + "\n")
		buf := &bytes.Buffer{}
		s := NewServer(in, buf)
		if err := s.Serve(); err != nil {
			t.Fatal(err)
		}
		out := buf.String()
		if !strings.Contains(out, `"content"`) {
			t.Fatalf("%s produced no content:\n%s", name, out)
		}
		if strings.Contains(out, "panic") {
			t.Fatalf("%s panicked:\n%s", name, out)
		}
	}
}
