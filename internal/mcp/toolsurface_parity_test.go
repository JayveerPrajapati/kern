package mcp

import (
	"slices"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/mcp/toolsurface"
)

// TestDefaultToolsMatchToolsurface pins toolpolicy.go's defaultTools to the
// shared leaf definition (internal/mcp/toolsurface) — the two must name the
// same surface (deep-dive A1: kern_run recommendations annotate against the
// leaf, advertisement filters against this map; drift would make the
// annotation lie).
func TestDefaultToolsMatchToolsurface(t *testing.T) {
	if len(defaultTools) != 11 {
		t.Fatalf("defaultTools size = %d, want 11", len(defaultTools))
	}
	if len(toolsurface.Default) != len(defaultTools) {
		t.Fatalf("toolsurface.Default size = %d, defaultTools size = %d — surface drift", len(toolsurface.Default), len(defaultTools))
	}
	for _, n := range toolsurface.Default {
		if !defaultTools[n] {
			t.Errorf("toolsurface.Default names %q but toolpolicy.defaultTools does not", n)
		}
	}
}

// TestSurfaceCountsPinned derives the three surface facts every doc, plugin
// mirror and gate agrees on (perfection plan B2): the full catalog, the
// default advertised surface, and the full-only tools. Surface changes are
// deliberate: any drift here means the catalog, the default surface, or the
// KERN_MCP_FULL gating moved, and this pin must be updated IN THE SAME
// change — alongside the docs gates (TestDocToolCountsDerived,
// TestCurrentDocsToolNamesMatchCatalog) and the plugin mirrors
// (TestPluginMatchesMCPCatalog, TestPluginDefaultToolsMatchGoDefaultTools)
// that pin the downstream copies.
func TestSurfaceCountsPinned(t *testing.T) {
	full := ToolNames()
	if len(full) != 34 {
		t.Fatalf("full catalog = %d tools, want 34 — an unapproved surface change (updating this pin is a deliberate act)", len(full))
	}
	if len(defaultTools) != 11 {
		t.Fatalf("default surface = %d, want 11 — an unapproved surface change", len(defaultTools))
	}
	var fullOnly []string
	for _, n := range full {
		if !defaultTools[n] {
			fullOnly = append(fullOnly, n)
		}
	}
	slices.Sort(fullOnly)
	want := []string{
		"kern_apply", "kern_arch", "kern_ast_search", "kern_compact_file", "kern_doc", "kern_evidence",
		"kern_exec", "kern_fetch_raw_anchor", "kern_fit_context", "kern_heal", "kern_mask_pii", "kern_memory",
		"kern_optimize", "kern_path", "kern_plan", "kern_plan_context", "kern_project_map",
		"kern_run", "kern_semcache", "kern_skill", "kern_stats", "kern_swap", "kern_why",
	}
	if !slices.Equal(fullOnly, want) {
		t.Fatalf("full-only tools = %v, want the 23 catalog tools outside the direct surface", fullOnly)
	}
}
