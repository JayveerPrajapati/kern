package main

import (
	"sort"
	"strings"
	"testing"
)

// dispatch_usage_parity_test.go closes the usage-string content drift gap:
// the hand-written `usage` strings in the commandTable (dispatch_table.go)
// are not just help text — expectedFlags (cmd_blueprint_tools.go) PARSES
// them to derive the accepted flag set for the load-bearing NL-positional
// error, and the existing gates (TestEveryCommandHasUsage,
// TestEveryCommandHasCategory, TestCLIReferenceDocCoversCommandTable) only
// check presence, never content. This file pins the content:
//
//  1. goldenUsageFlags pins the exact per-command flag set every usage
//     string yields (any edit that changes a set fails until the map is
//     consciously updated);
//  2. a parser-oracle gate proves every documented flag either parses
//     through the shared parseFlags or is a known handler-read flag
//     (so a usage string can never advertise a flag no one reads);
//  3. the blueprint-tools family (where expectedFlags is load-bearing) is
//     additionally pinned to the flags runBlueprintToolCLI actually reads.
//
// Deterministic, fast, no fixtures beyond what the package already has.

// usageFlagTokens extracts the "--flag" tokens from a usage string exactly
// like expectedFlags (cmd_blueprint_tools.go) does: every
// whitespace-delimited token that STARTS with "--". Bracket-wrapped forms
// like [--root ROOT] are NOT extracted — the field "[--root" does not start
// with "--". This replica exists so the parity gate and the load-bearing
// function provably share one parse (the blueprint test asserts equality).
func usageFlagTokens(usage string) []string {
	seen := map[string]bool{}
	var toks []string
	for _, l := range strings.Split(usage, "\n") {
		for _, tok := range strings.Fields(l) {
			if strings.HasPrefix(tok, "--") && !seen[tok] {
				seen[tok] = true
				toks = append(toks, tok)
			}
		}
	}
	return toks
}

func sortedCommandKeys() []string {
	names := make([]string, 0, len(commandTable))
	for name := range commandTable {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// goldenUsageFlags pins the exact, order-preserved flag set that each
// dispatch-table command's usage string currently yields. This is the
// consciously-maintained expectation: a usage-string edit that adds,
// removes, reorders or renames a documented flag fails
// TestUsageFlagSetsPinnedToGolden until the entry is updated. Generated
// from the live table at authoring time (2026-10-01); every token below is
// the deterministic output of the same extraction expectedFlags uses.
var goldenUsageFlags = map[string][]string{
	"approve": {"--approver", "--prefix", "--reason", "--reject", "--root", "--scope"},

	"approval": {"--approver", "--reason", "--timeout", "--root"},

	"policy": {"--merge", "--file", "--json", "--root"},

	"arch": {"--json", "--root"},

	"ast": {"--all", "--root"},

	"audit": {"--file", "--json", "--limit", "--tail", "--root"},

	"authorize-context": {"--agent", "--task", "--deny-path", "--symbol", "--root", "--json"},

	"bench": {"--root", "--json"},

	"buddy": {"--map", "--root"},

	"build": {"--dir", "--session", "--root", "--timeout"},

	"commit": {"--all", "--dry-run", "--message"},

	"compact": {"--root", "--tier", "--etag"},

	"config": {"--json", "--root"},

	"context": {"--lens", "--lines", "--profile", "--root", "--etag"},

	"docs": {"--limit", "--root", "--semantic"},

	"doctor": {"--arch-drift", "--calibration", "--json", "--root"},

	"evidence": {"--full-state", "--restore", "--root", "--out", "--agent-id", "--task", "--sign", "--file", "--url", "--expect-fingerprint"},

	"exec": {"--json", "--lang", "--readonly", "--list", "--max", "--stdin", "--timeout"},

	"explain-context": {"--task", "--budget", "--json", "--root"},

	"orchestrate":     {"--budget", "--json", "--max-tokens", "--mode", "--root", "--task", "--with-skill"},
	"remember":        {"--auto", "--root"},
	"memory-ranked":   {"--half-life", "--k", "--root"},
	"evidence-anchor": {"--claim", "--file", "--line", "--root", "--symbol"},
	"health":          {"--json", "--root"},

	"explore": {"--depth", "--explain", "--json", "--max", "--max-tokens", "--min-confidence", "--root", "--etag"},

	"fingerprint": {"--file", "--json", "--root"},

	"fit-context": {"--file", "--symbol", "--query", "--max-tokens", "--root", "--json"},

	"gen-catalog": {"--root", "--check"},

	"gen-contracts": {"--root", "--check"},

	"gen-plugin": {"--root", "--check"},

	"gen-docs": {"--doc", "--root", "--check"},

	"graph": {"--cypher", "--entities", "--graphml", "--html", "--json", "--limit", "--max-tokens", "--mermaid", "--min-confidence", "--one-line", "--out", "--root", "--etag"},

	"guard": {"--task", "--check", "--reason", "--agent-id", "--duration", "--file", "--force", "--json", "--precision", "--range", "--require-boundaries", "--root", "--sarif", "--scope", "--threshold"},

	"heal": {"--force", "--yes", "--llm", "--task"},

	"hook": {"--range", "--global"},

	"impact": {"--json", "--precision", "--risk", "--lens", "--root", "--runtime"},

	"apply": {"--root", "--test", "--dry-run", "--json"},

	"index": {"--force", "--json", "--root", "--status", "--strict", "--update"},

	"mask": {"--names"},

	"mcp":        {"--http", "--tls-cert", "--tls-key"},
	"mcp-client": {"--url", "--root"},

	"memory": {"--auto", "--clear", "--json", "--limit", "--root"},

	"meta": {"--pipeline"},

	"onboard": {"--root"},

	"path": {"--from", "--to", "--json", "--min-confidence", "--root"},

	"optimize": {"--attach", "--cache", "--fewshot", "--kind", "--llm", "--mask", "--model", "--names", "--session"},

	"log": {"--cache", "--context-after", "--context-before", "--mask", "--names", "--profile", "--root"},

	"plan": {"--json", "--root", "--task"},

	"probe": {"--json", "--max", "--min-confidence"},

	"project-map": {"--root"},

	"reject": {"--approver", "--reason", "--root"},

	"review": {"--file", "--json", "--lens", "--max", "--profile", "--range", "--root", "--runtime"},

	"run": {"--root"},

	"search": {"--json", "--limit", "--repos", "--root", "--semantic", "--etag"},

	"sec": {"--engine", "--json", "--max", "--root", "--severity"},

	"semcache": {"--json"},

	"setup": {"--agents", "--agents-md", "--all-agents", "--check", "--detect", "--dry-run", "--global", "--global-only", "--global-rules", "--json", "--plain", "--remove", "--root", "--verify", "--yes"},

	"stats": {"--days", "--session", "--json", "--by-tool", "--by-agent", "--by-root", "--root", "--net"},

	"swap": {"--max", "--mode"},

	"update": {"--dry-run", "--force", "--pin", "--channel"},

	"verify": {"--cve", "--license", "--secrets", "--command", "--output", "--anchor", "--changed", "--types", "--short", "--fast", "--changed-since", "--staged", "--timeout", "--full", "--eval", "--json", "--root", "--scan", "--skill", "--verify-pipeline", "--verify-silent", "--verify-token-reduction"},

	"why": {"--json", "--min-confidence", "--root", "--etag"},
}

// TestUsageFlagSetsPinnedToGolden is the content-parity gate for the
// dispatch-table usage strings. Every command whose usage yields at least
// one "--flag" token must have a goldenUsageFlags entry whose set matches
// exactly; a golden entry whose command vanished (or whose usage lost all
// flags) also fails. A usage-string edit that changes any documented flag
// set therefore fails this test until the map is consciously updated — the
// drift gap left open by the presence-only gates.
func TestUsageFlagSetsPinnedToGolden(t *testing.T) {
	for _, name := range sortedCommandKeys() {
		e := commandTable[name]
		got := usageFlagTokens(e.usage)
		want, pinned := goldenUsageFlags[name]
		switch {
		case len(got) == 0 && !pinned:
			continue // no flags documented, nothing to pin
		case len(got) == 0 && pinned:
			t.Errorf("command %q: usage string no longer documents any flag, but goldenUsageFlags pins %v — remove the entry or restore the flags", name, want)
		case len(got) > 0 && !pinned:
			t.Errorf("command %q: usage documents flags %v with no goldenUsageFlags entry — add one (conscious update)", name, got)
		case !equalStrings(got, want):
			t.Errorf("command %q: usage-derived flag set drifted\n  got:  %v\n  want: %v\nupdate goldenUsageFlags if the new set is intended", name, got, want)
		}
	}
	for name := range goldenUsageFlags {
		if _, ok := commandTable[name]; !ok {
			t.Errorf("goldenUsageFlags pins command %q which no longer exists in commandTable — remove the entry", name)
		}
	}
}

// handlerReadUsageFlags are documented flags the shared parseFlags does NOT
// know but a command handler reads itself (a hand-rolled strip/switch
// before or instead of parseFlags). Each entry states who reads it, so the
// list is auditable; adding a flag here without a matching reader is the
// exact drift the parser-oracle gate exists to catch.
var handlerReadUsageFlags = map[string]string{
	"--":                   "sandbox: command separator (kern sandbox [root] -- <command...>); parseFlags accepts it as a positional",
	"--anchor":             "verify: runVerifyCommand → internal/verifycmd command mode (re-slice a saved run by anchor id)",
	"--check":              "gen-docs/gen-catalog/gen-contracts/gen-plugin: runGenDocsImpl compares the generated artifact without writing it",
	"--duration":           "guard: internal/contextcli parsePauseFlags (pause subcommand)",
	"--scope":              "guard: internal/contextcli parsePauseFlags (pause subcommand); approve: internal/approvalcli parseFlags (Ask-Protocol --scope once|prefix)",
	"--prefix":             "approve: internal/approvalcli parseFlags (Ask-Protocol --scope prefix prefix override)",
	"--command":            "verify: runVerifyCommand → internal/verifycmd command mode (governed allowlist)",
	"--doc":                "gen-docs: runGenDocsImpl own switch (catalog|contracts|schemas|site|selection)",
	"--list":               "exec: runExec strips --list before parseFlags (kern exec --list lists runtimes; accepted in any position)",
	"--readonly":           "exec: internal/execution parseExecFlags (read-only bash fallbacks; skip the KERN_TOOLS/KERN_ALLOW_EXEC gate, keep the firewall)",
	"--require-boundaries": "guard: internal/contextcli RunGuard own switch (strict mode: fail when .kern/boundaries.json is missing)",
	"--map":                "brief/buddy: runBrief strips --map in its own switch before calling brief.BuildWithOptions",
	"--obsidian":           "wiki: runWiki strips before parseFlags",
	"--output":             "verify: runVerifyCommand → internal/verifycmd command-mode output slice (summary|failures|tail:N|lines:A-B|full)",
	"--probe":              "agents: runAgents strips before parseFlags",
	"--staged":             "verify: internal/verifycmd parseVerifyFlags (staged-package scope, the pre-commit hook tier)",
	"--tail":               "audit: internal/approvalcli parseFlags (alias for --limit; the N most recent audit entries)",
	"--test":               "apply: parseApplyFlags in cmd_apply.go (scoped go test on modified packages after the build gate)",
	"--watch":              "precache: runPrecache strips before parseFlags",
}

// usageProseArtifacts is intentionally gone. The tokens it allowlisted
// ("--cached;", "(--apply)", "--short/--full)", ...) were prose glued to
// flag mentions by the Fields-based extraction. They were reworded out of
// the usage strings in the 2026-10-02 drift-fix pass — each became a bare
// token of a real flag (deduped into the documented set) or was reworded
// away — so the parser-oracle gate no longer needs an artifact allowlist:
// every token a usage string yields must parse through parseFlags or be a
// handler-read flag.

// TestUsageFlagTokensKnownToParserOrHandler is the parser-oracle gate: every
// "--flag" token a usage string documents must be either accepted by the
// shared parseFlags (flags.go) or explicitly allowlisted as a handler-read
// flag or a known prose artifact. A usage string can therefore never
// advertise a flag that the CLI rejects with "unknown flag" — the strongest
// content check feasible without a per-command flag registry (none exists:
// parseFlags is the single unified parser, so per-command ground truth is
// only derivable for the handler-read families).
func TestUsageFlagTokensKnownToParserOrHandler(t *testing.T) {
	for _, name := range sortedCommandKeys() {
		for _, tok := range usageFlagTokens(commandTable[name].usage) {
			if _, _, err := parseFlags([]string{tok}); err == nil {
				continue // shared parser accepts it
			}
			if _, ok := handlerReadUsageFlags[tok]; ok {
				continue // real flag read by a handler outside parseFlags
			}
			t.Errorf("command %q: usage documents flag %q which parseFlags rejects and no handler reads it — fix the usage string, or add it to handlerReadUsageFlags only if a handler really parses it", name, tok)
		}
	}
}
