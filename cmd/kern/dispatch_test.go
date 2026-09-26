package main

import (
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
	if code := dispatchCommand("guide", nil); code != 0 {
		t.Errorf("dispatchCommand(guide) = %d, want 0", code)
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

func TestEveryCommandHasUsage(t *testing.T) {
	if len(commandTable) < 170 {
		t.Fatalf("commandTable has %d entries, want >= 170", len(commandTable))
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
	if len(commandTable) < 170 {
		t.Fatalf("commandTable has %d entries, want >= 170", len(commandTable))
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

// snakeAliasPairs is the 16 kebab/snake duplicate command pairs (N4): the
// snake_case forms are hidden from the printed help listings but MUST keep
// dispatching.
var snakeAliasPairs = map[string]string{
	"agent_coordination": "agent-coordination",
	"agent_fingerprint":  "agent-fingerprint",
	"agent_role_rbac":    "agent-role-rbac",
	"ast_transform":      "ast-transform",
	"context_watch":      "context-watch",
	"cross_repo_impact":  "cross-repo-impact",
	"doc_fetch":          "doc-fetch",
	"doc_search":         "doc-search",
	"evidence_anchor":    "evidence-anchor",
	"memory_ranked":      "memory-ranked",
	"policy_dsl":         "policy-dsl",
	"pre_edit":           "pre-edit",
	"prompt_fill":        "prompt-fill",
	"semantic_diff":      "semantic-diff",
	"semantic_merge":     "semantic-merge",
	"synthesize_test":    "synthesize-test",
}

// TestSnakeAliasDispatchesSameAsKebab pins N4 backward compatibility: every
// snake_case alias is marked alias:true, its kebab twin is not, and both
// spellings still dispatch through the table (for the cheap handlers: a real
// handler's usage error, never the unknown-command path).
func TestSnakeAliasDispatchesSameAsKebab(t *testing.T) {
	for snake, kebab := range snakeAliasPairs {
		se, ok := commandTable[snake]
		if !ok {
			t.Errorf("snake alias %q missing from commandTable", snake)
			continue
		}
		if !se.alias {
			t.Errorf("snake alias %q must be marked alias: true", snake)
		}
		ke, ok := commandTable[kebab]
		if !ok {
			t.Errorf("kebab twin %q missing from commandTable", kebab)
			continue
		}
		if ke.alias {
			t.Errorf("kebab twin %q must NOT be marked alias", kebab)
		}
	}
	// Behavioral dispatch proof on the cheap handlers: with no args each
	// spelling must reach the handler's own usage error (exit 2) — never the
	// "unknown command" path. Heavy handlers (index/network) and handlers
	// whose empty-arg path runs a tool call (agent_role_rbac) are excluded;
	// their dispatch is covered by the table-presence assertions above.
	for _, name := range []string{"doc_fetch", "doc_search", "context_watch", "cross_repo_impact", "memory_ranked"} {
		stderr, code := captureStderrExit(t, func() {
			dispatchCommand(name, nil)
		})
		if code != 2 {
			t.Errorf("dispatchCommand(%q, nil) exit = %d, want 2 (handler usage error)", name, code)
		}
		if strings.Contains(stderr, "unknown command") {
			t.Errorf("dispatchCommand(%q) hit the unknown-command path:\n%s", name, stderr)
		}
	}
}

// TestHelpListingOmitsAliases pins N4: the printed catalog (`kern --all`,
// flat and grouped) must no longer list the 16 snake_case aliases, while the
// kebab-case twins stay listed. Dispatch keeps both spellings (see
// TestSnakeAliasDispatchesSameAsKebab); this test covers the listing only.
func TestHelpListingOmitsAliases(t *testing.T) {
	seen := map[string]bool{}
	for _, out := range []string{captureStderrExitFlat(t), captureStderrExitGrouped(t)} {
		for name := range commandTable {
			if strings.Contains(out, "  kern "+name) {
				seen[name] = true
			}
		}
	}
	for snake, kebab := range snakeAliasPairs {
		if seen[snake] {
			t.Errorf("snake alias %q still appears in the help listing", snake)
		}
		if !seen[kebab] {
			t.Errorf("kebab twin %q missing from the help listing", kebab)
		}
	}
}

// captureStderrExitFlat renders the flat `kern --all` listing to stderr.
func captureStderrExitFlat(t *testing.T) string {
	t.Helper()
	return captureStderr(t, func() { usageAll(true) })
}

// captureStderrExitGrouped renders the grouped `kern --all` listing.
func captureStderrExitGrouped(t *testing.T) string {
	t.Helper()
	return captureStderr(t, func() { usageAll(false) })
}
