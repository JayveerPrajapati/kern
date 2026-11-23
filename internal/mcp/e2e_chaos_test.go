package mcp

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/testfixture"
)

func TestE2E_kern_compact_file_path_escape(t *testing.T) {
	root := fixtureRoot(t)
	s := e2eDefaultServer(t, root)

	resp := e2eCall(t, s, 3001, "kern_compact_file", map[string]any{
		"root":     root,
		"path":     "../outside.go",
		"no_cache": true,
	})
	assertE2EToolError(t, "kern_compact_file/path-escape", resp, "outside")
}

func TestE2E_kern_fit_context_path_escape(t *testing.T) {
	root := fixtureRoot(t)
	s := e2eDefaultServer(t, root)

	resp := e2eCall(t, s, 3002, "kern_fit_context", map[string]any{
		"root":     root,
		"files":    "../outside.go",
		"no_cache": true,
	})
	assertE2EToolError(t, "kern_fit_context/path-escape", resp, "outside")
}

func TestE2E_kern_explore_stale_index(t *testing.T) {
	root := testfixture.Generate(t, testfixture.RepoSpec{
		Files:          2,
		SymbolsPerFile: 2,
		DeepCallGraph:  true,
	})
	s := e2eDefaultServer(t, root)

	// Build the index first.
	resp := e2eCall(t, s, 3003, "kern_explore", map[string]any{
		"root":   root,
		"symbol": "Symbol0000",
		"mode":   "concise",
	})
	assertE2EContract(t, "kern_explore/build-index", resp, e2eContract{Success: true})

	// Mutate the source after the index is built.
	file := filepath.Join(root, "file000.go")
	if err := os.WriteFile(file, []byte("package main\n\nfunc Symbol0000() int { return 42 }\n"), 0o644); err != nil {
		t.Fatalf("mutate source: %v", err)
	}

	resp = e2eCall(t, s, 3004, "kern_explore", map[string]any{
		"root":           root,
		"symbol":         "Symbol0000",
		"mode":           "concise",
		"with_freshness": true,
		"no_cache":       true,
	})
	assertE2EContract(t, "kern_explore/stale-index", resp, e2eContract{Success: true})
}

func TestE2E_default_surface_invalid_input_matrix(t *testing.T) {
	root := fixtureRoot(t)
	s := e2eDefaultServer(t, root)
	cases := []struct {
		tool string
		args map[string]any
		want string
	}{
		{"kern_buddy", map[string]any{"root": "/definitely/not/a/repo", "no_cache": true}, "cannot"},
		{"kern_search", map[string]any{"root": root, "no_cache": true}, "query is required"},
		{"kern_explore", map[string]any{"root": root, "no_cache": true}, "symbol is required"},
		{"kern_context", map[string]any{"root": root, "no_cache": true}, "symbol is required"},
		{"kern_probe", map[string]any{"root": root, "no_cache": true}, "task is required"},
		{"kern_research", map[string]any{"root": root, "no_cache": true}, "task is required"},
		{"kern_impact", map[string]any{"root": root, "no_cache": true}, "change is required"},
		{"kern_graph", map[string]any{"root": root, "symbol": "ZZZmissing"}, "unknown symbol"},
		{"kern_review", map[string]any{"root": root, "lens": "bogus"}, "unknown lens"},
		{"kern_verify", map[string]any{"root": root, "types": "zzz"}, "unknown verify type: zzz"},
		{"kern_meta", map[string]any{"root": root, "request": "tell me about the weather"}, "no code intent detected"},
	}
	for i, tc := range cases {
		resp := e2eCall(t, s, 3100+i, tc.tool, tc.args)
		assertE2EToolError(t, tc.tool+"/invalid-input", resp, tc.want)
	}
}

func TestE2E_kern_compact_file_symlink_escape(t *testing.T) {
	root := fixtureRoot(t)
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "outside.go")
	if err := os.WriteFile(outsideFile, []byte("package outside\n"), 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	link := filepath.Join(root, "outside-link.go")
	if err := os.Symlink(outsideFile, link); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	s := e2eDefaultServer(t, root)
	resp := e2eCall(t, s, 3111, "kern_compact_file", map[string]any{
		"root":     root,
		"path":     link,
		"no_cache": true,
	})
	assertE2EToolError(t, "kern_compact_file/symlink-escape", resp, "outside")
}

func TestE2E_kern_search_cache_purge_under_load(t *testing.T) {
	root := fixtureRoot(t)
	s := e2eDefaultServer(t, root)

	before := goroutineCount()
	result := runToolLoad(t, s, e2eLoadCase{
		Name:        "cache-purge-before",
		Tool:        "kern_search",
		Persona:     personaDeveloper,
		Requests:    4,
		Concurrency: 2,
	}, func(i int) map[string]any {
		return map[string]any{"root": root, "query": "NewServer", "limit": 5}
	})
	assertE2ELoadOK(t, result, 4)

	// Removing the on-disk store between load slices proves the index can be
	// reconstructed deterministically and that no stale in-memory result is
	// reported as a safe success after a purge.
	if err := os.RemoveAll(filepath.Join(root, ".kern")); err != nil {
		t.Fatalf("purge index store: %v", err)
	}
	result = runToolLoad(t, s, e2eLoadCase{
		Name:        "cache-purge-after",
		Tool:        "kern_search",
		Persona:     personaDeveloper,
		Requests:    4,
		Concurrency: 2,
	}, func(i int) map[string]any {
		return map[string]any{"root": root, "query": "NewServer", "limit": 5}
	})
	assertE2ELoadOK(t, result, 4)
	assertNoGoroutineLeak(t, before)
}
