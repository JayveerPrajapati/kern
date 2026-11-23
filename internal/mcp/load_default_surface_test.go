package mcp

import (
	"testing"
	"time"

	mcpcontext "github.com/JayveerPrajapati/kern/internal/mcp/context"
	"github.com/JayveerPrajapati/kern/internal/testfixture"
)

func runDefaultSurfaceLoad(t *testing.T, s *Server, tool string, persona e2ePersona, makeArgs func(i int) map[string]any) {
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
	mcpcontext.BuddyWarmWait()
	assertNoGoroutineLeak(t, before)
}

func defaultLoadFixture(t *testing.T, dirty bool) (string, *Server) {
	t.Helper()
	root := testfixture.Generate(t, testfixture.RepoSpec{
		Files:          2,
		SymbolsPerFile: 2,
		DeepCallGraph:  true,
		DirtyGit:       dirty,
	})
	return root, e2eDefaultServer(t, root)
}

func TestLoad_kern_buddy(t *testing.T) {
	root, s := defaultLoadFixture(t, false)
	resp := e2eCall(t, s, 6001, "kern_buddy", map[string]any{"root": root, "no_memory": true})
	assertE2EContract(t, "kern_buddy/load-warmup", resp, e2eContract{Success: true})
	runDefaultSurfaceLoad(t, s, "kern_buddy", personaAgent, func(i int) map[string]any {
		return map[string]any{"root": root, "no_memory": true}
	})
	mcpcontext.BuddyWarmWait()
}

func TestLoad_kern_context(t *testing.T) {
	root, s := defaultLoadFixture(t, false)
	runDefaultSurfaceLoad(t, s, "kern_context", personaRefactorer, func(i int) map[string]any {
		return map[string]any{"root": root, "symbol": "Symbol0000", "lines": 8}
	})
}

func TestLoad_kern_probe(t *testing.T) {
	root, s := defaultLoadFixture(t, false)
	runDefaultSurfaceLoad(t, s, "kern_probe", personaDeveloper, func(i int) map[string]any {
		return map[string]any{"root": root, "task": "Explain the Symbol0000 call path"}
	})
}

func TestLoad_kern_research(t *testing.T) {
	root, s := defaultLoadFixture(t, false)
	runDefaultSurfaceLoad(t, s, "kern_research", personaArchitect, func(i int) map[string]any {
		return map[string]any{"root": root, "task": "Research Symbol0000 and Symbol0001"}
	})
}

func TestLoad_kern_impact(t *testing.T) {
	root, s := defaultLoadFixture(t, false)
	runDefaultSurfaceLoad(t, s, "kern_impact", personaRefactorer, func(i int) map[string]any {
		return map[string]any{"root": root, "change": "Symbol0000"}
	})
}

func TestLoad_kern_review(t *testing.T) {
	root, s := defaultLoadFixture(t, true)
	runDefaultSurfaceLoad(t, s, "kern_review", personaSecurity, func(i int) map[string]any {
		return map[string]any{"root": root, "lens": "security"}
	})
}

func TestLoad_kern_verify(t *testing.T) {
	root, s := defaultLoadFixture(t, false)
	runDefaultSurfaceLoad(t, s, "kern_verify", personaSRE, func(i int) map[string]any {
		return map[string]any{"root": root, "types": "architecture"}
	})
}

func TestLoad_kern_meta(t *testing.T) {
	root, s := defaultLoadFixture(t, false)
	runDefaultSurfaceLoad(t, s, "kern_meta", personaAgent, func(i int) map[string]any {
		return map[string]any{"root": root, "request": "how does Symbol0000 work?"}
	})
}
