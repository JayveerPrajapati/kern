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
	"agent-message":         {"--to", "--from", "--task"},
	"agents":                {"--probe", "--json", "--root"},
	"analyze":               {"--lens", "--profile", "--root", "--task"},
	"approve":               {"--approver", "--reason", "--reject", "--root"},
	"arch":                  {"--json", "--root"},
	"artifacts":             {"--json", "--root"},
	"ast":                   {"--all", "--root"},
	"audit":                 {"--json", "--root"},
	"bench":                 {"--json", "--root"},
	"bridges":               {"--json", "--root"},
	"ast-transform":         {"--file", "--target", "--field", "--field-type", "--tag", "--apply", "--root"},
	"authorize-context":     {"--agent", "--task", "--deny-path", "--symbol", "--root", "--json"},
	"autonomy":              {"--level", "--root"},
	"blueprint":             {"--blocking"},
	"brief":                 {"--root"},
	"buddy":                 {"--root"},
	"budget":                {"--max", "--mode", "--file", "--symbol", "--query", "--max-tokens", "--root", "--json"},
	"build":                 {"--dir", "--session"},
	"cache":                 {"--dry-run"},
	"calibrate":             {"--root", "--thresholds"},
	"changes":               {"--file", "--json", "--lens", "--max", "--profile", "--range", "--root", "--runtime"},
	"check":                 {"--all", "--ci", "--fast", "--format", "--json", "--repo", "--require-kern", "--source", "--staged"},
	"check-draft":           {"--file", "--lang", "--root"},
	"churn":                 {"--json", "--range", "--root"},
	"ci":                    {"--base", "--head", "--receipt"},
	"cochange":              {"--json", "--range", "--root"},
	"commit":                {"--all", "--dry-run", "--message"},
	"commitmsg":             {"--range", "--root", "--staged", "--subject"},
	"communities":           {"--full", "--json", "--root"},
	"compact":               {"--root", "--tier", "--etag"},
	"compose":               {"--pipeline"},
	"config":                {"--json", "--root"},
	"context":               {"--lens", "--lines", "--profile", "--root", "--etag"},
	"context-envelope":      {"--change", "--mode", "--max-tokens", "--root"},
	"context-watch":         {"--budget", "--format"},
	"correlate":             {"--code", "--root"},
	"cross-repo-impact":     {"--repo", "--root"},
	"cycles":                {"--json", "--root"},
	"dead":                  {"--json", "--root"},
	"delete":                {"--apply", "--force", "--json", "--root"},
	"deploy":                {"--root", "--version"},
	"diff":                  {"--limit", "--session", "--json"},
	"do":                    {"--level", "--mode", "--root"},
	"doc-fetch":             {"--name", "--root"},
	"doc-search":            {"--limit", "--root"},
	"docs":                  {"--limit", "--root", "--semantic"},
	"doctor":                {"--arch-drift", "--calibration", "--json", "--root"},
	"efficiency":            {"--root"},
	"entries":               {"--json", "--pattern", "--root"},
	"entry-points":          {"--pattern", "--root"},
	"entrypoints":           {"--pattern", "--root"},
	"eval":                  {"--max-tokens", "--mode", "--root"},
	"evidence":              {"--full-state", "--restore", "--root", "--out", "--agent-id", "--task", "--sign", "--file", "--url", "--expect-fingerprint"},
	"exec":                  {"--json", "--lang", "--list", "--max", "--stdin", "--timeout"},
	"execute":               {"--root"},
	"explain":               {"--root"},
	"explain-context":       {"--task", "--mode", "--budget", "--json", "--root"},
	"explain-finding":       {"--finding", "--root"},
	"explore":               {"--depth", "--explain", "--json", "--max", "--min-confidence", "--root", "--etag"},
	"fingerprint":           {"--file", "--json", "--root"},
	"fit-context":           {"--mode", "--file", "--symbol", "--query", "--max-tokens", "--root", "--json"},
	"flows":                 {"--json", "--root"},
	"fragility":             {"--target", "--commits", "--min-fixes", "--limit", "--root", "--json"},
	"fts":                   {"--json", "--limit", "--root"},
	"fw-trace":              {"--root", "--json"},
	"gen-catalog":           {"--root"},
	"gen-contracts":         {"--root"},
	"gen-docs":              {"--doc", "--root"},
	"graph":                 {"--cypher", "--entities", "--graphml", "--html", "--json", "--limit", "--max-tokens", "--mermaid", "--min-confidence", "--one-line", "--out", "--root"},
	"guard":                 {"--task", "--agent-id", "--file", "--json", "--precision", "--range", "--sarif", "--threshold"},
	"heal":                  {"--force", "--yes", "--llm", "--task"},
	"hook":                  {"--range", "--global"},
	"host":                  {"--check", "--dry-run", "--root", "--task", "--uninstall"},
	"hubs":                  {"--bridges-only", "--json", "--root"},
	"impact":                {"--json", "--precision", "--risk", "--lens", "--root", "--runtime"},
	"incident":              {"--json", "--correlate", "--runbook", "--list-playbooks", "--root"},
	"index":                 {"--force", "--json", "--root", "--status", "--strict", "--update"},
	"inherits":              {"--json", "--root"},
	"install":               {"--global"},
	"larges":                {"--json", "--root"},
	"learn":                 {"--root"},
	"lock":                  {"--hold", "--wait", "--timeout", "--root"},
	"log":                   {"--kind", "--context-after", "--context-before", "--profile", "--root"},
	"loop":                  {"--level", "--mode", "--schedule", "--root"},
	"lsp":                   {"--root"},
	"lsp-bridge":            {"--file", "--line", "--column", "--action", "--server-cmd", "--root", "--json"},
	"mask":                  {"--names"},
	"mcp":                   {"--http", "--tls-cert", "--tls-key"},
	"memory":                {"--clear", "--json", "--limit", "--root"},
	"memory-ranked":         {"--half-life", "--root"},
	"meta":                  {"--pipeline"},
	"modernize":             {"--root"},
	"mutate":                {"--files", "--max", "--dry-run", "--cmd", "--min-score", "--root", "--json"},
	"near":                  {"--depth", "--json", "--max", "--root"},
	"onboard":               {"--root"},
	"orchestrate":           {"--change", "--max-tokens", "--mode", "--root", "--with-skill"},
	"org":                   {"--project"},
	"pack":                  {"--fold", "--graph", "--max-tokens", "--no-instructions", "--out", "--tier"},
	"path":                  {"--from", "--to", "--json", "--min-confidence", "--root"},
	"optimize":              {"--attach", "--cache", "--fewshot", "--kind", "--llm", "--mask", "--model", "--names", "--session"},
	"plan":                  {"--json", "--root", "--task"},
	"policy":                {"--merge", "--file", "--json", "--root"},
	"precache":              {"--watch", "--once", "--interval"},
	"preview":               {"--attach", "--cache", "--fewshot", "--kind", "--llm", "--mask", "--model", "--names", "--session"},
	"probe":                 {"--json", "--max", "--min-confidence"},
	"prompt":                {"--file", "--schema", "--task"},
	"prompt-fill":           {"--template", "--file", "--task"},
	"prose":                 {"--limit", "--root"},
	"recall":                {"--limit"},
	"refactor":              {"--edits", "--cmd", "--apply", "--root", "--json"},
	"refactor-transaction":  {"--edits", "--cmd", "--apply", "--root", "--json"},
	"register-host-sampler": {"--key", "--timeout", "--model"},
	"reject":                {"--approver", "--reason", "--root"},
	"remember":              {"--root"},
	"rename":                {"--apply", "--force", "--json", "--root"},
	"repair-diagnostics":    {"--compiler-output", "--apply", "--root", "--json"},
	"repair-guidance":       {"--finding", "--root"},
	"resolve":               {"--json", "--level", "--max-tokens", "--root"},
	"retrieve":              {"--symbol", "--depth", "--json", "--level", "--limit", "--lines", "--max", "--max-tokens", "--query", "--root", "--task-type", "--etag"},
	"review":                {"--file", "--json", "--lens", "--max", "--profile", "--range", "--root", "--runtime"},
	"risk":                  {"--lens", "--root"},
	"run":                   {"--level", "--root"},
	"sandbox":               {"--", "--force", "--json"},
	"schema":                {"--schema"},
	"search":                {"--json", "--limit", "--repos", "--root", "--semantic"},
	"sec":                   {"--engine", "--json", "--root", "--severity"},
	"security":              {"--engine", "--json", "--root", "--severity"},
	"semantic-merge":        {"--base", "--local", "--remote", "--file", "--apply", "--json", "--root"},
	"semcache":              {"--json"},
	"setup":                 {"--agents", "--agents-md", "--check", "--detect", "--global", "--global-rules", "--root", "--verify"},
	"serve":                 {"--addr", "--enterprise", "--project", "--root"},
	"simulate":              {"--json", "--root"},
	"snapshot":              {"--out", "--symbol", "--limit", "--verify", "--strict", "--format", "--max-tokens", "--root"},
	"stats":                 {"--days", "--session", "--json", "--by-tool", "--by-agent"},
	"status":                {"--json"},
	"surprising":            {"--json", "--root"},
	"swap":                  {"--max", "--mode"},
	"synthesize-test":       {"--auto-gap", "--apply", "--file", "--json", "--root", "--sinks", "--target"},
	"taint":                 {"--file", "--generate", "--range", "--root"},
	"task":                  {"--root"},
	"tasks":                 {"--root"},
	"team":                  {"--root"},
	"terse":                 {"--mode", "--max"},
	"tokens":                {"--bpe"},
	"trace":                 {"--json", "--limit"},
	"twin":                  {"--root"},
	"test-gaps":             {"--json", "--root"},
	"testgaps":              {"--json", "--root"},
	"udiff":                 {"--compact", "--out", "--root"},
	"ui":                    {"--addr", "--enterprise", "--project", "--root"},
	"unlock":                {"--root"},
	"update":                {"--dry-run", "--force", "--pin", "--channel"},
	"validate":              {"--cmd", "--json", "--root"},
	"validate-proposed":     {"--files", "--root", "--source"},
	"verify":                {"--cve", "--license", "--secrets", "--types", "--short", "--full", "--eval", "--json", "--root", "--scan", "--skill", "--verify-pipeline", "--verify-silent", "--verify-token-reduction"},
	"verify-receipt":        {"--receipt-id", "--repo", "--json", "--sarif", "--in-toto", "--check-diff"},
	"walk":                  {"--depth", "--json", "--max", "--root"},
	"web":                   {"--addr", "--enterprise", "--project", "--root"},
	"what-if":               {"--json", "--root"},
	"why":                   {"--json", "--min-confidence", "--root"},
	"wiki":                  {"--out", "--obsidian", "--root"},
	"workflow":              {"--root", "--task"},
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
	"--":             "sandbox: command separator (kern sandbox [root] -- <command...>); parseFlags accepts it as a positional",
	"--blocking":     "blueprint/diff-gate: bpcli.RunDiffGate",
	"--check-diff":   "verify-receipt: bpcli parseVerifyReceiptFlags",
	"--doc":          "gen-docs: runGenDocsImpl own switch (catalog|contracts|site)",
	"--fast":         "check: bpcli.RunCheckAndReport",
	"--files":        "validate-proposed + mutate: runBlueprintToolCLI / runMutationTest extractListFlag",
	"--finding":      "explain-finding + repair-guidance: runBlueprintToolCLI payload flag",
	"--head":         "ci: bpcli.RunCI",
	"--in-toto":      "verify-receipt: bpcli parseVerifyReceiptFlags",
	"--list":         "exec: runExec strips --list before parseFlags (kern exec --list lists runtimes; accepted in any position)",
	"--min-score":    "mutate: runMutationTest extractFloatFlag",
	"--obsidian":     "wiki: runWiki strips before parseFlags",
	"--probe":        "agents: runAgents strips before parseFlags",
	"--receipt":      "ci: bpcli.RunCI",
	"--receipt-id":   "verify-receipt: bpcli parseVerifyReceiptFlags",
	"--require-kern": "check: bpcli.RunCheckAndReport",
	"--source":       "validate-proposed: runBlueprintToolCLI",
	"--watch":        "precache: runPrecache strips before parseFlags",
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
// only derivable for the handler-read families, which the blueprint test
// below pins).
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

// blueprintCommands pins the flags runBlueprintToolCLI (cmd_blueprint_tools.go)
// actually reads: --root/--source/--files/--finding, with --files vs
// --finding depending on the payload the command builds. These are the
// commands whose usage strings are LOAD-BEARING parse input — expectedFlags
// derives the "expected: ..." list in the NL-positional error from them.
var blueprintCommands = map[string][]string{
	"validate-proposed": {"--files", "--root", "--source"},
	"explain-finding":   {"--finding", "--root"},
	"repair-guidance":   {"--finding", "--root"},
}

// TestUsageBlueprintToolFlagsMatchExpectedFlags pins the blueprint-tools
// family: each usage string must keep a NON-EMPTY flag set (never triggering
// expectedFlags' hardcoded empty-usage fallback) that matches the exact set
// expectedFlags itself derives — the replica here and the load-bearing
// function must agree on the same strings.
func TestUsageBlueprintToolFlagsMatchExpectedFlags(t *testing.T) {
	for name, want := range blueprintCommands {
		e, ok := commandTable[name]
		if !ok {
			t.Errorf("blueprint command %q missing from commandTable", name)
			continue
		}
		got := usageFlagTokens(e.usage)
		if len(got) == 0 {
			t.Errorf("blueprint command %q: usage documents no flags — expectedFlags would fall back to its generic string, breaking the load-bearing NL-positional error", name)
			continue
		}
		if !equalStrings(got, want) {
			t.Errorf("blueprint command %q: usage-derived flags %v != pinned handler flags %v", name, got, want)
		}
		// The load-bearing function must agree with the replica and must not
		// hit its empty-usage fallback on these strings.
		if ef := expectedFlags(e.usage); ef != strings.Join(got, ", ") {
			t.Errorf("blueprint command %q: usageFlagTokens replica disagrees with expectedFlags: replica=%q expectedFlags=%q", name, strings.Join(got, ", "), ef)
		}
	}
}
