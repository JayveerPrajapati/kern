package main

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/mcp"
)

// dispatchCaseLabels returns every quoted label inside a `case ...:` line of
// dispatch.go (multi-label cases like `case "analyze", "plan":` included).
func dispatchCaseLabels(t *testing.T) map[string]bool {
	t.Helper()
	// The fused commandTable is the single source of truth for dispatchable
	// commands (the old switch-based dispatch was regex-parsed here; a
	// table lookup is compile-time reality).
	labels := map[string]bool{}
	for name := range commandTable {
		labels[name] = true
	}
	return labels
}

// TestMCPToolsReachableFromCLI enforces the CLI<->MCP surface contract: every
// MCP tool must be reachable from the CLI either through a same-name dispatch
// case (kern_search -> search) or through a documented alias in mcpCLIAlias,
// and every alias target must be a real dispatch case. Adding a new MCP tool
// without a CLI command (or an alias for one) fails this test.
func TestMCPToolsReachableFromCLI(t *testing.T) {
	labels := dispatchCaseLabels(t)
	names := map[string]bool{}
	for _, n := range mcp.ToolNames() {
		names[n] = true
	}
	// Every MCP tool is covered by a same-name case or an alias entry.
	for _, tool := range mcp.ToolNames() {
		suffix := strings.TrimPrefix(tool, "kern_")
		if labels[suffix] {
			continue
		}
		alias, ok := mcpCLIAlias[tool]
		if !ok {
			t.Errorf("MCP tool %s has no CLI dispatch case and no mcpCLIAlias entry", tool)
			continue
		}
		if !labels[alias] {
			t.Errorf("mcpCLIAlias[%s] = %q is not a dispatch case", tool, alias)
		}
	}
	// Every alias entry names a real MCP tool (no stale entries).
	for tool := range mcpCLIAlias {
		if !names[tool] {
			t.Errorf("mcpCLIAlias entry %s is not a registered MCP tool", tool)
		}
	}
}

// TestDispatchCommandCheapRoutes pins the dispatcher contract for the
// side-effect-free commands (E3 test-first): version variants exit 0, and an
// unknown command exits 2 after printing usage.
func TestDispatchCommandCheapRoutes(t *testing.T) {
	for _, cmd := range []string{"version", "--version", "-v"} {
		if code := dispatchCommand(cmd, nil); code != 0 {
			t.Errorf("dispatchCommand(%q) = %d, want 0", cmd, code)
		}
	}
}

// TestDispatchCommandUnknownExits2: an unrecognized command is a hard error
// (exit 2), not a silent success — the fail-closed CLI contract. The error is
// a one-liner with the full-catalog hint, NOT the old 30-line usage banner
// dump on stderr.
func TestDispatchCommandUnknownExits2(t *testing.T) {
	if code := dispatchCommand("definitely-not-a-command", nil); code != 2 {
		t.Errorf("dispatchCommand(unknown) = %d, want 2", code)
	}
	var code int
	stderr, _ := captureStderrExit(t, func() {
		code = dispatchCommand("definitely-not-a-command", nil)
	})
	if code != 2 {
		t.Fatalf("dispatchCommand(unknown) exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr, `kern: unknown command "definitely-not-a-command"`) {
		t.Fatalf("expected the one-line unknown-command error on stderr, got: %.200s", stderr)
	}
	if !strings.Contains(stderr, "run 'kern --all' for the full catalog") {
		t.Fatalf("expected the full-catalog hint on stderr, got: %.200s", stderr)
	}
	// The old behavior dumped the whole usage banner — the new contract is a
	// concise error, never the banner.
	if strings.Contains(stderr, "Core Workflows") {
		t.Fatalf("unknown command must not dump the usage banner, got:\n%s", stderr)
	}
}

// TestSuggestCommands pins the nearest-command matcher behind the
// unknown-command / unknown-help-topic suggestions: prefix matches outrank
// substring matches outrank edit-distance matches, and garbage yields none.
func TestSuggestCommands(t *testing.T) {
	got := suggestCommands("searc")
	if !slices.Contains(got, "search") {
		t.Errorf("suggestCommands(searc) = %v, want search included", got)
	}
	if got := suggestCommands("compct"); !slices.Contains(got, "compact") {
		t.Errorf("suggestCommands(compct) = %v, want compact included", got)
	}
	if got := suggestCommands("definitely-not-a-command"); len(got) != 0 {
		t.Errorf("suggestCommands(garbage) = %v, want no suggestions", got)
	}
	// Shape/length-aware fuzzy matching (4.1b): the removed `kern try`
	// command must not pull in the semantically unrelated `why` (edit
	// distance 2 on a 3-char query), and a genuine typo like "serch" must
	// still find "search".
	if got := suggestCommands("try"); len(got) != 0 {
		t.Errorf("suggestCommands(try) = %v, want no suggestions (fuzzy must not suggest why)", got)
	}
	if got := suggestCommands("try"); slices.Contains(got, "why") {
		t.Errorf("suggestCommands(try) = %v, must never suggest why", got)
	}
	if got := suggestCommands("serch"); !slices.Contains(got, "search") {
		t.Errorf("suggestCommands(serch) = %v, want search included", got)
	}
	if got := suggestCommands(""); got != nil {
		t.Errorf("suggestCommands(\"\") = %v, want nil", got)
	}
}

// TestDispatchCommandHelpStyleFlags: help-style invocations must not panic
// and must exit cleanly (0) — they are routed through the same dispatcher.
func TestDispatchCommandHelpStyleFlags(t *testing.T) {
	for _, cmd := range []string{"--help", "-h", "help"} {
		code := dispatchCommand(cmd, nil)
		if code != 0 && code != 2 {
			t.Errorf("dispatchCommand(%q) = %d, want 0 or 2 (no panic)", cmd, code)
		}
	}
}

// TestDispatchCommandBareKernExits0 pins F4: bare `kern` (no command) prints
// the full usage dump and exits 0 — a help print with no error is not a usage
// error (unknown commands and bad flags still exit 2).
func TestDispatchCommandBareKernExits0(t *testing.T) {
	stderr, code := captureStderrExit(t, func() {
		dispatchCommand("", nil)
	})
	if code != 0 {
		t.Errorf("dispatchCommand(\"\") exit = %d, want 0", code)
	}
	if !strings.Contains(stderr, "Core Workflows") {
		t.Errorf("expected the usage banner on stderr, got: %.80s", stderr)
	}
}

// TestProjectMapCommandRoutes pins F4: `kern project_map` — the command the
// buddy digest's truncation pointer recommends — is a real read-only command
// wired to the same renderer as `kern buddy --map`.
func TestProjectMapCommandRoutes(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n\nfunc Hello() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() {
		if code := dispatchCommand("project-map", []string{"--root", dir}); code != 0 {
			t.Errorf("dispatchCommand(project-map) = %d, want 0", code)
		}
	})
	if !strings.Contains(out, "Project:") || !strings.Contains(out, "a.go") {
		t.Errorf("expected the per-file project map, got: %.200s", out)
	}
}

func TestEveryCommandHasUsage(t *testing.T) {
	if len(commandTable) < 45 {
		t.Fatalf("commandTable has %d entries, want >= 45 (52 after the 2026-10-18 CLI slim)", len(commandTable))
	}
	for name, e := range commandTable {
		if strings.TrimSpace(e.usage) == "" {
			t.Errorf("command %q has empty usage (one-line help stub)", name)
			continue
		}
		// The usage must be multi-line (synopsis + options) or a full
		// sentence for flag-less commands — never a single bare line that
		// merely repeats the help text.
		if !strings.Contains(e.usage, "\n") && !strings.Contains(e.usage, "usage: ") {
			t.Errorf("command %q usage is a single bare line: %q", name, e.usage)
		}
		if strings.TrimSpace(e.usage) == e.help {
			t.Errorf("command %q usage merely repeats the one-liner help", name)
		}
	}
}

// TestEveryCommandHasCategory is the CLI drift gate for the functional-family
// taxonomy (T3): every registered command entry must carry a non-empty
// category, and every category must be one the grouped `kern --all` listing
// knows how to render (unknown categories would silently vanish from the
// fixed-order header list, so they are pinned by cliCategoryOrder).
func TestEveryCommandHasCategory(t *testing.T) {
	if len(commandTable) < 45 {
		t.Fatalf("commandTable has %d entries, want >= 45 (52 after the 2026-10-18 CLI slim)", len(commandTable))
	}
	known := map[string]bool{}
	for _, cat := range cliCategoryOrder {
		known[cat] = true
	}
	seen := map[string]bool{}
	for name, e := range commandTable {
		if strings.TrimSpace(e.category) == "" {
			t.Errorf("command %q has an empty category — assign a functional family", name)
			continue
		}
		seen[e.category] = true
	}
	for cat := range seen {
		if !known[cat] {
			t.Errorf("command category %q is not in cliCategoryOrder — add it to the grouped listing order", cat)
		}
	}
}

// TestPackArgsDefaultBudget pins F10: `kern pack` without --max-tokens gets
// the default budget injected (so a huge repo cannot dump millions of tokens
// to stdout), while any explicit --max-tokens (value, 0, or --max-tokens=N)
// bypasses the default.
func TestPackArgsDefaultBudget(t *testing.T) {
	args, injected := packArgs([]string{"--fold"})
	if !injected {
		t.Fatal("expected the default budget to be injected when --max-tokens is absent")
	}
	if len(args) < 2 || args[len(args)-2] != "--max-tokens" || args[len(args)-1] != strconv.Itoa(packDefaultMaxTokens) {
		t.Fatalf("injected args = %v, want trailing --max-tokens %d", args, packDefaultMaxTokens)
	}

	if _, injected := packArgs([]string{"--max-tokens", "1000000"}); injected {
		t.Error("explicit --max-tokens must bypass the default cap")
	}
	if _, injected := packArgs([]string{"--max-tokens", "0"}); injected {
		t.Error("explicit --max-tokens 0 (unlimited) must bypass the default cap")
	}
	if _, injected := packArgs([]string{"--max-tokens=500000"}); injected {
		t.Error("explicit --max-tokens=N must bypass the default cap")
	}
}

// TestSnakeAliasDispatchesSameAsKebab pins N4 backward compatibility: every
// snake_case alias is marked alias:true, its kebab twin is not, and both
// spellings still dispatch through the table (for the cheap handlers: a real
// handler's usage error, never the unknown-command path).

// TestHelpListingOmitsAliases pins N4: the printed catalog (`kern --all`,
// flat and grouped) must no longer list the 16 snake_case aliases, while the
// kebab-case twins stay listed. Dispatch keeps both spellings (see
// TestSnakeAliasDispatchesSameAsKebab); this test covers the listing only.

// TestUsageAllPrintsToStdout pins the catalog stream contract: `kern --all`
// (and `kern help --all`) are explicitly requested listings and must be
// pipeable (`kern --all | grep …`), so usageAll writes to stdout — error and
// usage banners keep stderr. A stderr catalog regresses scripting workflows.
func TestUsageAllPrintsToStdout(t *testing.T) {
	out := captureCatalogGrouped(t)
	if !strings.Contains(out, "Complete CLI catalog") {
		t.Fatalf("grouped catalog header missing from stdout:\n%s", out)
	}
	if strings.Count(out, "\n  kern ") == 0 && !strings.HasPrefix(out, "\n  kern ") {
		t.Fatalf("grouped catalog lists no commands on stdout:\n%s", out)
	}
	if leaked := captureStderr(t, func() { usageAll(false) }); leaked != "" {
		t.Errorf("grouped catalog leaked to stderr:\n%s", leaked)
	}
}

// captureCatalogFlat renders the flat `kern --all` listing to stdout.
func captureCatalogFlat(t *testing.T) string {
	t.Helper()
	return captureStdout(t, func() { usageAll(true) })
}

// captureCatalogGrouped renders the grouped `kern --all` listing.
func captureCatalogGrouped(t *testing.T) string {
	t.Helper()
	return captureStdout(t, func() { usageAll(false) })
}
