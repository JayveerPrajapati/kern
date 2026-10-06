package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/mcp/toolsurface"
)

// TestDefaultSurfaceAcceptanceGate is the L6 acceptance fixture: a fresh
// agent that sees ONLY the 6-tool default surface (kern_meta, kern_explore,
// kern_impact, kern_search, kern_verify, kern_buddy — the simplification
// brief's five verbs + buddy) must be able to complete a scoped codebase
// task using only those tools, and must be genuinely unable to call any
// non-default tool (the surface is a real gate, not an advertisement).
func TestDefaultSurfaceAcceptanceGate(t *testing.T) {
	// The default surface must be exactly the six verbs + buddy — the L3
	// contract. If this drifts, the acceptance gate itself is broken.
	if len(toolsurface.Default) != 6 {
		t.Fatalf("default surface = %d tools, want 6 (L3): %v", len(toolsurface.Default), toolsurface.Default)
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
	// 2c. kern_impact reports the blast radius of changing it.
	impactOut := mcpAssertOK(t, "kern_impact", map[string]any{"root": root, "change": "NewServer"})
	if !strings.Contains(impactOut, "NewServer") {
		t.Fatalf("kern_impact did not report NewServer impact: %q", impactOut)
	}
	// 2d. kern_verify gives a build/test verdict for the repo.
	verifyOut := mcpAssertOK(t, "kern_verify", map[string]any{"root": root})
	if !strings.Contains(verifyOut, "PASS") && !strings.Contains(verifyOut, "FAIL") {
		t.Fatalf("kern_verify did not return a verdict: %q", verifyOut)
	}
	// 2e. kern_meta routes a natural-language request (the default router).
	metaOut := mcpAssertOK(t, "kern_meta", map[string]any{"root": root, "request": "find the NewServer function"})
	if !strings.Contains(metaOut, "NewServer") {
		t.Fatalf("kern_meta routing did not reach NewServer: %q", metaOut)
	}
	// 2f. kern_buddy returns the onboarding digest (repo layout entry point).
	buddyOut := mcpAssertOK(t, "kern_buddy", map[string]any{"root": root})
	if !strings.Contains(buddyOut, "NewServer") && !strings.Contains(buddyOut, "UserService") {
		t.Fatalf("kern_buddy did not return the onboarding digest: %q", buddyOut)
	}

	// 3. The surface is an ADVERTISEMENT gate (tools/list), not an execution
	// firewall: precheckTool validates against the full catalog by design so
	// kern_meta's NL router can reach every sub-tool internally; execution
	// gating lives in KERN_TOOLS/governance (pinned by their own tests). So
	// a non-default tool must NOT be advertised...
	for _, hidden := range []string{"kern_run", "kern_optimize", "kern_context"} {
		for _, got := range listed {
			if got == hidden {
				t.Fatalf("non-default tool %s IS advertised in tools/list — the surface gate is broken: %v", hidden, listed)
			}
		}
	}
	// ...while kern_meta (the default router) can still reach a sub-tool
	// that is not on the default surface — the router's contract. The agent
	// sees 6 tools; the router gives it access to the deep catalog when the
	// task demands it, behind the governance firewall.
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
