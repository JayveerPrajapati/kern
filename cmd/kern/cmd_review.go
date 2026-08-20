package main

import (
	"fmt"
	"github.com/JayveerPrajapati/kern/internal/execution"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
	"github.com/JayveerPrajapati/kern/internal/intelligence"
	"github.com/JayveerPrajapati/kern/internal/ownership"
	"github.com/JayveerPrajapati/kern/internal/verification"
	"github.com/JayveerPrajapati/kern/internal/verify"
	"github.com/JayveerPrajapati/kern/internal/whatif"
	"os"
	"sort"
	"strings"
)

func runAnalyze(cmd string, rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	root := f.root
	if root == "" {
		root = "."
	}
	if len(args) < 1 || args[0] == "" {
		fatalUsage("usage: kern %s <change> [--root ROOT]", cmd)
	}
	change := args[0]
	text, err := analyzeChangeCLI(root, change)
	if err != nil {
		fatal("%v", err)
	}
	if cmd == "analyze" {
		fmt.Println("ANALYSIS for: " + change)
	} else {
		fmt.Println("PLAN for: " + change)
	}
	fmt.Print(text)

}

func runRisk(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	root := f.root
	if root == "" {
		root = "."
	}
	if len(args) < 1 || args[0] == "" {
		fatalUsage("usage: kern risk <change> [--root ROOT]")
	}
	change := args[0]
	text, err := riskChangeCLI(root, change)
	if err != nil {
		fatal("%v", err)
	}
	fmt.Print(text)

}

func runExecute(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	root := f.root
	if root == "" {
		root = "."
	}
	if len(args) < 1 || args[0] == "" {
		fatalUsage("usage: kern execute <patch|patch-file> [--root ROOT]")
	}
	// The plugin passes a multi-line patch through a temp file path; a
	// CLI caller may also pass the raw unified diff inline. Accept both.
	patchArg := args[0]
	pb := []byte(patchArg)
	if st, serr := os.Stat(patchArg); serr == nil && !st.IsDir() {
		pb, err = os.ReadFile(patchArg)
		if err != nil {
			fatal("cannot read patch: %v", err)
		}
	}
	if len(strings.TrimSpace(string(pb))) == 0 {
		fatal("patch is required")
	}
	wt, err := execution.NewWorktree(root)
	if err != nil {
		fatal("%v", err)
	}
	defer wt.Cleanup()
	if err := wt.Apply(string(pb)); err != nil {
		fatal("apply: %v", err)
	}
	diff, err := wt.Diff()
	if err != nil {
		fatal("diff: %v", err)
	}
	v := verification.NewEngine(wt.Dir()).Verify([]string{"build"})
	fmt.Printf("verdict: %s\n", v.Verdict)
	fmt.Printf("summary: %s\n", v.Summary)
	fmt.Printf("diff:\n%s\n", diff)

}

func runWhatIf(cmd string, rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	root := f.root
	if root == "" {
		root = "."
	}
	if len(args) < 1 || args[0] == "" {
		fatalUsage("usage: kern %s <change> [kind] [new-target] [--root ROOT]", cmd)
	}
	change := args[0]
	kind := string(parseChangeKind(change))
	if len(args) > 1 && args[1] != "" {
		kind = args[1]
	}
	newTarget := ""
	if len(args) > 2 && args[2] != "" {
		newTarget = args[2]
	}
	text, err := simulateChangeCLI(root, whatif.ChangeKind(kind), change, newTarget)
	if err != nil {
		fatal("%v", err)
	}
	fmt.Print(text)

}

func runImpact(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	root := f.root
	if root == "" {
		root = "."
	}
	if len(args) < 1 || args[0] == "" {
		fatalUsage("usage: kern impact <change> [kind] [new-target] [--root ROOT]")
	}
	change := args[0]
	kind := string(parseChangeKind(change))
	if len(args) > 1 && args[1] != "" {
		kind = args[1]
	}
	newTarget := ""
	if len(args) > 2 && args[2] != "" {
		newTarget = args[2]
	}
	text, err := simulateChangeCLI(root, whatif.ChangeKind(kind), change, newTarget)
	if err != nil {
		fatal("%v", err)
	}
	// Annotate impact output with owning teams (CODEOWNERS), best-effort: a
	// missing CODEOWNERS or parse failure yields no teams, not a failure.
	// ParseFromRepo returns (nil, err) on open failure (e.g. permission
	// denied); fall back to an empty map so Lookup below never nil-derefs.
	ownerMap, oerr := ownership.ParseFromRepo(root)
	if oerr != nil {
		ownerMap = &ownership.Map{}
	}
	var teams []string
	if ix, ierr := index.Build(root); ierr == nil {
		g := intelligence.FromIndex(ix)
		imp := whatif.Simulate(&g, whatif.Change{Kind: whatif.ChangeKind(kind), Target: change, NewTarget: newTarget})
		seen := map[string]bool{}
		for _, f := range imp.Files {
			for _, o := range ownerMap.Lookup(f) {
				if !seen[o] {
					seen[o] = true
					teams = append(teams, o)
				}
			}
		}
		sort.Strings(teams)
	}
	fmt.Print("IMPACT for: " + change + "\n")
	fmt.Print(text)
	if len(teams) > 0 {
		fmt.Println("Affected teams: " + strings.Join(teams, ", "))
	}

}

func runVerify(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	root := f.root
	if root == "" {
		root = "."
	}
	// Two forms share this subcommand. The high-level form is
	// `kern verify <types>` (or `kern verify` with no positional, defaulting
	// to build,test); the classic claims form is `kern verify <file|-> [root]`.
	if len(args) == 0 || isVerifyTypes(args[0]) {
		typesArg := "build,test"
		if len(args) > 0 && args[0] != "" {
			typesArg = args[0]
		}
		var types []string
		for _, t := range strings.Split(typesArg, ",") {
			if t = strings.TrimSpace(t); t != "" {
				types = append(types, t)
			}
		}
		v := verification.NewEngine(root).Verify(types)
		if f.json {
			printJSON(v)
			return
		}
		fmt.Printf("verdict: %s\n", v.Verdict)
		fmt.Printf("summary: %s\n", v.Summary)
		if v.Build != nil {
			st := "FAIL"
			if v.Build.OK {
				st = "OK"
			}
			fmt.Printf("build: %s (duration %s)\n", st, v.Build.Duration)
			if out := clipText(v.Build.Output, 500); out != "" {
				fmt.Println(out)
			}
		}
		if v.UnitTests != nil {
			st := "FAIL"
			if v.UnitTests.OK {
				st = "OK"
			}
			fmt.Printf("tests: passed=%d failed=%d skipped=%d %s (duration %s)\n", v.UnitTests.Passed, v.UnitTests.Failed, v.UnitTests.Skipped, st, v.UnitTests.Duration)
			if out := clipText(v.UnitTests.Output, 500); out != "" {
				fmt.Println(out)
			}
		}
		if v.Security != nil {
			st := "FAIL"
			if v.Security.OK {
				st = "OK"
			}
			fmt.Printf("security: %s findings=%d critical=%d high=%d low=%d\n", st, v.Security.Count, v.Security.Critical, v.Security.High, v.Security.Low)
			for i, fd := range v.Security.Findings {
				if i >= 10 {
					break
				}
				fmt.Printf("  - %s:%d [%s] %s: %s\n", fd.File, fd.Line, fd.Severity, fd.Rule, fd.Message)
			}
		}
		if v.Architecture != nil {
			st := "OK"
			if !v.Architecture.OK {
				st = "FAIL"
			}
			fmt.Printf("architecture: %s\n", st)
			for _, viol := range v.Architecture.Violations {
				fmt.Printf("  - %s\n", viol)
			}
		}
		if v.Dependency != nil {
			st := "OK"
			if !v.Dependency.OK {
				st = "FAIL"
			}
			fmt.Printf("dependency: %s nodes=%d edges=%d\n", st, v.Dependency.GraphNodes, v.Dependency.GraphEdges)
			for _, fd := range v.Dependency.Findings {
				fmt.Printf("  - %s\n", fd)
			}
		}
		return
	}
	in := "-"
	if len(args) > 0 && args[0] != "" {
		in = args[0]
		args = args[1:]
	}
	if len(args) > 0 {
		root = args[0]
	}
	var b []byte
	if in == "-" {
		b, err = readStdin()
	} else if st, serr := os.Stat(in); serr == nil && st.IsDir() {
		fatal("%q is a directory — kern verify checks a file of claims, e.g. \"kern verify <output.txt>\"; pipe stdin with '-'", in)
	} else {
		b, err = os.ReadFile(in)
	}
	if err != nil {
		fatal("%v", err)
	}
	if len(strings.TrimSpace(string(b))) == 0 {
		fatal("no claims to verify: %q is empty (pass a file of agent output or '-' for stdin)", in)
	}
	ix, ierr := loadOrBuild(root)
	if ierr != nil {
		ix = nil
	}
	rep := verify.Sorted(verify.Verify(ix, root, string(b)))
	if f.json {
		printJSON(rep)
		return
	}
	fmt.Println(verify.Render(rep))

}

func runChanges(cmd string, rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	root := f.root
	if root == "" {
		root = "."
		if len(args) > 0 {
			root = args[0]
		}
	}
	ix, err := intel.ReadIndex(root)
	if err != nil {
		fatal("%v", err)
	}
	var changes []intel.FileChange
	if f.file != "" {
		for _, p := range strings.Split(f.file, ",") {
			if p = strings.TrimSpace(p); p != "" {
				changes = append(changes, intel.FileChange{File: p})
			}
		}
	} else {
		from, to := splitRange(f.range_)
		changes, err = intel.FilesForRangeL(root, from, to)
		if err != nil {
			fatal("%v", err)
		}
	}
	if len(changes) == 0 {
		if f.json {
			// Clean tree is a success for CI: emit the same JSON shape as the
			// non-empty path, with empty/zero values.
			printJSON(&intel.ChangesReport{})
			return
		}
		// Clean tree is a success for CI: nothing to report.
		fmt.Println("no changed files (clean)")
		return
	}
	if cmd == "review" {
		report := intel.AnalyzeChangesRanged(ix, changes)
		if f.json {
			// --json is honored here too (same shape as `kern changes --json`),
			// not silently dropped for the markdown view.
			printJSON(report)
			if report.TotalRisk > 0 {
				os.Exit(1)
			}
			return
		}
		fmt.Println(intel.ReviewRanged(ix, changes, f.max))
		if report.TotalRisk > 0 {
			fmt.Fprintf(os.Stderr, "kern: %d changed file(s) with risk (total %.1f); exit 1\n", len(report.Changes), report.TotalRisk)
			os.Exit(1)
		}
		return
	}
	report := intel.AnalyzeChangesRanged(ix, changes)
	if f.json {
		printJSON(report)
		if report.TotalRisk > 0 {
			os.Exit(1)
		}
		return
	}
	fmt.Println(intel.RenderChanges(report))
	if report.TotalRisk > 0 {
		fmt.Fprintf(os.Stderr, "kern: %d changed file(s) with risk (total %.1f); exit 1\n", len(report.Changes), report.TotalRisk)
		os.Exit(1)
	}

}

// parseChangeKind derives the hypothetical change kind from the first word of
// a change description (case-insensitive). Falls back to RemoveSymbol, matching
// the historical default, when the wording is unrecognized.
func parseChangeKind(change string) whatif.ChangeKind {
	fields := strings.Fields(change)
	if len(fields) == 0 {
		return whatif.RemoveSymbol
	}
	word := strings.ToLower(fields[0])
	switch word {
	case "remove", "delete", "drop":
		return whatif.RemoveSymbol
	case "change", "modify", "update", "refactor", "rewrite", "replace":
		return whatif.ChangeSignature
	case "add", "create", "introduce", "new":
		return whatif.AddSymbol
	case "rename":
		return whatif.RenameSymbol
	case "move":
		return whatif.MoveModule
	case "split":
		return whatif.SplitService
	default:
		return whatif.RemoveSymbol
	}
}
