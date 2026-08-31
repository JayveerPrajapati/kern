package mcp

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewGateFromEnv(t *testing.T) {
	t.Run("unset_or_empty_fails_closed_to_cwd", func(t *testing.T) {
		cwd, _ := os.Getwd()
		for _, v := range []string{"", "   ", " , , "} {
			t.Setenv("KERN_MCP_ROOTS", v)
			g := NewGateFromEnv()
			if !g.enabled {
				t.Fatalf("KERN_MCP_ROOTS=%q must yield an enabled (fail-closed) gate", v)
			}
			if len(g.roots) != 1 || g.roots[0] != symlinkOrSelf(cwd) {
				t.Fatalf("KERN_MCP_ROOTS=%q must default to the cwd, got %v", v, g.roots)
			}
			if err := g.Check("kern_project_map", map[string]any{"root": "/etc"}); err == nil {
				t.Fatalf("fail-closed gate must deny paths outside the cwd (env %q)", v)
			}
		}
	})

	t.Run("permissive_mode_disables_gate", func(t *testing.T) {
		t.Setenv("KERN_MCP_PERMISSIVE", "1")
		t.Setenv("KERN_MCP_ROOTS", "")
		g := NewGateFromEnv()
		if g.enabled {
			t.Fatal("KERN_MCP_PERMISSIVE=1 must disable the gate (explicit opt-out)")
		}
		if err := g.Check("kern_project_map", map[string]any{"root": "/etc"}); err != nil {
			t.Fatalf("permissive mode must allow every call: %v", err)
		}
	})

	t.Run("parses_multi_root_and_cleans", func(t *testing.T) {
		t.Setenv("KERN_MCP_ROOTS", " /tmp/foo/ , /tmp/bar ,,")
		g := NewGateFromEnv()
		if !g.enabled {
			t.Fatal("expected an enabled gate")
		}
		if len(g.roots) != 2 {
			t.Fatalf("expected 2 roots, got %d: %v", len(g.roots), g.roots)
		}
		if g.roots[0] != "/tmp/foo" {
			t.Fatalf("root 0 = %q, want cleaned /tmp/foo", g.roots[0])
		}
		if g.roots[1] != "/tmp/bar" {
			t.Fatalf("root 1 = %q, want /tmp/bar", g.roots[1])
		}
	})

	t.Run("relative_root_resolved_against_cwd", func(t *testing.T) {
		cwd, _ := os.Getwd()
		t.Setenv("KERN_MCP_ROOTS", "relative-root-dir")
		g := NewGateFromEnv()
		want := filepath.Clean(filepath.Join(cwd, "relative-root-dir"))
		if len(g.roots) != 1 || g.roots[0] != want {
			t.Fatalf("roots = %v, want [%s]", g.roots, want)
		}
	})
}

func TestGateCheckContainment(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	inside := filepath.Join(root, "sub")
	sibling := filepath.Join(base, "rootx") // shares the "root" prefix but is NOT inside root
	for _, d := range []string{root, inside, sibling} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	g := &Gate{roots: []string{root}, enabled: true}

	if err := g.Check("kern_project_map", map[string]any{"root": inside}); err != nil {
		t.Fatalf("path inside root rejected: %v", err)
	}
	if err := g.Check("kern_project_map", map[string]any{"root": root}); err != nil {
		t.Fatalf("root itself rejected: %v", err)
	}
	err := g.Check("kern_project_map", map[string]any{"root": sibling})
	if err == nil {
		t.Fatal("sibling path with a shared prefix was allowed")
	}
	if !strings.Contains(err.Error(), `root=`) || !strings.Contains(err.Error(), "outside allowed roots") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGateCheckSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "sub"), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	g := &Gate{roots: []string{root}, enabled: true}

	// A symlink inside the root that points outside is judged by its real
	// location and denied.
	err := g.Check("kern_search", map[string]any{"path": filepath.Join(root, "escape")})
	if err == nil {
		t.Fatal("symlink escaping the root was allowed")
	}
	if !strings.Contains(err.Error(), "outside allowed roots") {
		t.Fatalf("unexpected error: %v", err)
	}
	// A symlink inside the root that stays inside is allowed.
	if err := g.Check("kern_search", map[string]any{"path": filepath.Join(root, "link")}); err != nil {
		t.Fatalf("symlink staying inside the root rejected: %v", err)
	}
	// An unresolvable path is treated as a violation (not trustworthy).
	if err := g.Check("kern_search", map[string]any{"path": filepath.Join(root, "nope")}); err == nil {
		t.Fatal("unresolvable path was allowed")
	}
}

func TestGateCheckRelativePath(t *testing.T) {
	cwd, _ := os.Getwd()
	other := t.TempDir()

	// A relative path is resolved against cwd; server.go exists in this
	// package's cwd during tests.
	g := &Gate{roots: []string{cwd}, enabled: true}
	if err := g.Check("kern_compact_file", map[string]any{"path": "server.go"}); err != nil {
		t.Fatalf("cwd-relative path inside root rejected: %v", err)
	}

	// With an unrelated root, the same cwd-relative path resolves outside.
	g2 := &Gate{roots: []string{other}, enabled: true}
	if err := g2.Check("kern_compact_file", map[string]any{"path": "server.go"}); err == nil {
		t.Fatal("cwd-relative path allowed under an unrelated root")
	}
}

func TestGateCheckNestedArgs(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	sub := filepath.Join(root, "sub")
	outside := filepath.Join(base, "outside")
	for _, d := range []string{root, sub, outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	g := &Gate{roots: []string{root}, enabled: true}

	// All-inside nested args pass.
	args := map[string]any{
		"root":   root,
		"files":  []any{map[string]any{"path": root}, map[string]any{"path": sub}},
		"nested": map[string]any{"dir": root},
	}
	if err := g.Check("kern_modernize", args); err != nil {
		t.Fatalf("all-inside nested args rejected: %v", err)
	}

	// One outside nested path denies the whole call.
	bad := map[string]any{
		"root":  root,
		"files": []any{map[string]any{"path": outside}},
	}
	err := g.Check("kern_modernize", bad)
	if err == nil {
		t.Fatal("nested outside path was allowed")
	}
	if !strings.Contains(err.Error(), `path=`) || !strings.Contains(err.Error(), "outside allowed roots") {
		t.Fatalf("unexpected nested error: %v", err)
	}

	// Deeper nesting (array inside array) is covered too, and the key match is
	// case-insensitive ("targetPath" contains "path").
	deep := map[string]any{
		"payload": []any{[]any{map[string]any{"targetPath": outside}}},
	}
	if err := g.Check("kern_modernize", deep); err == nil {
		t.Fatal("deeply nested outside path was allowed")
	}
}

func TestGateCheckNonStringAndEmptyIgnored(t *testing.T) {
	g := &Gate{roots: []string{t.TempDir()}, enabled: true}
	args := map[string]any{
		"root":      42,
		"dir":       true,
		"path":      []any{"x"},
		"pathEmpty": "",
	}
	if err := g.Check("kern_search", args); err != nil {
		t.Fatalf("non-string / empty path values must be skipped: %v", err)
	}
}

func TestGateCheckIncludesToolName(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	g := &Gate{roots: []string{root}, enabled: true}
	err := g.Check("kern_project_map", map[string]any{"root": outside})
	if err == nil {
		t.Fatal("outside root was allowed")
	}
	for _, want := range []string{"tool kern_project_map", `root=`, "outside allowed roots", "(allowed: " + root + ")"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q missing %q", err.Error(), want)
		}
	}
}

// TestDispatchGateRejectsOutsideRoots drives the gate through the real
// dispatch path: with KERN_MCP_ROOTS set, a path-typed argument outside the
// roots is rejected BEFORE its handler runs (the denied tool would have
// succeeded without the gate, so rejection proves the handler never ran),
// while an inside path reaches the handler and produces a normal result.
func TestDispatchGateRejectsOutsideRoots(t *testing.T) {
	rootDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(rootDir, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "b.go"), []byte("package b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KERN_MCP_ROOTS", rootDir)
	// Align the pre-existing KERN_ROOTS confinement with the gate so the
	// inside call is judged by the gate alone.
	t.Setenv("KERN_ROOTS", rootDir)

	t.Run("outside_root_denied_before_handler", func(t *testing.T) {
		req := writeReq("tools/call", 1, `{"name":"kern_project_map","arguments":{"root":"`+outside+`"}}`)
		in := strings.NewReader(req + "\n")
		buf := &bytes.Buffer{}
		s := NewServer(in, buf)
		if err := s.Serve(); err != nil {
			t.Fatalf("Serve: %v", err)
		}
		resp := decodeOne(t, buf.String())
		text, isErr := toolResultText(t, resp)
		if !isErr {
			t.Fatalf("outside root was not denied: %s", text)
		}
		if !strings.Contains(text, "pre-tool-use denied") || !strings.Contains(text, "outside allowed roots") {
			t.Fatalf("denial message missing: %q", text)
		}
	})

	t.Run("inside_path_runs_handler", func(t *testing.T) {
		args, err := json.Marshal(map[string]any{"root": rootDir, "path": filepath.Join(rootDir, "a.go")})
		if err != nil {
			t.Fatal(err)
		}
		req := writeReq("tools/call", 2, `{"name":"kern_compact_file","arguments":`+string(args)+`}`)
		in := strings.NewReader(req + "\n")
		buf := &bytes.Buffer{}
		s := NewServer(in, buf)
		if err := s.Serve(); err != nil {
			t.Fatalf("Serve: %v", err)
		}
		resp := decodeOne(t, buf.String())
		text, isErr := toolResultText(t, resp)
		if isErr {
			t.Fatalf("inside path was denied/errored: %s", text)
		}
		if text == "" {
			t.Fatal("inside path produced an empty result")
		}
	})

	t.Run("permissive_mode_preserves_default_behavior", func(t *testing.T) {
		t.Setenv("KERN_MCP_PERMISSIVE", "1")
		t.Setenv("KERN_MCP_ROOTS", "") // permissive opt-out -> gate disabled, loopback trust
		args, err := json.Marshal(map[string]any{"root": rootDir, "path": filepath.Join(rootDir, "a.go")})
		if err != nil {
			t.Fatal(err)
		}
		req := writeReq("tools/call", 3, `{"name":"kern_compact_file","arguments":`+string(args)+`}`)
		in := strings.NewReader(req + "\n")
		buf := &bytes.Buffer{}
		s := NewServer(in, buf)
		if err := s.Serve(); err != nil {
			t.Fatalf("Serve: %v", err)
		}
		resp := decodeOne(t, buf.String())
		text, isErr := toolResultText(t, resp)
		if isErr {
			t.Fatalf("permissive mode changed default behavior: %s", text)
		}
		if text == "" {
			t.Fatal("permissive mode produced an empty result")
		}
	})
}
