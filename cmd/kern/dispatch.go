package main

import (
	"fmt"
	"os"
	"slices"
	"strings"
)

// resolveCommandAndFlags extracts the subcommand and its remaining arguments
// from os.Args. It also handles the two pre-dispatch exits: no command at all
// (prints usage, exit 2) and a `--help`/`-h` request (prints usage, exit 0).
func resolveCommandAndFlags() (cmd string, rest []string) {
	if len(os.Args) < 2 {
		// No command: fall through to dispatch's unknown-command default, which
		// prints usage and returns exit code 2 on the normal sentinel path so
		// main() persists metrics before os.Exit (a direct os.Exit would skip it).
		return "", nil
	}
	cmd = os.Args[1]
	rest = os.Args[2:]

	if cmd == "--all" || ((cmd == "help" || cmd == "--help" || cmd == "-h") && hasFlag(rest, "--all")) {
		usageAll(hasFlag(rest, "--flat"))
		os.Exit(0)
	}

	if cmd == "help" {
		if len(rest) > 0 && rest[0] != "--help" && rest[0] != "-h" {
			// A category name keeps the grouped listing (backwards
			// compatible); a registered command shows its own help (the
			// same text as `kern <cmd> --help`); anything else is an
			// unknown help topic — a hard error with a nearest-match
			// suggestion, never a silent main-help dump.
			if !printCategoryHelp(rest[0]) && !printCommandHelp(rest[0]) {
				printUnknownCommand(rest[0])
				os.Exit(2)
			}
			// A category listing or a command's own help was printed (the
			// category path exits inside printCategoryHelp) — that is the
			// whole answer; do not fall through to the global usage.
			os.Exit(0)
		}
		usage()
		os.Exit(0)
	}

	// `--help`/`-h` on any subcommand (or on kern itself) prints the
	// per-command help and exits 0 instead of being dispatched to a
	// subcommand handler. Only genuinely unknown commands (including bare
	// `kern --help`) fall back to the global usage text — still exit 0.
	if cmd == "--help" || cmd == "-h" || hasFlag(rest, "--help") || hasFlag(rest, "-h") {
		if !printCommandHelp(cmd) {
			usage()
		}
		os.Exit(0)
	}
	return cmd, rest
}

// commandHelp holds a one-line description per subcommand, used by
// printCommandHelp for `kern <cmd> --help`. Aliases share their primary
// command's description.

// mcpCLIAlias maps every MCP tool whose CLI command name differs from its
// kern_* suffix to the dispatch case that serves it. Tools whose suffix IS
// a dispatch case (kern_search -> search) need no entry here. The parity
// test TestMCPToolsReachableFromCLI enforces that every registered MCP tool
// is covered either by a same-name dispatch case or by an alias entry, and
// that every alias target is a real dispatch case.
var mcpCLIAlias = map[string]string{
	"kern_agents":                "team",
	"kern_llm_providers":         "agents",
	"kern_register_host_sampler": "register-host-sampler",
	"kern_validate_staged":       "diff-gate",
	"kern_validate_proposed":     "validate-proposed",
	"kern_explain_finding":       "explain-finding",
	"kern_repair_guidance":       "repair-guidance",
	"kern_fetch_raw_anchor":      "anchor",
	"kern_ast_search":            "ast",
	"kern_authorize_context":     "authorize-context",
	"kern_compact_file":          "compact",
	"kern_context_budget":        "budget",
	"kern_context_envelope":      "context-envelope",
	"kern_plan_context":          "explain-context",
	"kern_orchestrate":           "orchestrate",
	"kern_skill":                 "skills",
	"kern_agent_message":         "agent-message",
	"kern_agent_interrupt":       "agent-interrupt",
	"kern_mcp_call":              "mcp-client",
	"kern_diff_files":            "udiff",
	"kern_doc_index":             "docs",
	"kern_entry_points":          "entry-points",
	"kern_fts_search":            "fts",
	"kern_guard_check":           "guard",
	"kern_lock_status":           "status",
	"kern_mask_pii":              "mask",
	"kern_memory_add":            "remember",
	"kern_memory_list":           "memory",
	"kern_memory_recall":         "recall",
	"kern_optimize_log":          "log",
	"kern_optimize_output":       "terse",
	"kern_optimize_prompt":       "optimize",
	"kern_project_map":           "project",
	"kern_repo_search":           "repos",
	"kern_safe_delete":           "delete",
	"kern_schema_validate":       "schema",
	"kern_security":              "sec",
	"kern_test_gaps":             "testgaps",
	"kern_usage_guide":           "guide",
	"kern_verify_output":         "verify",
	"kern_what_if":               "impact",
	"kern_check_draft":           "check-draft",
	"kern_taint":                 "taint",
	"kern_pre_edit":              "pre-edit",
	"kern_prompt_fill":           "prompt-fill",
	"kern_semantic_diff":         "semantic-diff",
	"kern_evidence_anchor":       "evidence-anchor",
	"kern_context_watch":         "context-watch",
	"kern_agent_fingerprint":     "agent-fingerprint",
	"kern_cross_repo_impact":     "cross-repo-impact",
	"kern_memory_ranked":         "memory-ranked",
	"kern_policy_dsl":            "policy-dsl",
	"kern_agent_coordination":    "agent-coordination",
	"kern_agent_role_rbac":       "agent-role-rbac",
	"kern_ast_transform":         "ast-transform",
	"kern_semantic_merge":        "semantic-merge",
	"kern_synthesize_test":       "synthesize-test",
	"kern_org_projects":          "org",
	"kern_org_agents":            "org",
	"kern_org_teams":             "org",
	"kern_org_memory":            "org",
	"kern_org_tasks":             "org",
	"kern_org_search":            "org",
	"kern_org_audit":             "org",
	"kern_org_user":              "org",
	"kern_fit_context":           "fit-context",
	"kern_refactor_transaction":  "refactor-transaction",
	"kern_repair_diagnostics":    "repair-diagnostics",
	"kern_lsp_bridge":            "lsp-bridge",
	"kern_fw_trace":              "fw-trace",
	"kern_mutation_test":         "mutate",
	"kern_fragility_hotspots":    "fragility",
}

// printCommandHelp prints the one-line help for a subcommand and returns
// true. Commands without a one-liner (help empty) still get their usage
// text — a bare `kern <cmd>` line plus the entry's usage block — instead of
// falling back to the global usage text. It returns false when cmd is not a
// registered command, so the caller decides between the global usage fallback
// (`kern --help`) and an unknown-topic error (`kern help <unknown>`).
func printCommandHelp(cmd string) bool {
	if e, ok := commandTable[cmd]; ok {
		if e.help != "" {
			fmt.Printf("kern %s — %s\n", cmd, e.help)
		} else {
			fmt.Printf("kern %s [flags]\n", cmd)
		}
		if e.usage != "" {
			fmt.Println(e.usage)
		}
		return true
	}
	return false
}

// printCategoryHelp handles `kern help <category>`: when the argument names
// a command category (as shown by the grouped `kern --all` listing), it
// lists that category's commands (alphabetical, with their one-liners) and
// exits 0. Returns false when the argument is not a category, so the caller
// falls through to per-command help.
func printCategoryHelp(cat string) bool {
	var names []string
	for name, e := range commandTable {
		// Alias spellings dispatch but are skipped from the category listing
		// (N4): the kebab/snake duplicates must not appear twice.
		if e.alias {
			continue
		}
		if e.category == cat {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return false
	}
	slices.Sort(names)
	fmt.Printf("[%s]\n", cat)
	for _, name := range names {
		e := commandTable[name]
		line := "  kern " + name
		if desc := commandDescription(e, name); desc != "" {
			line += "  " + desc
		}
		fmt.Println(line)
	}
	os.Exit(0)
	return true
}

// dispatchCommand routes a parsed subcommand to its handler and returns the
// process exit code. main() converts that into a single os.Exit call so every
// command shares the same shutdown path.
func dispatchCommand(cmd string, rest []string) int {
	if e, ok := commandTable[cmd]; ok {
		return e.run(cmd, rest)
	}
	if cmd == "" {
		// Bare `kern` (no command): keep the full usage dump so the
		// no-command default stays self-documenting.
		usage()
		return 2
	}
	printUnknownCommand(cmd)
	return 2
}

// printUnknownCommand writes a one-line unknown-command error to stderr with
// the nearest-match suggestion (when one exists) and a pointer to the full
// catalog. The caller owns the exit code (2 for usage errors). This is the
// replacement for the old behavior of dumping the entire 30-line usage banner
// on a mistyped command.
func printUnknownCommand(cmd string) {
	fmt.Fprintf(os.Stderr, "kern: unknown command %q\n", cmd)
	if sugg := suggestCommands(cmd); len(sugg) > 0 {
		fmt.Fprintf(os.Stderr, "\ndid you mean: %s\n", strings.Join(sugg, ", "))
	}
	fmt.Fprintln(os.Stderr, "run 'kern --all' for the full catalog")
}

// suggestCommands returns up to 3 command names from commandTable that best
// match query, in descending confidence order: exact-prefix matches first
// (the strongest typo signal, e.g. "searc" → "search"), then
// case-insensitive substring matches, then close edit-distance (Levenshtein)
// matches. It returns nil when nothing is close enough, so the unknown-command
// error stays a one-liner instead of guessing wildly.
func suggestCommands(query string) []string {
	if query == "" {
		return nil
	}
	q := strings.ToLower(query)
	var prefixes, substrings, fuzzy []string
	for name := range commandTable {
		n := strings.ToLower(name)
		switch {
		case strings.HasPrefix(n, q):
			prefixes = append(prefixes, name)
		case strings.Contains(n, q):
			substrings = append(substrings, name)
		case editDistance(q, n) <= max(2, len(q)/3):
			fuzzy = append(fuzzy, name)
		}
	}
	slices.Sort(prefixes)
	slices.Sort(substrings)
	// Fuzzy candidates sorted by edit distance so the closest match leads;
	// equal distances tie-break alphabetically for stable output across runs.
	slices.SortFunc(fuzzy, func(a, b string) int {
		da := editDistance(q, strings.ToLower(a))
		db := editDistance(q, strings.ToLower(b))
		if da != db {
			return da - db
		}
		return strings.Compare(a, b)
	})
	var out []string
	seen := map[string]bool{}
	for _, name := range append(append(prefixes, substrings...), fuzzy...) {
		if seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
		if len(out) >= 3 {
			break
		}
	}
	return out
}

// editDistance returns the Levenshtein edit distance between a and b
// (insertions, deletions and substitutions each cost 1). Used by
// suggestCommands to rank typo candidates.
func editDistance(a, b string) int {
	la, lb := len(a), len(b)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	prev := make([]int, lb+1)
	cur := make([]int, lb+1)
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		cur[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[lb]
}
