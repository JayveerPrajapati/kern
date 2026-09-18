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

// TestGateErrorDoesNotDiscloseRoots locks audit A6: a denied path's error
// must not disclose the server's allowed roots — they are server
// configuration a client must not learn from a denial. The denial names only
// the denied key with generic guidance.
func TestGateErrorDoesNotDiscloseRoots(t *testing.T) {
	secretRoot := t.TempDir()
	g := &Gate{roots: []string{secretRoot}, enabled: true}
	err := g.gatePath("root", "/tmp/kern-outside-dir")
	if err == nil {
		t.Fatal("path outside the allowed roots must be denied")
	}
	if strings.Contains(err.Error(), secretRoot) {
		t.Fatalf("denial must not disclose allowed roots, got: %v", err)
	}
	if !strings.Contains(err.Error(), "outside allowed roots") {
		t.Fatalf("denial should carry generic guidance, got: %v", err)
	}
	if !strings.Contains(err.Error(), `"root"`) {
		t.Fatalf("denial should name the denied key, got: %v", err)
	}
}

// TestGateCheckConfinesRepoArg locks R3: the raw `repo` argument (the root
// name blueprint tools use) must be confined exactly like root/dir — a client
// passing `repo` directly must not bypass raw-arg confinement.
func TestGateCheckConfinesRepoArg(t *testing.T) {
	ws := t.TempDir()
	sub := filepath.Join(ws, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	g := &Gate{roots: []string{ws}, enabled: true}
	if err := g.Check("kern_blueprint_apply", map[string]any{"repo": sub}); err != nil {
		t.Fatalf("repo arg inside the root must be allowed: %v", err)
	}
	outside := t.TempDir()
	err := g.Check("kern_blueprint_apply", map[string]any{"repo": outside})
	if err == nil {
		t.Fatal("repo arg outside the root must be denied")
	}
	if !strings.Contains(err.Error(), "outside allowed roots") {
		t.Fatalf("expected a confinement denial, got %v", err)
	}
}
