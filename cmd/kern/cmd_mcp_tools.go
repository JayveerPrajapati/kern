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
// runEvidenceAnchor is the one remaining wired delegate (the
// `evidence-anchor` dispatch row); the other mcpcli entry points have no
// dispatch row and no live caller, so their cmd/kern shells were removed
// (2026-10-21 M4 dead-surface cleanup — the engines stay in internal/mcpcli,
// a top-level sibling of the mcp subtree, if a future surface needs them).

func runEvidenceAnchor(rest []string) int {
	return mcpcli.RunEvidenceAnchor(rest)
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
