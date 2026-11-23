package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/mcp/toolsurface"
)

// TestDefaultSurfaceAcceptanceGate is the L6 acceptance fixture: a fresh
// agent that sees the direct eleven-tool surface must complete a scoped
// codebase task using those tools alone. KERN_MCP_FULL=1 remains the explicit
// full-catalog mode.
func TestDefaultSurfaceAcceptanceGate(t *testing.T) {
	// The default surface must be exactly the toolsurface.Default set — eleven
	// direct workflow tools. If this drifts, the acceptance gate itself is broken.
	if len(toolsurface.Default) != 11 {
		t.Fatalf("default surface = %d tools, want 11: %v", len(toolsurface.Default), toolsurface.Default)
	}

	root := fixtureRoot(t) // real fixture repo: symbols NewServer, UserService, ...
	t.Setenv("KERN_ALLOW_EXEC", "1")

	// 1. tools/list advertises exactly the default surface (no env set →
	// filteredTools() falls back to defaultTools; KERN_MCP_FULL etc. are
	// unset in the harness).
	listed := toolsListed(t)
	if len(listed) != len(toolsurface.Default) {
		t.Fatalf("tools/list advertises %d tools, want %d (default surface): %v", len(listed), len(toolsurface.Default), listed)
	}
	for _, want := range toolsurface.Default {
		found := false
		for _, got := range listed {
			if got == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("default tool %s missing from tools/list: %v", want, listed)
		}
	}

	// 2. A scoped task — "find the greeting symbol, understand its callers,
	// and confirm what changes to it would affect" — completed through the
	// default tools alone.
	// 2a. kern_search locates the symbol.
	searchOut := mcpAssertOK(t, "kern_search", map[string]any{"root": root, "query": "NewServer"})
	if !strings.Contains(searchOut, "NewServer") {
		t.Fatalf("kern_search did not surface NewServer: %q", searchOut)
	}
	// 2b. kern_explore returns the symbol's source + call flow.
	exploreOut := mcpAssertOK(t, "kern_explore", map[string]any{"root": root, "symbol": "NewServer"})
	if !strings.Contains(exploreOut, "NewServer") {
		t.Fatalf("kern_explore did not return NewServer context: %q", exploreOut)
	}
	// 2c. kern_probe builds a task-level bundle across the named symbols.
	probeOut := mcpAssertOK(t, "kern_probe", map[string]any{"root": root, "task": "NewServer and UserService"})
	if !strings.Contains(probeOut, "NewServer") || !strings.Contains(probeOut, "UserService") {
		t.Fatalf("kern_probe did not return task context: %q", probeOut)
	}
	// 2c-2. kern_research returns a read-only multi-symbol research bundle.
	researchOut := mcpAssertOK(t, "kern_research", map[string]any{"root": root, "task": "NewServer and UserService"})
	if !strings.Contains(researchOut, "NewServer") || !strings.Contains(researchOut, "UserService") {
		t.Fatalf("kern_research did not return task research: %q", researchOut)
	}
	// 2d. kern_impact reports the blast radius of changing it.
	impactOut := mcpAssertOK(t, "kern_impact", map[string]any{"root": root, "change": "NewServer"})
	if !strings.Contains(impactOut, "NewServer") {
		t.Fatalf("kern_impact did not report NewServer impact: %q", impactOut)
	}
	// 2e. kern_verify gives a build/test verdict for the repo.
	verifyOut := mcpAssertOK(t, "kern_verify", map[string]any{"root": root})
	if !strings.Contains(verifyOut, "PASS") && !strings.Contains(verifyOut, "FAIL") {
		t.Fatalf("kern_verify did not return a verdict: %q", verifyOut)
	}
	// 2f. kern_meta routes a natural-language request (the default router).
	metaOut := mcpAssertOK(t, "kern_meta", map[string]any{"root": root, "request": "find the NewServer function"})
	if !strings.Contains(metaOut, "NewServer") {
		t.Fatalf("kern_meta routing did not reach NewServer: %q", metaOut)
	}
	// 2g. kern_buddy returns the onboarding digest (repo layout entry point).
	buddyOut := mcpAssertOK(t, "kern_buddy", map[string]any{"root": root})
	if !strings.Contains(buddyOut, "NewServer") && !strings.Contains(buddyOut, "UserService") {
		t.Fatalf("kern_buddy did not return the onboarding digest: %q", buddyOut)
	}

	// 3. The surface is both an ADVERTISEMENT gate and an execution boundary:
	// kern_meta routes only within the same filtered set tools/list serves.
	// KERN_TOOLS/governance still apply to every advertised sub-tool.
	subOut := mcpAssertOK(t, "kern_meta", map[string]any{"root": root, "request": "run a full verification of this repo"})
	if !strings.Contains(subOut, "kern_verify") && !strings.Contains(subOut, "verdict") {
		t.Fatalf("kern_meta did not route to a deep-catalog sub-tool: %q", subOut)
	}
}

// toolsListed returns the tool names advertised by tools/list on a default
// (no KERN_MCP_FULL) server.
func toolsListed(t *testing.T) []string {
	t.Helper()
	resp := serveOne(t, writeReq("tools/list", 99, ""))
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("tools/list has no result: %+v", resp)
	}
	raw, err := json.Marshal(result["tools"])
	if err != nil {
		t.Fatal(err)
	}
	var tools []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &tools); err != nil {
		t.Fatal(err)
	}
	out := make([]string, 0, len(tools))
	for _, tl := range tools {
		out = append(out, tl.Name)
	}
	return out
}
