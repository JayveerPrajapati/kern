package mcp

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// benchProject writes the minimal demo project used by the mcp handler tests
// (same shape as mcpProject, but benchmark-typed).
func benchProject(b *testing.B) string {
	b.Helper()
	root := b.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module demo\n\ngo 1.22\n"), 0o644); err != nil {
		b.Fatal(err)
	}
	src := "package main\n\n// Greet says hello.\nfunc Greet() { println(\"hi\") }\n\n// helper is unused.\nfunc helper() {}\n\nfunc main() { Greet() }\n"
	if err := os.WriteFile(filepath.Join(root, "app.go"), []byte(src), 0o644); err != nil {
		b.Fatal(err)
	}
	return root
}

// BenchmarkDispatch measures the MCP server's dispatch path for a few
// representative tool calls: a retrieval tool (kern_search), the NL meta
// router (kern_meta), and a project-wide walk (kern_project_map). The
// session index is built once in the pre-warm so the timed loop measures
// steady-state dispatch (session-cached index), not the initial index build.
func BenchmarkDispatch(b *testing.B) {
	root := benchProject(b)
	s := NewServer(strings.NewReader(""), io.Discard)
	ctx := context.Background()
	calls := []struct {
		name string
		args map[string]any
	}{
		{"kern_search", map[string]any{"root": root, "query": "Greet"}},
		{"kern_meta", map[string]any{"root": root, "request": "locate the greeting function"}},
		{"kern_project_map", map[string]any{"root": root}},
	}
	// Pre-warm: build the session index once and verify each tool dispatches.
	for _, c := range calls {
		if _, err := s.dispatchTool(ctx, "bench", c.name, c.args); err != nil {
			b.Fatalf("pre-warm %s: %v", c.name, err)
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, c := range calls {
			if _, err := s.dispatchTool(ctx, "bench", c.name, c.args); err != nil {
				b.Fatal(err)
			}
		}
	}
}
