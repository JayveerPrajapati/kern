package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/setup"
)

// TestCheckSummaryFreshDir pins the derived summary on a clean project:
// the distinct-agent count comes from the adapter registry (11 builtin
// names with no custom adapters under a fake HOME), zero surfaces wired,
// and the checked count equals the live table length.
func TestCheckSummaryFreshDir(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir()) // no host-installed wrappers leak into the check
	dir := t.TempDir()

	results := setup.Check(dir)
	distinct, _ := setup.RegistryCounts(dir)
	want := fmt.Sprintf("%d MCP clients · 0 config surfaces wired · %d checked", len(distinct), len(results))
	got := CheckSummary(dir, results)
	if got != want {
		t.Fatalf("CheckSummary = %q, want %q", got, want)
	}
}

// TestCheckSummaryAfterWire pins the wired-surface count after a full wire:
// every adapter-registry config surface carries a kern entry, so the middle
// count must equal the registry length (12 entries, copilot repo+global).
func TestCheckSummaryAfterWire(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	dir := t.TempDir()

	sts := setup.Wire(dir, nil, false, true)
	for _, s := range sts {
		if !s.Skipped && !s.Installed {
			t.Errorf("wire failure: %+v", s)
		}
	}
	results := setup.Check(dir)
	got := CheckSummary(dir, results)
	names, entries := setup.RegistryCounts(dir)
	want := fmt.Sprintf("%d MCP clients · %d config surfaces wired · %d checked", len(names), entries, len(results))
	if got != want {
		t.Fatalf("CheckSummary after wire = %q, want %q", got, want)
	}
}

// TestWiredStateNone pins the "none" case on a clean project.
func TestWiredStateNone(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	dir := t.TempDir()

	if got := WiredState(dir); got != "none" {
		t.Fatalf("WiredState on fresh dir = %q, want \"none\"", got)
	}
}

// TestWiredStatePresent pins the wired case: after a full wire the state
// lists the project-scoped surfaces (universal MCP files, AGENTS.md rules)
// and excludes user-global surfaces (global opencode config).
func TestWiredStatePresent(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	dir := t.TempDir()

	setup.Wire(dir, nil, false, true)
	got := WiredState(dir)
	if !strings.HasPrefix(got, "present: ") {
		t.Fatalf("WiredState after wire = %q, want a \"present: …\" list", got)
	}
	for _, want := range []string{"mcp (project .mcp.json)", "AGENTS.md rules", "opencode (project)"} {
		if !strings.Contains(got, want) {
			t.Errorf("WiredState = %q, missing project surface %q", got, want)
		}
	}
	if strings.Contains(got, "opencode (global config)") {
		t.Errorf("WiredState = %q, must exclude user-global surfaces", got)
	}
}
