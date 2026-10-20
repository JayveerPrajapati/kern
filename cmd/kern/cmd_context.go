// cmd_context.go owns the `kern context`-family command bodies that stayed
// in cmd/kern (project/pack/prompt/swap/doctor) plus the thin delegates for
// the context/lock/guard family, whose implementations moved to
// internal/contextcli (workstream 3.6-A — context/guard family extraction).
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/cliflags"
	"github.com/JayveerPrajapati/kern/internal/code"
	"github.com/JayveerPrajapati/kern/internal/contextcli"
	"github.com/JayveerPrajapati/kern/internal/doctor"
	"github.com/JayveerPrajapati/kern/internal/pack"
	"github.com/JayveerPrajapati/kern/internal/prompt"
	"github.com/JayveerPrajapati/kern/internal/swap"
)

// runContext delegates `kern context` to the internal/contextcli family.
func runContext(rest []string) int { return contextcli.RunContext(rest) }

// runLock delegates `kern lock` to the internal/contextcli family.
func runLock(rest []string) int { return contextcli.RunLock(rest) }

// runUnlock delegates `kern unlock` to the internal/contextcli family.
func runUnlock(rest []string) int { return contextcli.RunUnlock(rest) }

// runStatus delegates `kern status` to the internal/contextcli family.
func runStatus(rest []string) int { return contextcli.RunStatus(rest) }

// runGuard delegates `kern guard` to the internal/contextcli family.
func runGuard(rest []string) int { return contextcli.RunGuard(rest) }

func runProject(rest []string) {
	f, args := parseFlagsOrDie(rest)
	root := "."
	if len(args) > 0 {
		root = args[0]
	}
	if _, err := os.Stat(root); err != nil {
		// A missing/typo root (e.g. `kern project map` treating
		// "map" as a root) must fail loud, not render "Project:
		// map (0 files)" with exit 0 (e2e round 2).
		fatal("Project: %v", err)
	}
	p, err := code.BuildProject(root, f.maxFiles, 200)
	if err != nil {
		fatal("Project: %v", err)
	}
	fmt.Println(p.Render())

}

func runPack(rest []string) {
	f, args := parseFlagsOrDie(rest)
	root := projectRoot(f)
	if f.root == "" && len(args) > 0 {
		root = args[0]
	}
	tier := code.TierFull
	if f.fold {
		tier = code.TierFolded
	} else if f.tier != "" {
		t, terr := code.ParseTier(f.tier)
		if terr != nil {
			fatalUsage("%v", terr)
		}
		tier = t
	}
	if f.graph {
		runPackGraph(root, f)
		return
	}
	if _, err := os.Stat(root); err != nil {
		fatal("pack: %v", err)
	}
	b, err := pack.Build(root, pack.Options{
		MaxTokens:        f.maxTokens,
		SkipInstructions: f.noinstructions,
		Tier:             tier,
	})
	if err != nil {
		fatal("Pack: %v", err)
	}
	// PACK-INTEGRATION (lane R-2): when the pack ran without a token budget
	// and the bundle exceeds the warning threshold, say so and point at
	// --max-tokens so the user can fit a budget. Informational only: rc stays 0.
	if f.maxTokens == 0 && b.TokenCount > pack.PackedTokensWarningThreshold {
		fmt.Fprintf(os.Stderr, "pack: %d tokens packed (unlimited — rerun with --max-tokens N to fit a budget)\n", b.TokenCount)
	}
	if len(b.Files) == 0 && f.maxTokens > 0 {
		// A budget smaller than the tree+instructions block silently packed
		// zero files — say so instead of printing "packed 0 files".
		fmt.Fprintf(os.Stderr, "kern: warning: --max-tokens %d left no room for any file (tree+instructions already %d tokens); raise the budget or drop --max-tokens\n", f.maxTokens, b.TotalTokens)
	}
	out := b.Render()
	if f.out != "" {
		if werr := os.WriteFile(f.out, []byte(out), 0o644); werr != nil {
			fatal("%v", werr)
		}
		fmt.Printf("kern: packed %d files (%d tokens) to %s\n", len(b.Files), b.TotalTokens, f.out)
	} else {
		fmt.Print(out)
	}
}

// runPackGraph implements the --graph branch of `kern pack`: load-or-build
// the index, render the graph-snapshot pack (whole graph, or the neighbourhood
// of --symbol when set), and print it or write it to --out like files mode.
func runPackGraph(root string, f flags) {
	gb, err := pack.BuildGraph(root, pack.Options{
		MaxTokens:   f.maxTokens,
		GraphSymbol: f.symbol,
	})
	if err != nil {
		fatal("Pack: %v", err)
	}
	out := gb.Render()
	if f.out != "" {
		if werr := os.WriteFile(f.out, []byte(out), 0o644); werr != nil {
			fatal("%v", werr)
		}
		mode := "whole graph"
		if gb.Symbol != "" {
			mode = "subgraph " + gb.Symbol
		}
		fmt.Printf("kern: packed graph %s (%d symbols, %d tokens) to %s\n", mode, len(gb.Nodes), gb.TotalTokens, f.out)
	} else {
		fmt.Print(out)
	}
}

func runPrompt(rest []string) {
	f, args := parseFlagsOrDie(rest)
	if len(args) == 0 {
		fatalUsage("usage: kern prompt <template> [--file <file>] [--task TEXT]")
	}
	if args[0] == "list" || args[0] == "--help" || args[0] == "-h" {
		names, err := prompt.List()
		if err != nil {
			fatal("Prompt: %v", err)
		}
		if args[0] != "list" {
			fmt.Println("usage: kern prompt <template> [--file <file>] [--task TEXT]")
			fmt.Println("templates:")
		}
		for _, n := range names {
			fmt.Println("  " + n)
		}
		return
	}
	// L2: "show" is a subcommand, not a template name — `kern prompt show X`
	// renders template X, and `kern prompt show` (no name) is a usage error
	// instead of a misleading "unknown template show" runtime failure.
	if args[0] == "show" {
		if len(args) < 2 {
			fatalUsage("usage: kern prompt show <template>\n  renders one template; 'kern prompt list' lists the available names")
		}
		args = args[1:]
	}
	vars := map[string]string{
		"ROOT":    ".",
		"LANG":    "",
		"MAP":     "",
		"FILE":    f.file,
		"TASK":    f.task,
		"SYMBOLS": "",
	}
	if abs, err := filepath.Abs("."); err == nil {
		vars["ROOT"] = abs
	}
	if p, err := code.BuildProject(".", 0, 200); err == nil {
		vars["MAP"] = p.Render()
		vars["LANG"] = projectLangs()
	}
	if f.file != "" {
		if ctx := fileContext(f.file); ctx != "" {
			vars["FILE"] = f.file + "\n" + ctx
		}
	}
	out, err := prompt.Render(args[0], vars)
	if err != nil {
		// A bare "unknown template" error leaves the user with no
		// idea how to proceed. Enrich it with the available template names
		// and the --file escape hatch for custom templates.
		if strings.Contains(err.Error(), "unknown template") {
			fatal("Prompt: %s", promptUnknownTemplateHint(args[0]))
		}
		fatal("Prompt: %v", err)
	}
	if f.schema != "" {
		sc, serr := loadSchema(f.schema)
		if serr != nil {
			fatal("%v", serr)
		}
		fmt.Print(out)
		fmt.Println()
		fmt.Print(sc.PromptBlock())
		return
	}
	fmt.Print(out)

}

// promptUnknownTemplateHint builds a self-diagnosing error for an unknown
// template name: it lists the bundled template names (the same set
// `kern prompt list` shows) and points at --file <file> for custom templates,
// so the user is not left staring at a bare "unknown template" error.
func promptUnknownTemplateHint(name string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "unknown template %q\n", name)
	if names, err := prompt.List(); err == nil && len(names) > 0 {
		fmt.Fprintln(&b, "available templates (kern prompt list):")
		for _, n := range names {
			fmt.Fprintf(&b, "  %s\n", n)
		}
	}
	fmt.Fprintln(&b, "tip: pass --file <file> to render a custom template file, or run `kern prompt list`")
	return b.String()
}

func runSwap(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	// Validate --mode against the modes the help text declares before doing
	// any work: an unknown mode is a usage error (rc=2), never a silent
	// fall-through to the default fit path.
	switch f.mode {
	case "", "fit", "summary", "expand":
	default:
		fatalUsage("swap: unknown --mode %q (valid modes: fit, summary, expand)", f.mode)
	}
	root := "."
	if len(args) > 0 && isDir(args[0]) {
		root = args[0]
		args = args[1:]
	}
	var b []byte
	if len(args) > 0 && args[0] == "-" {
		b, err = cliflags.ReadStdin()
	} else if len(args) > 0 {
		b, err = os.ReadFile(args[0])
	} else {
		b, err = cliflags.ReadStdin()
	}
	if err != nil {
		fatal("Swap: %v", err)
	}
	text := string(b)
	switch f.mode {
	case "expand":
		fmt.Print(swap.ExpandMode(text, root))
	case "summary":
		fmt.Print(swap.SummaryMode(text, root))
	default:
		// fit (default): budget-fit fenced-block replacement. When --max is
		// omitted the budget defaults to 4000 tokens, mirroring `kern budget`,
		// so a bare `kern swap` replaces blocks once the document is over it.
		budget := f.max
		if budget <= 0 {
			budget = 4000
		}
		out, fits, swapped := swap.FitBlocks(text, root, budget)
		if len(swapped) > 0 {
			fmt.Fprintf(os.Stderr, "kern: swapped %d fenced block(s) to fit budget %d: %s\n", len(swapped), budget, strings.Join(swapped, ", "))
		} else {
			// L13: the no-swap message must name ALL three real reasons a
			// swap can be a no-op: (1) no tagged fences in the document,
			// (2) the document already fits the budget, (3) tagged fences
			// exist but their path does not resolve under root (missing
			// file, escapes root, oversized) or their summary renders empty.
			if n, first := swap.UnswappableFences(text, root); n > 0 {
				fmt.Fprintf(os.Stderr, "kern: no fenced blocks swapped: %d tagged fence(s) could not be resolved (e.g. %q — path must reference a real file under %s) and none of the remaining fences were swapped\n", n, first, root)
			} else {
				fmt.Fprintf(os.Stderr, "kern: no fenced blocks swapped (none present or all within budget %d)\n", budget)
			}
		}
		if !fits {
			fmt.Fprintf(os.Stderr, "kern: warning: still over budget after summarization\n")
		}
		fmt.Print(out)
	}

}

func runDoctor(rest []string) int {
	f, args := parseFlagsOrDie(rest)
	root := projectRoot(f)
	if f.root == "" && len(args) > 0 {
		root = args[0]
	}
	findings := doctor.Run(root)
	if f.archDrift {
		arch, driftRecords := archDriftSection(root)
		findings = append(findings, arch...)
		recordArchDrift(driftRecords, root)
		recordDriftDogfood(driftRecords, root)
	}
	if f.calibration {
		findings = append(findings, calibrationFindings(root)...)
	}
	if f.json {
		printJSON(findings)
		return doctorExitCode(findings)
	}
	fmt.Println(doctor.Render(root, findings))

	return doctorExitCode(findings)
}

// doctorExitCode maps fail-level findings to a non-zero exit so scripts
// and CI can use `kern doctor` as a gate: warn-only runs exit 0, any
// [fail] exits 1 - matching the human-readable verdict line instead of
// contradicting it (e2e round 2: every lane saw "verdict: failures"
// with exit 0).
func doctorExitCode(findings []doctor.Finding) int {
	for _, f := range findings {
		if f.Level == "fail" {
			return 1
		}
	}
	return 0
}
