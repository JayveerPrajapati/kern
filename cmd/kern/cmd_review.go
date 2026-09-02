package main

import (
	"encoding/json"
	"fmt"
	"github.com/JayveerPrajapati/kern/internal/app"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/eventbus"
	"github.com/JayveerPrajapati/kern/internal/intel"
	"github.com/JayveerPrajapati/kern/internal/ownership"
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
	p, err := app.New(root)
	if err != nil {
		fatal("%v", err)
	}
	// When --task is set, create an authoritative Task record that tracks the
	// full lifecycle (context packet, risks, evidence) and can be queried via
	// `kern task <id>`. Without --task, the analysis runs stateless (the fast
	// backward-compatible path).
	if f.task != "" {
		ts := app.NewTaskService(p, eventbus.New()).WithPRProvider(app.AutoPRProvider())
		if cmd == "plan" {
			// Kern plan produces a structured domain.Plan via the
			// control-plane Plan workflow (analyze → memory → impact → risk →
			// architecture → plan artifact).
			t, plan, text, err := ts.Plan(change)
			if err != nil {
				fatal("%v", err)
			}
			fmt.Println("PLAN for: " + change)
			fmt.Print(text)
			fmt.Printf("\n[task: %s — state: %s — %d steps, risk=%s]\n", t.ID, t.State, len(plan.ImplementationSteps), plan.Risk)
			return
		}
		t, text, err := ts.Analyze(change)
		if err != nil {
			fatal("%v", err)
		}
		fmt.Println("ANALYSIS for: " + change)
		fmt.Print(text)
		fmt.Printf("\n[task: %s — state: %s]\n", t.ID, t.State)
	}
	if cmd == "plan" {
		// Stateless plan path: run analyze then assemble a plan inline.
		pkt, _, err := p.Analyze(change)
		if err != nil {
			fatal("%v", err)
		}
		fmt.Println("PLAN for: " + change)
		fmt.Print(renderStatelessPlan(change, pkt))
		return
	}
	_, text, err := p.Analyze(change)
	if err != nil {
		fatal("%v", err)
	}
	fmt.Println("ANALYSIS for: " + change)
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
	p, err := app.New(root)
	if err != nil {
		fatal("%v", err)
	}
	_, text, err := p.Risk(change)
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
	// Route through TaskService.ExecuteAndVerify so an authoritative Task is
	// created, governance is centralized (not per-call-site), and the diff +
	// verification are recorded as artifacts. This replaces the legacy raw
	// execution.NewWorktree + manual verify path.
	p, err := app.New(root)
	if err != nil {
		fatal("%v", err)
	}
	ts := app.NewTaskService(p, eventbus.New()).WithAgentID("cli").WithPRProvider(app.AutoPRProvider())
	t, diff, v, err := ts.ExecuteAndVerify(string(pb), []string{"build"})
	if err != nil {
		fatal("%v", err)
	}
	fmt.Printf("verdict: %s\n", v.Verdict)
	fmt.Printf("summary: %s\n", v.Summary)
	fmt.Printf("diff:\n%s\n", diff)
	fmt.Printf("\n[task: %s — state: %s]\n", t.ID, t.State)
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
	p, err := app.New(root)
	if err != nil {
		fatal("%v", err)
	}
	ts := app.NewTaskService(p, nil).WithPRProvider(app.AutoPRProvider())
	t, text, err := ts.WhatIf(whatif.ChangeKind(kind), change, newTarget)
	if err != nil {
		fatal("%v", err)
	}
	if f.json {
		printJSON(map[string]any{
			"change":  change,
			"kind":    kind,
			"task_id": t.ID,
			"state":   t.State,
			"impact":  t.ImpactReport,
		})
		return
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
	p, err := app.New(root)
	if err != nil {
		fatal("%v", err)
	}
	// Kern impact now produces the 11-question deterministic ImpactReport
	// via TaskService.Impact (graph-driven, no LLM). The what-if kind/new-target
	// args are still honored for backward compatibility but the primary output is
	// the structured impact report.
	ts := app.NewTaskService(p, eventbus.New()).WithPRProvider(app.AutoPRProvider())
	var impactOpts []app.ImpactOption
	if f.precision == "strict" {
		// Strict precision: skip call edges whose caller language is not
		// "resolved"-precision in the index (they are unknown, not guessable).
		impactOpts = append(impactOpts, app.ImpactStrict())
	}
	t, rep, text, err := ts.Impact(change, impactOpts...)
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
	{
		seen := map[string]bool{}
		// Use files from the task's context packet (attached by the analyze
		// stage inside Impact) for ownership lookup.
		if t.ContextPacket != nil {
			for _, f := range t.ContextPacket.Files {
				for _, o := range ownerMap.Lookup(f.Path) {
					if !seen[o] {
						seen[o] = true
						teams = append(teams, o)
					}
				}
			}
		}
		sort.Strings(teams)
	}
	if f.json {
		// Structured, tool-friendly output: the deterministic 11-question
		// ImpactReport plus routing context. Text behavior is unchanged when
		// --json is absent.
		printJSON(map[string]any{
			"change":  change,
			"task_id": t.ID,
			"state":   t.State,
			"risk":    rep.Risk,
			"teams":   teams,
			"impact":  rep,
		})
		return
	}
	fmt.Print("IMPACT for: " + change + "\n")
	fmt.Print(text)
	if f.precision == "strict" {
		fmt.Println("precision: strict — call edges from non-resolved languages were skipped (unknown)")
	}
	if len(teams) > 0 {
		fmt.Println("Affected teams: " + strings.Join(teams, ", "))
	}
	fmt.Printf("\n[task: %s — state: %s — risk=%s]\n", t.ID, t.State, rep.Risk)
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
		p, perr := app.New(root)
		if perr != nil {
			fatal("%v", perr)
		}
		ts := app.NewTaskService(p, nil).WithPRProvider(app.AutoPRProvider())
		_, v, err := ts.Verify(types)
		if err != nil {
			fatal("%v", err)
		}
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

// runCheckDraft implements `kern check-draft <file|-> [root] [--lang LANG]`:
// validate a draft code snippet against the project index. The MCP tool
// kern_check_draft is the primary surface (this thin CLI form exists so the
// opencode plugin, which shells out to the CLI, can reach the same check).
func runCheckDraft(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	root := f.root
	if root == "" {
		root = "."
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
	} else {
		b, err = os.ReadFile(in)
	}
	if err != nil {
		fatal("%v", err)
	}
	ix, ierr := loadOrBuild(root)
	if ierr != nil {
		ix = nil
	}
	findings := verify.CheckDraft(ix, root, b, f.lang)
	if len(findings) == 0 {
		fmt.Println("OK: draft validates cleanly — no issues found")
		return
	}
	for _, fd := range findings {
		fmt.Printf("draft.go:%d [%s] %s\n", fd.Line, fd.Kind, fd.Message)
	}
	fmt.Printf("%d issue(s) found\n", len(findings))
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
				panic(exitError{code: 1})
			}
			return
		}
		fmt.Println(intel.ReviewRanged(ix, changes, f.max))
		if report.TotalRisk > 0 {
			fmt.Fprintf(os.Stderr, "kern: %d changed file(s) with risk (total %.1f); exit 1\n", len(report.Changes), report.TotalRisk)
			panic(exitError{code: 1})
		}
		return
	}
	report := intel.AnalyzeChangesRanged(ix, changes)
	if f.json {
		printJSON(report)
		if report.TotalRisk > 0 {
			panic(exitError{code: 1})
		}
		return
	}
	fmt.Println(intel.RenderChanges(report))
	if report.TotalRisk > 0 {
		fmt.Fprintf(os.Stderr, "kern: %d changed file(s) with risk (total %.1f); exit 1\n", len(report.Changes), report.TotalRisk)
		panic(exitError{code: 1})
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

// renderStatelessPlan renders a domain.Plan-shaped text from a context packet
// for the stateless `kern plan` path (no --task flag). It mirrors the
// TaskService.Plan output shape so callers see the same sections regardless
// of whether a Task was created.
func renderStatelessPlan(change string, pkt domain.ContextPacket) string {
	var b strings.Builder
	fmt.Fprintf(&b, "PLAN\n")
	fmt.Fprintf(&b, "Objective: %s\n", change)
	fmt.Fprintf(&b, "Scope: %d symbols, %d files\n", len(pkt.Symbols), len(pkt.Files))
	risk := "low"
	for _, r := range pkt.Risks {
		if r.Level == domain.RiskCritical || r.Level == domain.RiskHigh {
			risk = "high"
			break
		}
		if r.Level == domain.RiskMedium {
			risk = "medium"
		}
	}
	fmt.Fprintf(&b, "Risk: %s\n", risk)
	fmt.Fprintf(&b, "Affected components:\n")
	for _, sym := range pkt.Symbols {
		fmt.Fprintf(&b, "  - %s\n", sym.Name)
	}
	for _, f := range pkt.Files {
		fmt.Fprintf(&b, "  - %s\n", f.Path)
	}
	fmt.Fprintf(&b, "Implementation steps:\n")
	fmt.Fprintf(&b, "  1. Implement the change in the affected components above.\n")
	for _, v := range pkt.RequiredValidation {
		fmt.Fprintf(&b, "  - %s\n", v)
	}
	if len(pkt.Risks) > 0 {
		fmt.Fprintf(&b, "Rollback: revert the commit")
		if risk == "high" {
			b.WriteString(" and redeploy previous version")
		}
		b.WriteString("\n")
	}
	if len(pkt.RequiredValidation) > 0 {
		fmt.Fprintf(&b, "Tests:\n")
		for _, t := range pkt.RequiredValidation {
			fmt.Fprintf(&b, "  - %s\n", t)
		}
	}
	return b.String()
}

func runCorrelate(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	root := f.root
	if root == "" {
		root = "."
	}
	if len(args) < 1 || args[0] == "" {
		fatalUsage("usage: kern correlate <alert-json> [--root ROOT]")
	}
	var al domain.Alert
	if err := json.Unmarshal([]byte(args[0]), &al); err != nil {
		fatal("invalid alert JSON: %v", err)
	}
	p, err := app.New(root)
	if err != nil {
		fatal("%v", err)
	}
	ts := app.NewTaskService(p, eventbus.New()).WithPRProvider(app.AutoPRProvider())
	t, _, text, err := ts.Correlate(al)
	if err != nil {
		fatal("%v", err)
	}
	fmt.Print(text)
	fmt.Printf("\n[task: %s — state: %s]\n", t.ID, t.State)
}

func runLearn(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	root := f.root
	if root == "" {
		root = "."
	}
	threshold := 3
	if len(args) > 0 && args[0] != "" {
		if n, err := fmt.Sscanf(args[0], "%d", &threshold); err != nil || n != 1 {
			threshold = 3
		}
	}
	p, err := app.New(root)
	if err != nil {
		fatal("%v", err)
	}
	ts := app.NewTaskService(p, eventbus.New()).WithPRProvider(app.AutoPRProvider())
	t, _, text, err := ts.Learn(threshold)
	if err != nil {
		fatal("%v", err)
	}
	fmt.Print(text)
	fmt.Printf("\n[task: %s — state: %s]\n", t.ID, t.State)
}

func runModernize(rest []string) {
	f, _, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	root := f.root
	if root == "" {
		root = "."
	}
	p, err := app.New(root)
	if err != nil {
		fatal("%v", err)
	}
	ts := app.NewTaskService(p, eventbus.New()).WithPRProvider(app.AutoPRProvider())
	t, _, text, err := ts.Modernize()
	if err != nil {
		fatal("%v", err)
	}
	fmt.Print(text)
	fmt.Printf("\n[task: %s — state: %s]\n", t.ID, t.State)
}

// runRun implements `kern run <intent>` — the kern_run entry point (Strict
// Plan ). It compiles the intent, selects the workflow + capabilities,
// creates a Task, and prints the run result.
func runRun(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	root := f.root
	if root == "" {
		root = "."
	}
	if len(args) < 1 || args[0] == "" {
		fatalUsage("usage: kern run <intent> [--root ROOT]")
	}
	intent := args[0]
	p, err := app.New(root)
	if err != nil {
		fatal("%v", err)
	}
	ts := app.NewTaskService(p, eventbus.New()).WithPRProvider(app.AutoPRProvider())
	result, err := ts.Run(intent)
	if err != nil {
		fatal("%v", err)
	}
	fmt.Printf("RUN for: %s\n", intent)
	fmt.Printf("  task:      %s\n", result.TaskID)
	fmt.Printf("  intent:    %s\n", result.Intent.Type)
	fmt.Printf("  workflow:  %s\n", result.Workflow)
	fmt.Printf("  target:    %s\n", result.Intent.Target)
	fmt.Printf("  risk:      %s (approval: %s)\n", result.Risk.Level, result.ApprovalState)
	fmt.Printf("  caps:      %s\n", strings.Join(result.Capabilities, ", "))
	fmt.Printf("  tools:     %s\n", strings.Join(result.Tools, ", "))
	fmt.Printf("  agents:    %s\n", strings.Join(result.Agents, ", "))
	fmt.Printf("  next:      %s\n", result.NextAction)
}
