package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/cliflags"
	"github.com/JayveerPrajapati/kern/internal/mcp"
	"github.com/JayveerPrajapati/kern/internal/mcpcli"
)

func runMCPTool(toolName string, args map[string]any) {
	out, err := callTool(toolName, args)
	if err != nil {
		fatal("%s: %v — see kern doctor for diagnostics", toolName, err)
	}
	fmt.Println(out)
}

// callTool invokes an MCP tool against a fresh in-process server and returns
// its raw output.
func callTool(toolName string, args map[string]any) (string, error) {
	srv := mcp.NewServer(os.Stdin, os.Stdout)
	return srv.CallTool(context.Background(), toolName, args)
}

func runComposeCore(rest []string) int {
	mcpcli.ComposeRunner = composeCLIRunner
	return mcpcli.RunCompose(rest)
}

// composeCLIRunner executes a compose pipeline step through the same loopback
// server the MCP-mirror commands use (the mcpcli package cannot construct
// the server itself — the mcp package imports the leaves it calls).
func composeCLIRunner(ctx context.Context, id, name string, args map[string]any) (string, error) {
	return callTool(name, args)
}

// runComposeCore is the thin CLI shell for the compose pipeline engine
// (workstream 3.6-A — MCP-mirror extraction): the business logic lives in
// internal/mcpcli, and runMeta (`kern meta --pipeline`) delegates here.
// The loopback tool runner stays in cmd/kern — see composeCLIRunner.
//
// runPreEdit, runPromptFill, runSemanticDiff, runEvidenceAnchor,
// runContextWatch, runAstTransform and runSemanticMerge are likewise thin
// delegates of the entry points in internal/mcpcli (a top-level sibling of
// the mcp subtree — the monolith's 19,000 LOC ratchet forbids landing the
// mirrors inside internal/mcp/<leaf>).

func runPreEdit(rest []string) int {
	return mcpcli.RunPreEdit(rest)
}

func runPromptFill(rest []string) int {
	return mcpcli.RunPromptFill(rest)
}

func runSemanticDiff(rest []string) int {
	return mcpcli.RunSemanticDiff(rest)
}

func runEvidenceAnchor(rest []string) int {
	return mcpcli.RunEvidenceAnchor(rest)
}

func runContextWatch(rest []string) int {
	return mcpcli.RunContextWatch(rest)
}

func runAgentFingerprint(rest []string) {
	f, pos := parseFlagsOrDie(rest)

	args := map[string]any{"format": f.format, "action": "fingerprint"}
	if f.agent != "" {
		args["agent_id"] = f.agent
	} else if len(pos) > 0 {
		args["agent_id"] = pos[0]
	}
	runMCPTool("kern_agent", args)
}

func runExplain(rest []string) {
	f, pos, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v\nusage: kern explain <target-symbol-or-file> [--root ROOT]", err)
	}

	t := f.target
	if t == "" && len(pos) > 0 {
		t = pos[0]
	}
	// QA: Go's flag package stops parsing at the first positional, so
	// `kern explain <sym> --nonsense` previously swallowed the unknown flag
	// (exit 0). The unified parser rejects unknown flags everywhere (before
	// AND after positionals), so the trailing-flag sweep is no longer needed.
	if t == "" {
		fatalUsage("usage: kern explain <target-symbol-or-file> [--root ROOT]")
	}
	runMCPTool("kern_explain", map[string]any{
		"target": t,
		"root":   f.root,
	})
}

func runCrossRepoImpact(rest []string) {
	f, pos, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v\nusage: kern cross-repo-impact <symbol> [--repo <path>]... [--root ROOT]", err)
	}

	t := f.target
	if t == "" {
		t = f.symbol
	}
	if t == "" && len(pos) > 0 {
		t = pos[0]
	}
	// QA: Go's flag package stops parsing at the first positional, so
	// `kern cross-repo-impact <sym> --nonsense` previously swallowed the
	// unknown flag (exit 0). The unified parser rejects unknown flags
	// everywhere, so the trailing-flag sweep is no longer needed.
	if t == "" {
		fatalUsage("usage: kern cross-repo-impact <symbol> [--repo <path>]... [--root ROOT]")
	}
	for _, rp := range f.repoPaths {
		if _, err := os.Stat(rp); err != nil {
			fatal("cross-repo-impact: linked repo not found: %s", rp)
		}
	}
	runMCPTool("kern_cross_repo_impact", map[string]any{
		"target_symbol": t,
		"linked_repos":  f.repoPaths,
		"root":          f.root,
	})
}

func runMemoryRanked(rest []string) {
	f, pos := parseFlagsOrDie(rest)

	p := f.prompt
	if p == "" && len(pos) > 0 {
		p = strings.Join(pos, " ")
	}
	if p == "" {
		fatalUsage("usage: kern memory-ranked <prompt> [-k 5] [--half-life 7.0] [--root ROOT]")
	}
	runMCPTool("kern_memory", map[string]any{
		"action":         "ranked",
		"prompt":         p,
		"k":              f.k,
		"half_life_days": f.halfLife,
		"root":           f.root,
	})
}

func runPolicyDSL(rest []string) {
	f, pos, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v\nusage: kern policy_dsl [--policy TEXT|FILE] [--diff STR] [--file F]...", err)
	}

	if f.policy == "" && len(pos) > 0 {
		// Positional DSL text (e.g. `kern policy_dsl 'allow agent=...'`)
		// was previously silently dropped.
		f.policy = strings.Join(pos, " ")
	}
	args := map[string]any{"root": f.root}
	if f.policy != "" {
		args["policy"] = f.policy
	}
	if f.diff != "" {
		args["diff"] = f.diff
	}
	if len(f.files) > 0 {
		args["files"] = f.files
	}
	// Run the tool directly so the CLI can mirror the verdict in its exit
	// code. A BLOCKED verdict is a successful tool call (the policy ran) but
	// the change must not pass: rc=1 — fail closed, never a vacuous ALLOWED
	// (F7). Missing policy files already surface as tool errors (rc=1).
	out, err := callTool("kern_policy_dsl", args)
	if err != nil {
		fatal("policy-dsl: %v — see kern doctor for diagnostics", err)
	}
	fmt.Println(out)
	if strings.Contains(out, "BLOCKED") {
		fatal("policy-dsl: verdict BLOCKED — see output above")
	}
}

func runAgentCoordination(rest []string) {
	f, pos := parseFlagsOrDie(rest)

	act := f.action
	if act == "" {
		act = "status"
	}
	if len(pos) > 0 {
		act = pos[0]
	}
	args := map[string]any{
		"action":       "coordination",
		"inner_action": act,
		"agent_id":     f.agent,
		"from_agent":   f.from,
		"to_agent":     f.to,
		"task_id":      f.task,
		"resource":     f.resource,
		"ttl_seconds":  f.ttl,
		"notes":        f.notes,
		"root":         f.root,
	}
	runMCPTool("kern_agent", args)
}

func runAgentRoleRBAC(rest []string) {
	f, pos, err := parseFlags(rest)
	if err != nil {
		fatalUsage("usage: kern agent_role_rbac -tool <name> [-action evaluate] [-agent ID] [-role R]")
	}

	act := f.action
	if len(pos) > 0 {
		act = pos[0]
	}
	if act == "" {
		// Mirrors the MCP tool's resolution: a -tool with no action means
		// evaluate. No action AND no tool is a usage error (exit 2, the
		// documented contract — QA 2026-10-15 finding 9) — previously the
		// forced "evaluate" default failed at the tool boundary with a
		// vague error and exit 1.
		if f.tool != "" {
			act = "evaluate"
		} else {
			fatalUsage("usage: kern agent-role-rbac -tool <name> [-action evaluate] [-agent ID] [-role R]  (or -action roles|assign)")
		}
	}
	args := map[string]any{
		"action":       "rbac",
		"inner_action": act,
		"agent_id":     f.agent,
		"role":         f.role,
		"tool":         f.tool,
		"root":         f.root,
	}
	runMCPTool("kern_agent", args)
}

func runStream(rest []string) {
	f, pos := parseFlagsOrDie(rest)

	act := f.action
	if act == "" {
		act = "status"
	}
	if len(pos) > 0 {
		act = pos[0]
	}
	args := map[string]any{
		"action":         act,
		"channel":        f.channel,
		"payload":        f.payload,
		"chunk_size":     f.chunkSize,
		"progress_token": f.progressToken,
		"percent":        f.percent,
		"message":        f.message,
	}
	runMCPTool("kern_stream", args)
}

func runAstTransform(rest []string) int {
	return mcpcli.RunAstTransform(rest)
}

func runSemanticMerge(rest []string) int {
	return mcpcli.RunSemanticMerge(rest)
}

func runSynthesizeTest(rest []string) {
	f, pos, err := parseFlags(rest)
	if err != nil {
		// cliflags.StdlibFlagErr keeps the pinned "flag provided but not defined"
		// wording (TestRunSynthesizeTestBadFlagExits2); the parse itself is
		// the unified parseFlags.
		fatalUsage("flags: %v\nusage: kern synthesize-test [<file>] [--target NAME] [--auto-gap] [--apply] [--json] [--root ROOT]\n  a source is required: a positional file, or use --file or --auto-gap\n  (--target alone does not select a source file)", cliflags.StdlibFlagErr(err))
	}

	tgt := f.target
	file := f.file
	if len(pos) > 0 {
		// The positional used to map only to --target, which alone never
		// selects a source — every documented positional-file invocation
		// failed with "must be provided". Disambiguate by existence: a
		// positional naming an existing file is the source form (the
		// engine resolves it against root); anything else stays a target
		// symbol for backwards compatibility.
		if file == "" {
			if posNamesFile(pos[0], f.root) {
				file = pos[0]
			} else if tgt == "" {
				tgt = pos[0]
			}
		} else if tgt == "" {
			tgt = pos[0]
		}
	}

	format := "text"
	if f.json {
		format = "json"
	}

	args := map[string]any{
		"target":   tgt,
		"file":     file,
		"auto_gap": f.autoGap,
		"apply":    f.apply,
		"format":   format,
		"root":     f.root,
	}
	if f.sinks != "" {
		args["sinks"] = f.sinks
	}
	out, err := callTool("kern_synthesize_test", args)
	if err != nil {
		fatal("kern_synthesize_test: %v", err)
	}
	fmt.Println(out)
	// a REFUSED apply (generated test fails at runtime, or
	// the write is otherwise rejected) is a decided-state / policy outcome —
	// exit 3, not 0, so scripts/CI can detect it without parsing the report.
	// The "apply refused" marker is stable in both text and JSON reports
	// (synthtest sets Message to "apply refused: <reason>").
	if f.apply && strings.Contains(out, "apply refused") {
		fatalPolicy("synthesize-test: apply refused — generated test did not pass validation (see report above)")
	}
}

// posNamesFile reports whether a positional argument names an existing file,
// either as given (CWD-relative) or joined to the project root — the
// synthesize-test engine resolves file paths against root, so both
// spellings count as the source form.
func posNamesFile(p, root string) bool {
	if _, err := os.Stat(p); err == nil {
		return true
	}
	if root != "" && !filepath.IsAbs(p) {
		if _, err := os.Stat(filepath.Join(root, p)); err == nil {
			return true
		}
	}
	return false
}

func runFetchRawAnchor(rest []string) int {
	if len(rest) == 0 {
		fmt.Fprintln(os.Stderr, "kern anchor: anchor_id argument required")
		return 1
	}
	anchorID := rest[0]
	out, err := callTool("kern_fetch_raw_anchor", map[string]any{"anchor_id": anchorID})
	if err != nil {
		fmt.Fprintf(os.Stderr, "kern anchor: %v\n", err)
		return 1
	}
	fmt.Println(out)
	return 0
}
