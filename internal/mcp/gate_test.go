package mcp

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// renameHubProject writes a Go project whose Hub function has 11 callers
// (HIGH pre-edit verdict) and returns the root.
func renameHubProject(t *testing.T) string {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module demo\n\ngo 1.22\n")
	write("hub.go", "package p\n\nfunc Hub() int { return 0 }\n")
	for i := 0; i < 11; i++ {
		write(fmt.Sprintf("c%d.go", i), fmt.Sprintf("package p\n\nfunc C%d() int { return Hub() }\n", i))
	}
	return root
}

// TestHandleRenameApplyRefusesHigh pins the P2 rename gate on the MCP path:
// applying a HIGH-verdict rename without force is refused.
func TestHandleRenameApplyRefusesHigh(t *testing.T) {
	root := renameHubProject(t)
	s := NewServer(strings.NewReader(""), io.Discard)
	defer s.Close()

	_, err := s.handleRename(context.Background(), map[string]any{
		"root": root, "symbol": "Hub", "new_name": "Hub2", "apply": "true",
	})
	if err == nil || !strings.Contains(err.Error(), "force") {
		t.Fatalf("expected HIGH refusal naming force, got err=%v", err)
	}
	if body, _ := os.ReadFile(filepath.Join(root, "hub.go")); strings.Contains(string(body), "Hub2") {
		t.Fatalf("refused rename must not touch the tree, got:\n%s", body)
	}
}

// TestHandleRenameApplyForceOverrides pins the override: force=true applies
// the HIGH-verdict rename.
func TestHandleRenameApplyForceOverrides(t *testing.T) {
	root := renameHubProject(t)
	s := NewServer(strings.NewReader(""), io.Discard)
	defer s.Close()

	if _, err := s.handleRename(context.Background(), map[string]any{
		"root": root, "symbol": "Hub", "new_name": "Hub2", "apply": "true", "force": "true",
	}); err != nil {
		t.Fatalf("forced apply must proceed, got err=%v", err)
	}
	body, _ := os.ReadFile(filepath.Join(root, "hub.go"))
	if !strings.Contains(string(body), "Hub2") {
		t.Fatalf("forced rename must rewrite the definition, got:\n%s", body)
	}
}

// TestHandleRenamePreviewUnaffected pins gate transparency: previews never
// gate, even on HIGH symbols.
func TestHandleRenamePreviewUnaffected(t *testing.T) {
	root := renameHubProject(t)
	s := NewServer(strings.NewReader(""), io.Discard)
	defer s.Close()

	out, err := s.handleRename(context.Background(), map[string]any{
		"root": root, "symbol": "Hub", "new_name": "Hub2",
	})
	if err != nil {
		t.Fatalf("preview must not gate, got err=%v", err)
	}
	if out == "" {
		t.Fatal("expected non-empty rename preview")
	}
}
