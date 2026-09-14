package main

import (
	"fmt"
	"os"
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
		usageAll()
		os.Exit(0)
	}

	if cmd == "help" {
		if len(rest) > 0 && rest[0] != "--help" && rest[0] != "-h" {
			printCommandHelp(rest[0])
		}
		usage()
		os.Exit(0)
	}

	// `--help`/`-h` on any subcommand (or on kern itself) prints the
	// per-command help and exits 0 instead of being dispatched to a
	// subcommand handler.
	if cmd == "--help" || cmd == "-h" || hasFlag(rest, "--help") || hasFlag(rest, "-h") {
		printCommandHelp(cmd)
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
	"kern_agents":             "team",
	"kern_llm_providers":      "agents",
	"kern_validate_staged":    "diff-gate",
	"kern_validate_proposed":  "validate-proposed",
	"kern_explain_finding":    "explain-finding",
	"kern_repair_guidance":    "repair-guidance",
	"kern_ast_search":         "ast",
	"kern_authorize_context":  "authorize-context",
	"kern_code_graph":         "graph",
	"kern_compact_file":       "compact",
	"kern_context_budget":     "budget",
	"kern_context_envelope":   "context-envelope",
	"kern_plan_context":       "explain-context",
	"kern_orchestrate":        "orchestrate",
	"kern_skill":              "skills",
	"kern_agent_message":      "agent-message",
	"kern_agent_interrupt":    "agent-interrupt",
	"kern_mcp_call":           "mcp-client",
	"kern_note":               "note",
	"kern_diff_files":         "udiff",
	"kern_doc_index":          "docs",
	"kern_entry_points":       "entries",
	"kern_fts_search":         "fts",
	"kern_guard_check":        "guard",
	"kern_lock_status":        "status",
	"kern_mask_pii":           "mask",
	"kern_memory_add":         "remember",
	"kern_memory_list":        "memory",
	"kern_memory_recall":      "recall",
	"kern_optimize_log":       "log",
	"kern_optimize_output":    "terse",
	"kern_optimize_prompt":    "optimize",
	"kern_project_map":        "project",
	"kern_repo_search":        "repos",
	"kern_run_build":          "build",
	"kern_safe_delete":        "delete",
	"kern_schema_validate":    "schema",
	"kern_security":           "sec",
	"kern_test_gaps":          "testgaps",
	"kern_usage_guide":        "guide",
	"kern_verify_output":      "verify",
	"kern_what_if":            "what-if",
	"kern_check_draft":        "check-draft",
	"kern_taint":              "taint",
	"kern_pre_edit":           "pre-edit",
	"kern_prompt_fill":        "prompt-fill",
	"kern_semantic_diff":      "semantic-diff",
	"kern_evidence_anchor":    "evidence-anchor",
	"kern_context_watch":      "context-watch",
	"kern_agent_fingerprint":  "agent-fingerprint",
	"kern_cross_repo_impact":  "cross-repo-impact",
	"kern_memory_ranked":      "memory-ranked",
	"kern_policy_dsl":         "policy-dsl",
	"kern_agent_coordination": "agent-coordination",
	"kern_agent_role_rbac":    "agent-role-rbac",
	"kern_ast_transform":      "ast-transform",
	"kern_semantic_merge":     "semantic-merge",
	"kern_synthesize_test":    "synthesize-test",
	"kern_org_projects":       "org",
	"kern_org_agents":         "org",
	"kern_org_teams":          "org",
	"kern_org_memory":         "org",
	"kern_org_tasks":          "org",
	"kern_org_search":         "org",
	"kern_org_audit":          "org",
	"kern_fit_context":         "fit-context",
	"kern_refactor_transaction": "refactor-transaction",
	"kern_repair_diagnostics":  "repair-diagnostics",
	"kern_lsp_bridge":          "lsp-bridge",
	"kern_fw_trace":            "fw-trace",
	"kern_mutation_test":       "mutate",
	"kern_fragility_hotspots":  "fragility",
}

// printCommandHelp prints the one-line help for a subcommand and exits 0.
// Unknown commands (including bare `kern --help`) fall back to the global
// usage text.
func printCommandHelp(cmd string) {
	if e, ok := commandTable[cmd]; ok && e.help != "" {
		fmt.Printf("kern %s — %s\n", cmd, e.help)
		if e.usage != "" {
			fmt.Println(e.usage)
		}
		os.Exit(0)
	}
	usage()
	os.Exit(0)
}

// dispatchCommand routes a parsed subcommand to its handler and returns the
// process exit code. main() converts that into a single os.Exit call so every
// command shares the same shutdown path.
func dispatchCommand(cmd string, rest []string) int {
	if e, ok := commandTable[cmd]; ok {
		return e.run(cmd, rest)
	}
	usage()
	return 2
}
