package mcp

import (
	"testing"

	mcpcontext "github.com/JayveerPrajapati/kern/internal/mcp/context"
	"github.com/JayveerPrajapati/kern/internal/testfixture"
)

// e2eDefaultServer prepares the HTTP server for one default-surface tool test.
// Each caller still supplies its own fixture root so tests stay independent.
func e2eDefaultServer(t *testing.T, root string) *Server {
	t.Helper()
	t.Setenv("KERN_ROOTS", root)
	t.Setenv("KERN_MCP_ROOTS", root)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("KERN_PRELOAD", "0")
	return newHTTPServer()
}

func assertE2ELoadOK(t *testing.T, result loadResult, want int) {
	t.Helper()
	if result.Successes != want {
		t.Errorf("load successes = %d, want %d", result.Successes, want)
	}
	if result.ToolErrors != 0 {
		t.Errorf("load tool errors = %d", result.ToolErrors)
	}
	if result.TransportErr != 0 {
		t.Errorf("load transport errors = %d", result.TransportErr)
	}
	if result.P50 <= 0 || result.P95 <= 0 || result.P99 <= 0 {
		t.Errorf("load latency metrics missing: p50=%s p95=%s p99=%s", result.P50, result.P95, result.P99)
	}
}

func TestE2E_kern_buddy(t *testing.T) {
	root := fixtureRoot(t)
	s := e2eDefaultServer(t, root)

	t.Run("developer onboarding", func(t *testing.T) {
		resp := e2eCall(t, s, 101, "kern_buddy", map[string]any{"root": root, "no_cache": true})
		assertE2EContract(t, "kern_buddy/onboarding", resp, e2eContract{
			Success:     true,
			MustContain: []string{"# kern buddy briefing", "Project map"},
		})
	})

	t.Run("subagent no-memory", func(t *testing.T) {
		resp := e2eCall(t, s, 102, "kern_buddy", map[string]any{"root": root, "no_memory": true})
		assertE2EContract(t, "kern_buddy/no-memory", resp, e2eContract{
			Success:        true,
			MustContain:    []string{"# kern buddy briefing"},
			MustNotContain: []string{"Project memory", "Recent session activity"},
		})
	})

	t.Run("concurrent", func(t *testing.T) {
		before := goroutineCount()
		result := runToolLoad(t, s, e2eLoadCase{
			Name:        "buddy-smoke",
			Tool:        "kern_buddy",
			Persona:     personaAgent,
			Requests:    4,
			Concurrency: 2,
		}, func(i int) map[string]any {
			return map[string]any{"root": root, "no_memory": true}
		})
		assertE2ELoadOK(t, result, 4)
		mcpcontext.BuddyWarmWait()
		assertNoGoroutineLeak(t, before)
	})
}

func TestE2E_kern_search(t *testing.T) {
	root := fixtureRoot(t)
	s := e2eDefaultServer(t, root)

	t.Run("developer exact symbol", func(t *testing.T) {
		resp := e2eCall(t, s, 201, "kern_search", map[string]any{"root": root, "query": "NewServer", "limit": 5})
		assertE2EContract(t, "kern_search/exact", resp, e2eContract{
			Success:        true,
			MustContain:    []string{"NewServer"},
			WantProvenance: true,
		})
	})

	t.Run("refactorer path fragment", func(t *testing.T) {
		resp := e2eCall(t, s, 202, "kern_search", map[string]any{"root": root, "query": "web/server", "limit": 5})
		assertE2EContract(t, "kern_search/path", resp, e2eContract{
			Success:        true,
			MustContain:    []string{"web/server.go"},
			WantProvenance: true,
		})
	})

	t.Run("missing query", func(t *testing.T) {
		resp := e2eCall(t, s, 203, "kern_search", map[string]any{"root": root, "no_cache": true})
		assertE2EToolError(t, "kern_search/missing-query", resp, "query is required")
	})

	t.Run("concurrent", func(t *testing.T) {
		before := goroutineCount()
		result := runToolLoad(t, s, e2eLoadCase{
			Name:        "search-smoke",
			Tool:        "kern_search",
			Persona:     personaDeveloper,
			Requests:    8,
			Concurrency: 4,
		}, func(i int) map[string]any {
			return map[string]any{"root": root, "query": "NewServer", "limit": 5}
		})
		assertE2ELoadOK(t, result, 8)
		assertNoGoroutineLeak(t, before)
	})
}

func TestE2E_kern_explore(t *testing.T) {
	root := fixtureRoot(t)
	s := e2eDefaultServer(t, root)

	t.Run("developer concise", func(t *testing.T) {
		resp := e2eCall(t, s, 301, "kern_explore", map[string]any{"root": root, "symbol": "NewServer", "mode": "concise"})
		assertE2EContract(t, "kern_explore/concise", resp, e2eContract{
			Success:        true,
			MustContain:    []string{"NewServer"},
			WantProvenance: true,
		})
	})

	t.Run("refactorer evidence", func(t *testing.T) {
		resp := e2eCall(t, s, 302, "kern_explore", map[string]any{"root": root, "symbol": "NewServer", "mode": "evidence"})
		assertE2EContract(t, "kern_explore/evidence", resp, e2eContract{
			Success:        true,
			MustContain:    []string{"NewServer", "callers"},
			WantProvenance: true,
		})
	})

	t.Run("missing symbol", func(t *testing.T) {
		resp := e2eCall(t, s, 303, "kern_explore", map[string]any{"root": root, "no_cache": true})
		assertE2EToolError(t, "kern_explore/missing-symbol", resp, "symbol is required")
	})

	t.Run("concurrent", func(t *testing.T) {
		before := goroutineCount()
		result := runToolLoad(t, s, e2eLoadCase{
			Name:        "explore-smoke",
			Tool:        "kern_explore",
			Persona:     personaRefactorer,
			Requests:    8,
			Concurrency: 4,
		}, func(i int) map[string]any {
			return map[string]any{"root": root, "symbol": "NewServer", "mode": "balanced"}
		})
		assertE2ELoadOK(t, result, 8)
		assertNoGoroutineLeak(t, before)
	})
}

func TestE2E_kern_context(t *testing.T) {
	root := fixtureRoot(t)
	s := e2eDefaultServer(t, root)

	t.Run("developer default", func(t *testing.T) {
		resp := e2eCall(t, s, 401, "kern_context", map[string]any{"root": root, "symbol": "NewServer", "lines": 12})
		assertE2EContract(t, "kern_context/default", resp, e2eContract{
			Success:        true,
			MustContain:    []string{"NewServer"},
			WantProvenance: true,
		})
	})

	t.Run("agent freshness", func(t *testing.T) {
		resp := e2eCall(t, s, 402, "kern_context", map[string]any{"root": root, "symbol": "NewServer", "with_freshness": true})
		assertE2EContract(t, "kern_context/freshness", resp, e2eContract{
			Success:        true,
			MustContain:    []string{"NewServer", "freshness"},
			WantProvenance: true,
		})
	})

	t.Run("unknown symbol", func(t *testing.T) {
		resp := e2eCall(t, s, 403, "kern_context", map[string]any{"root": root, "symbol": "ZZZmissing", "no_cache": true})
		assertE2EContract(t, "kern_context/unknown", resp, e2eContract{
			Success:        true,
			MustContain:    []string{"no symbol found: ZZZmissing"},
			MustNotContain: []string{"resolved ZZZmissing"},
		})
	})

	t.Run("concurrent", func(t *testing.T) {
		before := goroutineCount()
		result := runToolLoad(t, s, e2eLoadCase{
			Name:        "context-smoke",
			Tool:        "kern_context",
			Persona:     personaAgent,
			Requests:    8,
			Concurrency: 4,
		}, func(i int) map[string]any {
			return map[string]any{"root": root, "symbol": "NewServer", "lines": 8}
		})
		assertE2ELoadOK(t, result, 8)
		assertNoGoroutineLeak(t, before)
	})
}

func TestE2E_kern_probe(t *testing.T) {
	root := fixtureRoot(t)
	s := e2eDefaultServer(t, root)

	t.Run("SRE bug report", func(t *testing.T) {
		resp := e2eCall(t, s, 501, "kern_probe", map[string]any{"root": root, "task": "what does NewServer touch"})
		assertE2EContract(t, "kern_probe/bug", resp, e2eContract{
			Success:        true,
			MustContain:    []string{"NewServer"},
			WantProvenance: true,
		})
	})

	t.Run("agent token budget", func(t *testing.T) {
		resp := e2eCall(t, s, 502, "kern_probe", map[string]any{"root": root, "task": "NewServer and UserService", "max_tokens": 1000})
		assertE2EContract(t, "kern_probe/budget", resp, e2eContract{
			Success:        true,
			MustContain:    []string{"NewServer"},
			WantProvenance: true,
		})
	})

	t.Run("missing task", func(t *testing.T) {
		resp := e2eCall(t, s, 503, "kern_probe", map[string]any{"root": root, "no_cache": true})
		assertE2EToolError(t, "kern_probe/missing-task", resp, "task is required")
	})

	t.Run("concurrent", func(t *testing.T) {
		before := goroutineCount()
		result := runToolLoad(t, s, e2eLoadCase{
			Name:        "probe-smoke",
			Tool:        "kern_probe",
			Persona:     personaSRE,
			Requests:    8,
			Concurrency: 4,
		}, func(i int) map[string]any {
			return map[string]any{"root": root, "task": "what does NewServer touch"}
		})
		assertE2ELoadOK(t, result, 8)
		assertNoGoroutineLeak(t, before)
	})
}

func TestE2E_kern_research(t *testing.T) {
	root := fixtureRoot(t)
	s := e2eDefaultServer(t, root)

	t.Run("architect multi-symbol", func(t *testing.T) {
		resp := e2eCall(t, s, 601, "kern_research", map[string]any{"root": root, "task": "NewServer and UserService"})
		assertE2EContract(t, "kern_research/multi", resp, e2eContract{
			Success:        true,
			MustContain:    []string{"NewServer", "UserService"},
			WantProvenance: true,
		})
	})

	t.Run("agent bounded", func(t *testing.T) {
		resp := e2eCall(t, s, 602, "kern_research", map[string]any{"root": root, "task": "NewServer and UserService", "max_tokens": 1000})
		assertE2EContract(t, "kern_research/bounded", resp, e2eContract{
			Success:        true,
			MustContain:    []string{"NewServer"},
			WantProvenance: true,
		})
	})

	t.Run("missing task", func(t *testing.T) {
		resp := e2eCall(t, s, 603, "kern_research", map[string]any{"root": root, "no_cache": true})
		assertE2EToolError(t, "kern_research/missing-task", resp, "task is required")
	})

	t.Run("concurrent", func(t *testing.T) {
		before := goroutineCount()
		result := runToolLoad(t, s, e2eLoadCase{
			Name:        "research-smoke",
			Tool:        "kern_research",
			Persona:     personaArchitect,
			Requests:    8,
			Concurrency: 4,
		}, func(i int) map[string]any {
			return map[string]any{"root": root, "task": "NewServer and UserService", "max_tokens": 1500}
		})
		assertE2ELoadOK(t, result, 8)
		assertNoGoroutineLeak(t, before)
	})
}

func TestE2E_kern_impact(t *testing.T) {
	root := fixtureRoot(t)
	s := e2eDefaultServer(t, root)

	t.Run("refactorer remove symbol", func(t *testing.T) {
		resp := e2eCall(t, s, 701, "kern_impact", map[string]any{"root": root, "change": "NewServer", "kind": "remove_symbol"})
		assertE2EContract(t, "kern_impact/remove", resp, e2eContract{
			Success:        true,
			MustContain:    []string{"IMPACT for:", "NewServer"},
			WantProvenance: true,
		})
	})

	t.Run("security risk", func(t *testing.T) {
		resp := e2eCall(t, s, 702, "kern_impact", map[string]any{"root": root, "change": "NewServer", "risk": true})
		assertE2EContract(t, "kern_impact/risk", resp, e2eContract{
			Success:     true,
			MustContain: []string{"RISK for:", "NewServer"},
		})
	})

	t.Run("missing change", func(t *testing.T) {
		resp := e2eCall(t, s, 703, "kern_impact", map[string]any{"root": root, "no_cache": true})
		assertE2EToolError(t, "kern_impact/missing-change", resp, "change is required")
	})

	t.Run("concurrent", func(t *testing.T) {
		before := goroutineCount()
		result := runToolLoad(t, s, e2eLoadCase{
			Name:        "impact-smoke",
			Tool:        "kern_impact",
			Persona:     personaRefactorer,
			Requests:    8,
			Concurrency: 4,
		}, func(i int) map[string]any {
			return map[string]any{"root": root, "change": "NewServer"}
		})
		assertE2ELoadOK(t, result, 8)
		assertNoGoroutineLeak(t, before)
	})
}

func TestE2E_kern_graph(t *testing.T) {
	root := fixtureRoot(t)
	s := e2eDefaultServer(t, root)

	t.Run("architect adjacency", func(t *testing.T) {
		resp := e2eCall(t, s, 801, "kern_graph", map[string]any{"root": root, "symbol": "NewServer", "format": "adjacency"})
		assertE2EContract(t, "kern_graph/adjacency", resp, e2eContract{
			Success:        true,
			MustContain:    []string{"NewServer"},
			WantProvenance: true,
		})
	})

	t.Run("developer one-line", func(t *testing.T) {
		resp := e2eCall(t, s, 802, "kern_graph", map[string]any{"root": root, "symbol": "NewServer", "format": "one-line"})
		assertE2EContract(t, "kern_graph/one-line", resp, e2eContract{
			Success:        true,
			MustContain:    []string{"NewServer"},
			WantProvenance: true,
		})
	})

	t.Run("unknown symbol", func(t *testing.T) {
		resp := e2eCall(t, s, 803, "kern_graph", map[string]any{"root": root, "symbol": "ZZZmissing", "no_cache": true})
		assertE2EToolError(t, "kern_graph/unknown", resp, "unknown symbol")
	})

	t.Run("concurrent", func(t *testing.T) {
		before := goroutineCount()
		result := runToolLoad(t, s, e2eLoadCase{
			Name:        "graph-smoke",
			Tool:        "kern_graph",
			Persona:     personaArchitect,
			Requests:    8,
			Concurrency: 4,
		}, func(i int) map[string]any {
			return map[string]any{"root": root, "symbol": "NewServer", "format": "one-line"}
		})
		assertE2ELoadOK(t, result, 8)
		assertNoGoroutineLeak(t, before)
	})
}

func TestE2E_kern_review(t *testing.T) {
	root := testfixture.Generate(t, testfixture.RepoSpec{Files: 3, SymbolsPerFile: 3, DeepCallGraph: true, DirtyGit: true})
	s := e2eDefaultServer(t, root)

	t.Run("refactorer working tree", func(t *testing.T) {
		resp := e2eCall(t, s, 901, "kern_review", map[string]any{"root": root, "no_cache": true})
		assertE2EContract(t, "kern_review/working-tree", resp, e2eContract{
			Success:     true,
			MustContain: []string{"# kern review"},
		})
	})

	t.Run("security lens", func(t *testing.T) {
		resp := e2eCall(t, s, 902, "kern_review", map[string]any{"root": root, "lens": "security"})
		assertE2EContract(t, "kern_review/security", resp, e2eContract{
			Success:     true,
			MustContain: []string{"lens: security (", "# kern review"},
		})
	})

	t.Run("invalid lens", func(t *testing.T) {
		resp := e2eCall(t, s, 903, "kern_review", map[string]any{"root": root, "lens": "bogus"})
		assertE2EToolError(t, "kern_review/invalid-lens", resp, "unknown lens")
	})

	t.Run("concurrent", func(t *testing.T) {
		before := goroutineCount()
		result := runToolLoad(t, s, e2eLoadCase{
			Name:        "review-smoke",
			Tool:        "kern_review",
			Persona:     personaRefactorer,
			Requests:    4,
			Concurrency: 2,
		}, func(i int) map[string]any {
			return map[string]any{"root": root, "lens": "security"}
		})
		assertE2ELoadOK(t, result, 4)
		assertNoGoroutineLeak(t, before)
	})
}

func TestE2E_kern_verify(t *testing.T) {
	root := testfixture.Generate(t, testfixture.RepoSpec{Files: 2, SymbolsPerFile: 2, DeepCallGraph: true})
	s := e2eDefaultServer(t, root)

	t.Run("SRE architecture", func(t *testing.T) {
		resp := e2eCall(t, s, 1001, "kern_verify", map[string]any{"root": root, "types": "architecture"})
		assertE2EContract(t, "kern_verify/architecture", resp, e2eContract{
			Success:     true,
			MustContain: []string{"verdict:"},
		})
	})

	t.Run("release manager full", func(t *testing.T) {
		resp := e2eCall(t, s, 1002, "kern_verify", map[string]any{"root": root, "types": "architecture", "full": true})
		assertE2EContract(t, "kern_verify/full", resp, e2eContract{
			Success:     true,
			MustContain: []string{"verdict:"},
		})
	})

	t.Run("invalid type", func(t *testing.T) {
		resp := e2eCall(t, s, 1003, "kern_verify", map[string]any{"root": root, "types": "zzz"})
		assertE2EToolError(t, "kern_verify/invalid-type", resp, "unknown verify type: zzz")
	})

	t.Run("concurrent", func(t *testing.T) {
		before := goroutineCount()
		result := runToolLoad(t, s, e2eLoadCase{
			Name:        "verify-smoke",
			Tool:        "kern_verify",
			Persona:     personaSRE,
			Requests:    4,
			Concurrency: 2,
		}, func(i int) map[string]any {
			return map[string]any{"root": root, "types": "architecture"}
		})
		assertE2ELoadOK(t, result, 4)
		assertNoGoroutineLeak(t, before)
	})
}

func TestE2E_kern_meta(t *testing.T) {
	root := fixtureRoot(t)
	s := e2eDefaultServer(t, root)

	t.Run("developer explore", func(t *testing.T) {
		resp := e2eCall(t, s, 1101, "kern_meta", map[string]any{"root": root, "request": "how does NewServer work?"})
		assertE2EContract(t, "kern_meta/explore", resp, e2eContract{
			Success:     true,
			MustContain: []string{"kern_explore", "NewServer"},
		})
	})

	t.Run("agent search", func(t *testing.T) {
		resp := e2eCall(t, s, 1102, "kern_meta", map[string]any{"root": root, "request": "search for NewServer"})
		assertE2EContract(t, "kern_meta/search", resp, e2eContract{
			Success:     true,
			MustContain: []string{"kern_search", "NewServer"},
		})
	})

	t.Run("no code intent", func(t *testing.T) {
		resp := e2eCall(t, s, 1103, "kern_meta", map[string]any{"root": root, "request": "tell me about the weather"})
		assertE2EToolError(t, "kern_meta/no-code-intent", resp, "no code intent detected")
	})

	t.Run("concurrent", func(t *testing.T) {
		before := goroutineCount()
		result := runToolLoad(t, s, e2eLoadCase{
			Name:        "meta-smoke",
			Tool:        "kern_meta",
			Persona:     personaAgent,
			Requests:    8,
			Concurrency: 4,
		}, func(i int) map[string]any {
			return map[string]any{"root": root, "request": "how does NewServer work?"}
		})
		assertE2ELoadOK(t, result, 8)
		assertNoGoroutineLeak(t, before)
	})
}
