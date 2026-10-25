package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/mcp/toolsurface"
)

// TestDefaultSurfaceAcceptanceGate is the L6 acceptance fixture: a fresh
// agent that sees the full 32-tool default surface (the original L3+D5 set —
// kern_meta, kern_explore, kern_impact, kern_search, kern_verify, kern_buddy,
// kern_exec — plus the 25 usage-curated additions: kern_compact_file,
// kern_run, kern_optimize, kern_context, kern_memory, kern_evidence,
// kern_probe, kern_plan, kern_doc, kern_project_map, kern_ast_search,
// kern_plan_context, kern_path, kern_semcache, kern_swap, kern_mask_pii,
// kern_fetch_raw_anchor, kern_review, kern_graph, kern_arch) must be able to
// complete a scoped codebase task using only those tools, with every catalog
// tool advertised (the surface is the full catalog, standing mandate
// 2026-10-23).
func TestDefaultSurfaceAcceptanceGate(t *testing.T) {
	// The default surface must be exactly the toolsurface.Default set — the
	// full 32-tool catalog (standing mandate 2026-10-23: agents use ALL
	// available tools). If this drifts, the acceptance gate itself is broken.
	if len(toolsurface.Default) != 32 {
		t.Fatalf("default surface = %d tools, want 32: %v", len(toolsurface.Default), toolsurface.Default)
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
	// gating lives in KERN_TOOLS/governance (pinned by their own tests). The
	// full 32-tool catalog is the default surface (standing mandate
	// 2026-10-23), so every catalog tool must be advertised.
	// ...while kern_meta (the default router) can still reach any sub-tool
	// internally — the router's contract. The agent sees the full 32-tool
	// catalog by default; kern_meta routes to every sub-tool behind the
	// governance firewall.
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
