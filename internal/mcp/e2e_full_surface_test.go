package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func e2eFullSurfaceServer(t *testing.T, root string) *Server {
	t.Helper()
	t.Setenv("KERN_MCP_FULL", "1")
	t.Setenv("KERN_MCP_PHASE", "")
	t.Setenv("KERN_MCP_SINGLE_TOOL", "0")
	return e2eDefaultServer(t, root)
}

func TestE2E_kern_optimize(t *testing.T) {
	root := fixtureRoot(t)
	s := e2eFullSurfaceServer(t, root)

	t.Run("agent output", func(t *testing.T) {
		text := strings.Repeat("kern keeps context compact. ", 128)
		resp := e2eCall(t, s, 2001, "kern_optimize", map[string]any{
			"action": "output",
			"text":   text,
			"root":   root,
		})
		assertE2EContract(t, "kern_optimize/output", resp, e2eContract{
			Success:     true,
			MustContain: []string{"kern keeps context compact."},
		})
	})

	t.Run("missing action", func(t *testing.T) {
		resp := e2eCall(t, s, 2002, "kern_optimize", map[string]any{"root": root, "no_cache": true})
		assertE2EToolError(t, "kern_optimize/missing-action", resp, "'action' is required")
	})
}

func TestE2E_kern_fetch_raw_anchor(t *testing.T) {
	root := fixtureRoot(t)
	s := e2eFullSurfaceServer(t, root)

	t.Run("security invalid anchor", func(t *testing.T) {
		resp := e2eCall(t, s, 2003, "kern_fetch_raw_anchor", map[string]any{"anchor_id": "anchor-doesnotexist"})
		assertE2EToolError(t, "kern_fetch_raw_anchor/invalid", resp, "not found")
	})

	t.Run("missing anchor", func(t *testing.T) {
		resp := e2eCall(t, s, 2004, "kern_fetch_raw_anchor", map[string]any{"no_cache": true})
		assertE2EToolError(t, "kern_fetch_raw_anchor/missing", resp, "anchor_id is required")
	})
}

func TestE2E_kern_memory(t *testing.T) {
	root := fixtureRoot(t)
	s := e2eFullSurfaceServer(t, root)

	t.Run("agent add and list", func(t *testing.T) {
		resp := e2eCall(t, s, 2005, "kern_memory", map[string]any{
			"action": "add",
			"lesson": "kern memory e2e lesson",
			"root":   root,
		})
		assertE2EContract(t, "kern_memory/add", resp, e2eContract{Success: true})

		resp = e2eCall(t, s, 2006, "kern_memory", map[string]any{"action": "list", "root": root})
		assertE2EContract(t, "kern_memory/list", resp, e2eContract{
			Success:     true,
			MustContain: []string{"kern memory e2e lesson"},
		})
	})

	t.Run("missing action", func(t *testing.T) {
		resp := e2eCall(t, s, 2007, "kern_memory", map[string]any{"root": root, "no_cache": true})
		assertE2EToolError(t, "kern_memory/missing-action", resp, "kern_memory: 'action' is required")
	})
}

func TestE2E_kern_mask_pii(t *testing.T) {
	root := fixtureRoot(t)
	s := e2eFullSurfaceServer(t, root)

	t.Run("security secrets", func(t *testing.T) {
		resp := e2eCall(t, s, 2008, "kern_mask_pii", map[string]any{
			"text": "email user@example.com token sk-abcdefghijklmnopqrstuvwxyz",
			"root": root,
		})
		assertE2EContract(t, "kern_mask_pii/secrets", resp, e2eContract{
			Success:        true,
			MustNotContain: []string{"user@example.com", "sk-abcdefghijklmnopqrstuvwxyz"},
		})
	})

	t.Run("missing text", func(t *testing.T) {
		resp := e2eCall(t, s, 2009, "kern_mask_pii", map[string]any{"root": root, "no_cache": true})
		assertE2EToolError(t, "kern_mask_pii/missing-text", resp, "text is required")
	})
}

func TestE2E_kern_doc(t *testing.T) {
	root := t.TempDir()
	docs := filepath.Join(root, "docs")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docs, "readme.md"), []byte("# Kern docs\n\nKern local documentation fixture.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := e2eFullSurfaceServer(t, root)

	t.Run("docs writer index and search", func(t *testing.T) {
		resp := e2eCall(t, s, 2010, "kern_doc", map[string]any{"action": "index", "root": root})
		assertE2EContract(t, "kern_doc/index", resp, e2eContract{Success: true})

		resp = e2eCall(t, s, 2011, "kern_doc", map[string]any{
			"action": "search",
			"query":  "local documentation fixture",
			"root":   root,
		})
		assertE2EContract(t, "kern_doc/search", resp, e2eContract{
			Success:     true,
			MustContain: []string{"Kern local documentation fixture."},
		})
	})

	t.Run("invalid action", func(t *testing.T) {
		resp := e2eCall(t, s, 2012, "kern_doc", map[string]any{"action": "bogus", "root": root, "no_cache": true})
		assertE2EToolError(t, "kern_doc/invalid-action", resp, "action")
	})
}

func TestE2E_kern_swap(t *testing.T) {
	root := fixtureRoot(t)
	s := e2eFullSurfaceServer(t, root)

	t.Run("refactorer identity", func(t *testing.T) {
		text := "plain prose without fences stays as-is"
		resp := e2eCall(t, s, 2013, "kern_swap", map[string]any{
			"text": text,
			"root": root,
			"mode": "summary",
		})
		assertE2EContract(t, "kern_swap/identity", resp, e2eContract{
			Success:     true,
			MustContain: []string{text},
		})
	})

	t.Run("missing text", func(t *testing.T) {
		resp := e2eCall(t, s, 2014, "kern_swap", map[string]any{"root": root, "no_cache": true})
		assertE2EToolError(t, "kern_swap/missing-text", resp, "text is required")
	})
}

func TestE2E_kern_heal(t *testing.T) {
	root := fixtureRoot(t)
	s := e2eFullSurfaceServer(t, root)

	t.Run("SRE invalid rounds", func(t *testing.T) {
		resp := e2eCall(t, s, 2015, "kern_heal", map[string]any{
			"root":       root,
			"max_rounds": "not-a-number",
			"no_cache":   true,
		})
		assertE2EToolError(t, "kern_heal/invalid-rounds", resp, "max_rounds")
	})

	t.Run("invalid timeout", func(t *testing.T) {
		resp := e2eCall(t, s, 2016, "kern_heal", map[string]any{
			"root":     root,
			"timeout":  "-1",
			"no_cache": true,
		})
		assertE2EToolError(t, "kern_heal/invalid-timeout", resp, "timeout")
	})
}

func TestE2E_kern_compact_file(t *testing.T) {
	root := fixtureRoot(t)
	s := e2eFullSurfaceServer(t, root)

	t.Run("developer summary", func(t *testing.T) {
		resp := e2eCall(t, s, 2017, "kern_compact_file", map[string]any{
			"root": root,
			"path": filepath.Join(root, "web/server.go"),
			"tier": "summary",
		})
		assertE2EContract(t, "kern_compact_file/summary", resp, e2eContract{
			Success:     true,
			MustContain: []string{"web/server.go"},
		})
	})

	t.Run("missing path", func(t *testing.T) {
		resp := e2eCall(t, s, 2018, "kern_compact_file", map[string]any{"root": root, "no_cache": true})
		assertE2EToolError(t, "kern_compact_file/missing-path", resp, "path is required")
	})
}

func TestE2E_kern_fit_context(t *testing.T) {
	root := fixtureRoot(t)
	s := e2eFullSurfaceServer(t, root)

	t.Run("agent files", func(t *testing.T) {
		resp := e2eCall(t, s, 2019, "kern_fit_context", map[string]any{
			"root":       root,
			"files":      filepath.Join(root, "web/server.go"),
			"max_tokens": 1000,
		})
		assertE2EContract(t, "kern_fit_context/files", resp, e2eContract{
			Success:     true,
			MustContain: []string{"web/server.go"},
		})
	})

	t.Run("no targets", func(t *testing.T) {
		resp := e2eCall(t, s, 2020, "kern_fit_context", map[string]any{"root": root, "no_cache": true})
		assertE2EContract(t, "kern_fit_context/no-targets", resp, e2eContract{
			Success:     true,
			MustContain: []string{"No matching files or symbols found to fit."},
		})
	})
}

func TestE2E_kern_project_map(t *testing.T) {
	root := fixtureRoot(t)
	s := e2eFullSurfaceServer(t, root)

	t.Run("developer map", func(t *testing.T) {
		resp := e2eCall(t, s, 2021, "kern_project_map", map[string]any{"root": root})
		assertE2EContract(t, "kern_project_map/map", resp, e2eContract{
			Success:     true,
			MustContain: []string{"web/server.go"},
		})
	})

	t.Run("invalid root", func(t *testing.T) {
		resp := e2eCall(t, s, 2022, "kern_project_map", map[string]any{"root": "/definitely/not/a/repo", "no_cache": true})
		assertE2EToolError(t, "kern_project_map/invalid-root", resp, "cannot")
	})
}

func TestE2E_kern_stats(t *testing.T) {
	root := fixtureRoot(t)
	s := e2eFullSurfaceServer(t, root)

	t.Run("SRE stats", func(t *testing.T) {
		resp := e2eCall(t, s, 2023, "kern_stats", map[string]any{"days": "1"})
		assertE2EContract(t, "kern_stats/stats", resp, e2eContract{Success: true})
	})

	t.Run("invalid days", func(t *testing.T) {
		resp := e2eCall(t, s, 2024, "kern_stats", map[string]any{"days": "abc", "no_cache": true})
		assertE2EToolError(t, "kern_stats/invalid-days", resp, "days")
	})
}

func TestE2E_kern_semcache(t *testing.T) {
	root := fixtureRoot(t)
	s := e2eFullSurfaceServer(t, root)

	t.Run("SRE stats", func(t *testing.T) {
		resp := e2eCall(t, s, 2025, "kern_semcache", map[string]any{"action": "stats"})
		assertE2EContract(t, "kern_semcache/stats", resp, e2eContract{Success: true})
	})

	t.Run("list without namespace", func(t *testing.T) {
		resp := e2eCall(t, s, 2026, "kern_semcache", map[string]any{"action": "list", "no_cache": true})
		assertE2EToolError(t, "kern_semcache/missing-namespace", resp, "namespace is required")
	})
}

func TestE2E_kern_ast_search(t *testing.T) {
	root := fixtureRoot(t)
	s := e2eFullSurfaceServer(t, root)

	t.Run("developer function", func(t *testing.T) {
		resp := e2eCall(t, s, 2027, "kern_ast_search", map[string]any{
			"root":    root,
			"pattern": "func NewServer",
			"limit":   "5",
		})
		assertE2EContract(t, "kern_ast_search/function", resp, e2eContract{
			Success:     true,
			MustContain: []string{"NewServer"},
		})
	})

	t.Run("missing pattern", func(t *testing.T) {
		resp := e2eCall(t, s, 2028, "kern_ast_search", map[string]any{"root": root, "no_cache": true})
		assertE2EToolError(t, "kern_ast_search/missing-pattern", resp, "pattern is required")
	})
}

func TestE2E_kern_why(t *testing.T) {
	root := fixtureRoot(t)
	s := e2eFullSurfaceServer(t, root)

	t.Run("architect why", func(t *testing.T) {
		resp := e2eCall(t, s, 2029, "kern_why", map[string]any{"root": root, "symbol": "NewServer"})
		assertE2EContract(t, "kern_why/why", resp, e2eContract{
			Success:     true,
			MustContain: []string{"NewServer"},
		})
	})

	t.Run("missing symbol", func(t *testing.T) {
		resp := e2eCall(t, s, 2030, "kern_why", map[string]any{"root": root, "no_cache": true})
		assertE2EToolError(t, "kern_why/missing-symbol", resp, "symbol is required")
	})
}

func TestE2E_kern_path(t *testing.T) {
	root := fixtureRoot(t)
	s := e2eFullSurfaceServer(t, root)

	t.Run("architect path", func(t *testing.T) {
		resp := e2eCall(t, s, 2031, "kern_path", map[string]any{
			"root": root,
			"from": "NewServer",
			"to":   "NewUserService",
		})
		assertE2EContract(t, "kern_path/path", resp, e2eContract{Success: true})
	})

	t.Run("missing from", func(t *testing.T) {
		resp := e2eCall(t, s, 2032, "kern_path", map[string]any{"root": root, "to": "NewUserService", "no_cache": true})
		assertE2EToolError(t, "kern_path/missing-from", resp, "from and to are required")
	})
}

func TestE2E_kern_arch(t *testing.T) {
	root := fixtureRoot(t)
	s := e2eFullSurfaceServer(t, root)

	t.Run("architect overview", func(t *testing.T) {
		resp := e2eCall(t, s, 2033, "kern_arch", map[string]any{"root": root})
		assertE2EContract(t, "kern_arch/overview", resp, e2eContract{Success: true})
	})

	t.Run("invalid root", func(t *testing.T) {
		resp := e2eCall(t, s, 2034, "kern_arch", map[string]any{"root": "/definitely/not/a/repo", "no_cache": true})
		assertE2EToolError(t, "kern_arch/invalid-root", resp, "cannot")
	})
}

func TestE2E_kern_plan_context(t *testing.T) {
	root := fixtureRoot(t)
	s := e2eFullSurfaceServer(t, root)

	t.Run("planner change", func(t *testing.T) {
		resp := e2eCall(t, s, 2035, "kern_plan_context", map[string]any{
			"root":   root,
			"change": "NewServer",
			"budget": 1000,
		})
		assertE2EContract(t, "kern_plan_context/change", resp, e2eContract{
			Success:     true,
			MustContain: []string{"NewServer"},
		})
	})

	t.Run("missing change", func(t *testing.T) {
		resp := e2eCall(t, s, 2036, "kern_plan_context", map[string]any{"root": root, "no_cache": true})
		assertE2EToolError(t, "kern_plan_context/missing-change", resp, "change is required")
	})
}

func TestE2E_kern_exec(t *testing.T) {
	root := fixtureRoot(t)
	s := e2eFullSurfaceServer(t, root)

	t.Run("SRE runtime list", func(t *testing.T) {
		resp := e2eCall(t, s, 2037, "kern_exec", map[string]any{"list": true})
		assertE2EContract(t, "kern_exec/list", resp, e2eContract{
			Success:     true,
			MustContain: []string{"installed runtimes:"},
		})
	})

	t.Run("missing code", func(t *testing.T) {
		resp := e2eCall(t, s, 2038, "kern_exec", map[string]any{"root": root, "no_cache": true})
		assertE2EToolError(t, "kern_exec/missing-code", resp, "empty script")
	})
}

func TestE2E_kern_plan(t *testing.T) {
	root := fixtureRoot(t)
	s := e2eFullSurfaceServer(t, root)

	t.Run("refactorer plan", func(t *testing.T) {
		resp := e2eCall(t, s, 2039, "kern_plan", map[string]any{
			"root":   root,
			"change": "NewServer",
		})
		assertE2EContract(t, "kern_plan/plan", resp, e2eContract{Success: true})
	})

	t.Run("missing change", func(t *testing.T) {
		resp := e2eCall(t, s, 2040, "kern_plan", map[string]any{"root": root, "no_cache": true})
		assertE2EToolError(t, "kern_plan/missing-change", resp, "change is required")
	})
}

func TestE2E_kern_run(t *testing.T) {
	root := fixtureRoot(t)
	s := e2eFullSurfaceServer(t, root)

	t.Run("agent intent", func(t *testing.T) {
		resp := e2eCall(t, s, 2041, "kern_run", map[string]any{
			"root":   root,
			"intent": "Analyze the repository",
		})
		assertE2EContract(t, "kern_run/intent", resp, e2eContract{Success: true})
	})

	t.Run("missing intent", func(t *testing.T) {
		resp := e2eCall(t, s, 2042, "kern_run", map[string]any{"root": root, "no_cache": true})
		assertE2EToolError(t, "kern_run/missing-intent", resp, "intent is required")
	})
}

func TestE2E_kern_apply(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := e2eFullSurfaceServer(t, root)

	t.Run("agent dry run", func(t *testing.T) {
		resp := e2eCall(t, s, 2043, "kern_apply", map[string]any{
			"root":    root,
			"dry_run": true,
			"edits":   `[{"path":"a.txt","content":"after\n"}]`,
			"format":  "json",
		})
		assertE2EContract(t, "kern_apply/dry-run", resp, e2eContract{
			Success:     true,
			MustContain: []string{`"applied": false`},
		})
	})

	t.Run("missing edits", func(t *testing.T) {
		resp := e2eCall(t, s, 2044, "kern_apply", map[string]any{"root": root, "no_cache": true})
		assertE2EToolError(t, "kern_apply/missing-edits", resp, "edits parameter is required")
	})
}

func TestE2E_kern_evidence(t *testing.T) {
	root := evidenceFixtureRoot(t)
	bundle := evidenceBundleFile(t, root)
	if data, err := os.ReadFile(bundle); err == nil {
		bundle = filepath.Join(root, "bundle.json")
		if err := os.WriteFile(bundle, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s := e2eFullSurfaceServer(t, root)

	t.Run("release manager verify", func(t *testing.T) {
		resp := e2eCall(t, s, 2045, "kern_evidence", map[string]any{
			"action": "verify",
			"file":   bundle,
			"root":   root,
		})
		assertE2EContract(t, "kern_evidence/verify", resp, e2eContract{
			Success:     true,
			MustContain: []string{`"valid": true`},
		})
	})

	t.Run("missing file", func(t *testing.T) {
		resp := e2eCall(t, s, 2046, "kern_evidence", map[string]any{
			"action":   "verify",
			"root":     root,
			"no_cache": true,
		})
		assertE2EToolError(t, "kern_evidence/missing-file", resp, "file")
	})
}
