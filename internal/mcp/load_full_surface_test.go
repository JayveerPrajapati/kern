package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/optimize"
	"github.com/JayveerPrajapati/kern/internal/testfixture"
)

func fullSurfaceLoadFixture(t *testing.T) (string, *Server) {
	t.Helper()
	root := testfixture.Generate(t, testfixture.RepoSpec{
		Files:          2,
		SymbolsPerFile: 2,
		DeepCallGraph:  true,
	})
	return root, e2eFullSurfaceServer(t, root)
}

func runFullSurfaceLoad(t *testing.T, s *Server, tool string, persona e2ePersona, makeArgs func(i int) map[string]any) {
	t.Helper()
	before := goroutineCount()
	result := runToolLoad(t, s, e2eLoadCase{
		Name:        "smoke",
		Tool:        tool,
		Persona:     persona,
		Requests:    4,
		Concurrency: 2,
		Timeout:     10 * time.Second,
	}, makeArgs)
	assertE2ELoadOK(t, result, 4)
	assertNoGoroutineLeak(t, before)
}

func TestLoad_kern_optimize(t *testing.T) {
	root, s := fullSurfaceLoadFixture(t)
	runFullSurfaceLoad(t, s, "kern_optimize", personaDeveloper, func(i int) map[string]any {
		return map[string]any{
			"root":   root,
			"action": "output",
			"text":   strings.Repeat("kern keeps context compact. ", 128),
		}
	})
}

func TestLoad_kern_fetch_raw_anchor(t *testing.T) {
	_, s := fullSurfaceLoadFixture(t)
	anchorID := optimize.StoreAnchor("full-surface deterministic load anchor")
	runFullSurfaceLoad(t, s, "kern_fetch_raw_anchor", personaDeveloper, func(i int) map[string]any {
		return map[string]any{"anchor_id": anchorID}
	})
}

func TestLoad_kern_memory(t *testing.T) {
	root, s := fullSurfaceLoadFixture(t)
	runFullSurfaceLoad(t, s, "kern_memory", personaDeveloper, func(i int) map[string]any {
		return map[string]any{"root": root, "action": "list"}
	})
}

func TestLoad_kern_mask_pii(t *testing.T) {
	root, s := fullSurfaceLoadFixture(t)
	runFullSurfaceLoad(t, s, "kern_mask_pii", personaAgent, func(i int) map[string]any {
		return map[string]any{"root": root, "text": "email user@example.com token sk-abcdefghijklmnopqrstuvwxyz"}
	})
}

func TestLoad_kern_doc(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "readme.md"), []byte("# Kern docs\n\nKern local documentation fixture.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := e2eFullSurfaceServer(t, root)
	resp := e2eCall(t, s, 5001, "kern_doc", map[string]any{"action": "index", "root": root})
	assertE2EContract(t, "kern_doc/load-index", resp, e2eContract{Success: true})

	runFullSurfaceLoad(t, s, "kern_doc", personaDeveloper, func(i int) map[string]any {
		return map[string]any{"root": root, "action": "search", "query": "local documentation fixture"}
	})
}

func TestLoad_kern_swap(t *testing.T) {
	root, s := fullSurfaceLoadFixture(t)
	runFullSurfaceLoad(t, s, "kern_swap", personaDeveloper, func(i int) map[string]any {
		return map[string]any{"root": root, "text": "plain prose without fences stays as-is", "mode": "summary"}
	})
}

func TestLoad_kern_heal(t *testing.T) {
	root, s := fullSurfaceLoadFixture(t)
	runFullSurfaceLoad(t, s, "kern_heal", personaReleaseManager, func(i int) map[string]any {
		return map[string]any{"root": root, "max_rounds": "1", "timeout": "10"}
	})
}

func TestLoad_kern_compact_file(t *testing.T) {
	root, s := fullSurfaceLoadFixture(t)
	runFullSurfaceLoad(t, s, "kern_compact_file", personaAgent, func(i int) map[string]any {
		return map[string]any{"root": root, "path": filepath.Join(root, "main.go"), "tier": "summary"}
	})
}

func TestLoad_kern_fit_context(t *testing.T) {
	root, s := fullSurfaceLoadFixture(t)
	runFullSurfaceLoad(t, s, "kern_fit_context", personaDeveloper, func(i int) map[string]any {
		return map[string]any{"root": root, "files": filepath.Join(root, "main.go"), "max_tokens": 1000}
	})
}

func TestLoad_kern_project_map(t *testing.T) {
	root, s := fullSurfaceLoadFixture(t)
	runFullSurfaceLoad(t, s, "kern_project_map", personaArchitect, func(i int) map[string]any {
		return map[string]any{"root": root}
	})
}

func TestLoad_kern_stats(t *testing.T) {
	root, s := fullSurfaceLoadFixture(t)
	runFullSurfaceLoad(t, s, "kern_stats", personaDeveloper, func(i int) map[string]any {
		return map[string]any{"root": root, "days": "1"}
	})
}

func TestLoad_kern_semcache(t *testing.T) {
	root, s := fullSurfaceLoadFixture(t)
	runFullSurfaceLoad(t, s, "kern_semcache", personaAgent, func(i int) map[string]any {
		return map[string]any{"root": root, "action": "stats"}
	})
}

func TestLoad_kern_ast_search(t *testing.T) {
	root, s := fullSurfaceLoadFixture(t)
	runFullSurfaceLoad(t, s, "kern_ast_search", personaRefactorer, func(i int) map[string]any {
		return map[string]any{"root": root, "pattern": "func Symbol0000", "limit": "5"}
	})
}

func TestLoad_kern_why(t *testing.T) {
	root, s := fullSurfaceLoadFixture(t)
	runFullSurfaceLoad(t, s, "kern_why", personaDeveloper, func(i int) map[string]any {
		return map[string]any{"root": root, "symbol": "Symbol0000"}
	})
}

func TestLoad_kern_path(t *testing.T) {
	root, s := fullSurfaceLoadFixture(t)
	runFullSurfaceLoad(t, s, "kern_path", personaDeveloper, func(i int) map[string]any {
		return map[string]any{"root": root, "from": "Symbol0000", "to": "Symbol0001"}
	})
}

func TestLoad_kern_arch(t *testing.T) {
	root, s := fullSurfaceLoadFixture(t)
	runFullSurfaceLoad(t, s, "kern_arch", personaDeveloper, func(i int) map[string]any {
		return map[string]any{"root": root}
	})
}

func TestLoad_kern_plan_context(t *testing.T) {
	root, s := fullSurfaceLoadFixture(t)
	runFullSurfaceLoad(t, s, "kern_plan_context", personaDeveloper, func(i int) map[string]any {
		return map[string]any{"root": root, "change": "Symbol0000", "budget": 1000}
	})
}

func TestLoad_kern_exec(t *testing.T) {
	root, s := fullSurfaceLoadFixture(t)
	runFullSurfaceLoad(t, s, "kern_exec", personaDeveloper, func(i int) map[string]any {
		return map[string]any{"root": root, "list": true}
	})
}

func TestLoad_kern_plan(t *testing.T) {
	root, s := fullSurfaceLoadFixture(t)
	runFullSurfaceLoad(t, s, "kern_plan", personaArchitect, func(i int) map[string]any {
		return map[string]any{"root": root, "change": "Symbol0000"}
	})
}

func TestLoad_kern_run(t *testing.T) {
	root, s := fullSurfaceLoadFixture(t)
	runFullSurfaceLoad(t, s, "kern_run", personaDeveloper, func(i int) map[string]any {
		return map[string]any{"root": root, "intent": "Analyze the repository"}
	})
}

func TestLoad_kern_apply(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := e2eFullSurfaceServer(t, root)
	runFullSurfaceLoad(t, s, "kern_apply", personaReleaseManager, func(i int) map[string]any {
		return map[string]any{
			"root":    root,
			"dry_run": true,
			"edits":   `[{"path":"a.txt","content":"after\n"}]`,
			"format":  "json",
		}
	})
}

func TestLoad_kern_evidence(t *testing.T) {
	root := evidenceFixtureRoot(t)
	bundle := evidenceBundleFile(t, root)
	if data, err := os.ReadFile(bundle); err == nil {
		bundle = filepath.Join(root, "bundle.json")
		if err := os.WriteFile(bundle, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s := e2eFullSurfaceServer(t, root)
	runFullSurfaceLoad(t, s, "kern_evidence", personaSRE, func(i int) map[string]any {
		return map[string]any{"root": root, "action": "verify", "file": bundle}
	})
}
