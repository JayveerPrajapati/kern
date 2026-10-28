package mcp

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/config"
)

// TestDefaultWorkspaceRootsHonorsKERNMCPROOTS pins F2: KERN_MCP_ROOTS is the
// documented confinement variable (gate.go) and must be honored as the tool
// workspace roots too. Before the fix, a root configured through
// KERN_MCP_ROOTS was confined to but never served — tool calls with no root
// fell back to the process cwd and could silently build an empty index.
func TestDefaultWorkspaceRootsHonorsKERNMCPROOTS(t *testing.T) {
	root := t.TempDir()
	t.Setenv("KERN_MCP_ROOTS", root)
	t.Setenv("KERN_ROOTS", "")
	roots := defaultWorkspaceRoots()
	if len(roots) != 1 {
		t.Fatalf("KERN_MCP_ROOTS should yield exactly one root, got %v", roots)
	}
	want, _ := filepath.Abs(root)
	if filepath.Clean(roots[0]) != filepath.Clean(want) {
		t.Fatalf("root = %q, want %q", roots[0], want)
	}
}

// TestDefaultWorkspaceRootsMergesBothSpellings pins the F2 alias contract:
// KERN_MCP_ROOTS and KERN_ROOTS are accepted as aliases and merged
// (deduplicated), so a setup that sets either spelling gets served.
func TestDefaultWorkspaceRootsMergesBothSpellings(t *testing.T) {
	a := t.TempDir()
	b := t.TempDir()
	t.Setenv("KERN_MCP_ROOTS", a)
	t.Setenv("KERN_ROOTS", a+","+b)
	roots := defaultWorkspaceRoots()
	if len(roots) != 2 {
		t.Fatalf("both spellings should yield exactly two roots (deduped), got %v", roots)
	}
	seen := map[string]bool{}
	for _, r := range roots {
		seen[filepath.Clean(r)] = true
	}
	wa, _ := filepath.Abs(a)
	wb, _ := filepath.Abs(b)
	if !seen[filepath.Clean(wa)] || !seen[filepath.Clean(wb)] {
		t.Fatalf("missing root from merged set %v (want %q and %q)", roots, wa, wb)
	}
}

// TestDefaultWorkspaceRootsFallsBackToCwd pins the unconfigured fallback:
// with neither env set and no mcp.roots config, the workspace is the startup
// directory.
func TestDefaultWorkspaceRootsFallsBackToCwd(t *testing.T) {
	t.Setenv("KERN_MCP_ROOTS", "")
	t.Setenv("KERN_ROOTS", "")
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	roots := defaultWorkspaceRoots()
	if len(roots) == 0 {
		t.Fatal("no roots with unset env")
	}
	want, _ := filepath.Abs(cwd)
	if filepath.Clean(roots[0]) != filepath.Clean(want) {
		t.Fatalf("default root = %q, want cwd %q", roots[0], want)
	}
}

// TestEmptyRootSurfacesRealError pins F2 end to end: an MCP tool call whose
// root yields an empty index (zero files → content root is the SHA-256 of
// the empty string) must surface as an isError with a real message, never as
// a silent "no symbols matched" success over a "fresh" 0-symbol index.
func TestEmptyRootSurfacesRealError(t *testing.T) {
	t.Parallel()
	empty := t.TempDir() // a directory with no indexable files
	err := mcpToolError(t, "kern_search", map[string]any{"root": empty, "query": "anything"})
	if !strings.Contains(err, "empty index") {
		t.Fatalf("expected empty-index error, got %q", err)
	}
	if !strings.Contains(err, "KERN_MCP_ROOTS") {
		t.Fatalf("error should point at the root configuration, got %q", err)
	}

	// Control: the same tool on a root with source files still succeeds.
	root := mcpProject(t)
	out := mcpAssertOK(t, "kern_search", map[string]any{"root": root, "query": "Greet"})
	if !strings.Contains(out, "Greet") {
		t.Fatalf("expected search hit on real project, got %q", out)
	}
}

// TestWorkspaceRootsForRootIgnoresEnv pins the per-App confinement rule: a
// root-bound server's workspace is the root ONLY — KERN_MCP_ROOTS cannot
// widen it (cross-App root targeting). The env keeps its semantics for the
// single-root stdio path (defaultWorkspaceRoots, pinned above).
func TestWorkspaceRootsForRootIgnoresEnv(t *testing.T) {
	a := t.TempDir()
	b := t.TempDir()
	t.Setenv("KERN_MCP_ROOTS", a+","+b)
	t.Setenv("KERN_ROOTS", "")
	roots := workspaceRootsForRoot(a)
	if len(roots) != 1 {
		t.Fatalf("workspaceRootsForRoot must confine to the App root only, got %v", roots)
	}
	wa, _ := filepath.Abs(a)
	if filepath.Clean(roots[0]) != filepath.Clean(wa) {
		t.Fatalf("root = %q, want %q", roots[0], wa)
	}
}

// TestServerForRootRefusesCrossAppRoots is the behavior-level pin: App A's
// per-App server (NewServerForRoot, the web-console tool factory) refuses
// App B's root even with KERN_MCP_ROOTS=A,B set, while the single-root stdio
// server (NewServer) still honors the env and serves B.
func TestServerForRootRefusesCrossAppRoots(t *testing.T) {
	appA := mcpProject(t)
	appB := mcpProject(t)
	t.Setenv("KERN_MCP_ROOTS", appA+","+appB)
	t.Setenv("KERN_ROOTS", "")

	sA := NewServerForRoot(strings.NewReader(""), io.Discard, appA)
	defer sA.Close()
	if _, err := sA.CallToolGoverned(context.Background(), "kern_search", map[string]any{"root": appA, "query": "Greet"}); err != nil {
		t.Fatalf("App A's own root must be allowed on its own server: %v", err)
	}
	if _, err := sA.CallToolGoverned(context.Background(), "kern_search", map[string]any{"root": appB, "query": "Greet"}); err == nil {
		t.Fatal("App A's server must refuse App B's root even with KERN_MCP_ROOTS naming both (per-App confinement)")
	}

	sStdio := NewServer(strings.NewReader(""), io.Discard)
	defer sStdio.Close()
	if _, err := sStdio.CallToolGoverned(context.Background(), "kern_search", map[string]any{"root": appB, "query": "Greet"}); err != nil {
		t.Fatalf("single-root stdio server must keep honoring KERN_MCP_ROOTS (App B served): %v", err)
	}
}

// writeProjectConfig writes a .kern/config.json fixture inside root.
func writeProjectConfig(t *testing.T, root, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, ".kern"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".kern", "config.json"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// chdir changes the process working directory for the duration of the
// closure and restores it after (the config file is discovered from cwd).
func chdir(t *testing.T, dir string) func() {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	return func() {
		if err := os.Chdir(old); err != nil {
			t.Fatal(err)
		}
	}
}

// TestConfigRootsDoNotWidenWorkspace pins F-CRIT (2026-10-26 black-box audit):
// a repo shipping mcp.roots in .kern/config.json must NOT widen (or replace)
// the server's workspace confinement unless the operator opted in with
// KERN_MCP_ALLOW_CONFIG_ROOTS=1. Before the fix the config roots REPLACED the
// jail — the server's own startup cwd was locked out and the attacker-named
// root was served instead.
func TestConfigRootsDoNotWidenWorkspace(t *testing.T) {
	attacker := t.TempDir()
	victim := t.TempDir()
	writeProjectConfig(t, attacker, `{"mcp": {"roots": ["`+victim+`"]}}`)
	t.Setenv("KERN_MCP_ROOTS", "")
	t.Setenv("KERN_ROOTS", "")
	t.Setenv("KERN_MCP_ALLOW_CONFIG_ROOTS", "")
	config.Reset()
	defer config.Reset()
	defer chdir(t, attacker)()

	roots := defaultWorkspaceRoots()
	want, _ := filepath.EvalSymlinks(attacker) // /var -> /private/var on macOS
	if len(roots) != 1 || filepath.Clean(roots[0]) != filepath.Clean(want) {
		t.Fatalf("config roots without opt-in must not widen the workspace (want startup cwd only), got %v", roots)
	}

	// The operator opt-in restores the merged behavior for operators who
	// set it explicitly.
	t.Setenv("KERN_MCP_ALLOW_CONFIG_ROOTS", "1")
	found := false
	for _, r := range defaultWorkspaceRoots() {
		if filepath.Clean(r) == filepath.Clean(victim) {
			found = true
		}
	}
	if !found {
		t.Fatal("opt-in must honor config roots")
	}
}

// TestConfigRootsWideningAttackDenied is the F-CRIT end-to-end pin: a server
// started inside the attacker repo refuses a governed tool call targeting
// the config-named victim root. Before the fix this exact shape landed a
// kern_apply write in the victim tree with isError:false.
func TestConfigRootsWideningAttackDenied(t *testing.T) {
	attacker := mcpProject(t)
	victim := t.TempDir()
	writeProjectConfig(t, attacker, `{"mcp": {"roots": ["`+victim+`"]}}`)
	t.Setenv("KERN_MCP_ROOTS", "")
	t.Setenv("KERN_ROOTS", "")
	t.Setenv("KERN_MCP_ALLOW_CONFIG_ROOTS", "")
	config.Reset()
	defer config.Reset()
	defer chdir(t, attacker)()

	s := NewServer(strings.NewReader(""), io.Discard)
	defer s.Close()
	if _, err := s.CallToolGoverned(context.Background(), "kern_search", map[string]any{"root": victim, "query": "Greet"}); err == nil {
		t.Fatal("server started in the attacker repo must refuse the config-named victim root (F-CRIT)")
	}
	// Control: the server's own root (the startup cwd) still works.
	if _, err := s.CallToolGoverned(context.Background(), "kern_search", map[string]any{"root": attacker, "query": "Greet"}); err != nil {
		t.Fatalf("the server's own startup cwd must remain served, got: %v", err)
	}
}
