package main

import (
	"encoding/json"
	"fmt"
	"github.com/JayveerPrajapati/kern/internal/agent"
	"github.com/JayveerPrajapati/kern/internal/app"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/eval"
	"github.com/JayveerPrajapati/kern/internal/eventbus"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
	"github.com/JayveerPrajapati/kern/internal/lenses"
	"github.com/JayveerPrajapati/kern/internal/ownership"
	"github.com/JayveerPrajapati/kern/internal/profiles"
	"github.com/JayveerPrajapati/kern/internal/runtime"
	"github.com/JayveerPrajapati/kern/internal/skills"
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
	// --lens/--profile are only honored for kern analyze: AnalyzeWithLens
	// re-ranks packet facts via TaskService, and plan/risk have no lens or
	// profile surface. Reject them loudly instead of silently dropping.
	if cmd != "analyze" && (f.lens != "" || f.profile != "") {
		fatal("--lens/--profile are only supported for kern analyze")
	}
	change := args[0]
	// V5: the deterministic analyze/plan path resolves <change> as an exact
	// symbol name. NL prose belongs to the kern run intent pipeline — route
	// loudly instead of failing later with a confusing "no symbol named" error.
	if strings.ContainsAny(change, " \t") {
		fatal("kern %s resolves <change> as a SYMBOL name (e.g. pkg.Func or Type.Method).\nFor natural-language change descriptions use: kern run \"%s\"", cmd, change)
	}
	p, err := app.New(root)
	if err != nil {
		fatal("Analyze: %v", err)
	}
	// When --task is set, create an authoritative Task record that tracks the
	// full lifecycle (context packet, risks, evidence) and can be queried via
	// `kern task <id>`. Without --task, the analysis runs stateless (the fast
	// backward-compatible path).
	if f.task != "" || f.lens != "" {
		// --lens requires the taskful path: AnalyzeWithLens re-ranks the
		// packet facts via TaskService, which the stateless p.Analyze path
		// cannot do.
		ts := app.NewTaskService(p, eventbus.New()).WithPRProvider(app.AutoPRProvider())
		if cmd == "plan" {
			// Kern plan produces a structured domain.Plan via the
			// control-plane Plan workflow (analyze → memory → impact → risk →
			// architecture → plan artifact).
			t, plan, text, err := ts.Plan(change)
			if err != nil {
				if planSymbolDegrade(change, root, err) {
					return
				}
				fatal("Analyze: %v", err)
			}
			fmt.Println("PLAN for: " + change)
			fmt.Print(text)
			fmt.Printf("\n[task: %s — state: %s — %d steps, risk=%s]\n", t.ID, t.State, len(plan.ImplementationSteps), plan.Risk)
			return
		}
		var t *agent.Task
		var text string
		if f.lens != "" {
			t, text, err = ts.AnalyzeWithLens(change, f.lens)
		} else {
			t, text, err = ts.Analyze(change)
		}
		if err != nil {
			fatal("Analyze: %v", err)
		}
		// --profile (analyze only): shape how the analysis is presented
		// without changing the evidence (deterministic, no LLM).
		if f.profile != "" {
			pf, ok := profiles.NewRegistryWithBuiltins().Select(f.profile)
			if !ok {
				fatal("unknown profile %q", f.profile)
			}
			text = profiles.ApplyProfile(pf, text)
		}
		fmt.Println("ANALYSIS for: " + change)
		fmt.Print(text)
		fmt.Printf("\n[task: %s — state: %s]\n", t.ID, t.State)
		return
	}
	if cmd == "plan" {
		// Stateless plan path: run analyze then assemble a plan inline.
		pkt, _, err := p.Analyze(change)
		if err != nil {
			if planSymbolDegrade(change, root, err) {
				return
			}
			fatal("Analyze: %v", err)
		}
		fmt.Println("PLAN for: " + change)
		fmt.Print(renderStatelessPlan(change, pkt))
		return
	}
	_, text, err := p.Analyze(change)
	if err != nil {
		fatal("Analyze: %v", err)
	}
	// --profile (analyze only): shape how the analysis is presented
	// without changing the evidence (deterministic, no LLM).
	if f.profile != "" {
		pf, ok := profiles.NewRegistryWithBuiltins().Select(f.profile)
		if !ok {
			fatal("unknown profile %q", f.profile)
		}
		text = profiles.ApplyProfile(pf, text)
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
		fatal("Risk: %v", err)
	}
	_, text, err := p.Risk(change)
	if err != nil {
		fatal("Risk: %v", err)
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
		fatal("Execute: %v", err)
	}
	ts := app.NewTaskService(p, eventbus.New()).WithAgentID("cli").WithPRProvider(app.AutoPRProvider())
	t, diff, v, err := ts.ExecuteAndVerify(string(pb), []string{"build"})
	if err != nil {
		fatal("Execute: %v", err)
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
		fatal("WhatIf: %v", err)
	}
	ts := app.NewTaskService(p, nil).WithPRProvider(app.AutoPRProvider())
	t, text, err := ts.WhatIf(whatif.ChangeKind(kind), change, newTarget)
	if err != nil {
		fatal("WhatIf: %v", err)
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
		fatal("Impact: %v", err)
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
		fatal("Impact: %v", err)
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
	// renderImpactText already emits the "IMPACT for: <target>" header (the
	// impact renderer is shared with the MCP and REST surfaces), so printing it
	// here again produced a duplicated header (F-014). The transitive callees in
	// the "What it calls" section are relabeled against the index's direct call
	// edges so transitive entries are no longer indistinguishable from direct
	// ones.
	fmt.Print(annotateImpactCallees(text, change, root))
	if f.precision == "strict" {
		fmt.Println("precision: strict — call edges from non-resolved languages were skipped (unknown)")
	}
	if len(teams) > 0 {
		fmt.Println("Affected teams: " + strings.Join(teams, ", "))
	}
	fmt.Printf("\n[task: %s — state: %s — risk=%s]\n", t.ID, t.State, rep.Risk)
}

// annotateImpactCallees relabels the "What it calls" section of a rendered
// impact report, marking each entry "(direct)" or "(transitive)" using the
// index's direct call edges (F-014: the report listed transitive callees of
// the target — callees of callees — alongside direct ones with no way to tell
// them apart). The text is returned unchanged when the index is unavailable
// or the target has no recorded call edges, so the annotation never degrades
// the report.
func annotateImpactCallees(text, target, root string) string {
	ix, err := index.Load(root)
	if err != nil {
		return text
	}
	direct := map[string]bool{}
	for _, ce := range ix.Calls[target] {
		direct[simpleSymName(ce.Target)] = true
	}
	if len(direct) == 0 {
		// The target may be typed qualified while the index keys it by the
		// simple name (or vice versa); retry with the bare name.
		for _, ce := range ix.Calls[simpleSymName(target)] {
			direct[simpleSymName(ce.Target)] = true
		}
	}
	if len(direct) == 0 {
		return text
	}
	lines := strings.Split(text, "\n")
	inCalls := false
	for i, ln := range lines {
		trimmed := strings.TrimSpace(ln)
		if strings.HasPrefix(trimmed, "What it calls:") {
			inCalls = true
			continue
		}
		if !inCalls {
			continue
		}
		if !strings.HasPrefix(trimmed, "- ") {
			inCalls = false // next section
			continue
		}
		entry := strings.TrimSpace(trimmed[2:])
		// P1-4: skip renderer summary lines (stdlib collapse, "+N more") so
		// they are never mislabeled as transitive callees.
		if strings.HasPrefix(entry, "stdlib:") || strings.Contains(entry, "use --json for full list") {
			continue
		}
		name := simpleSymName(entry)
		if direct[name] {
			lines[i] = ln + " (direct)"
		} else {
			lines[i] = ln + " (transitive)"
		}
	}
	return strings.Join(lines, "\n")
}

// simpleSymName returns the part of a name after the last '.', so qualified
// ("repo.Query") and bare ("Query") spellings compare equal.
func simpleSymName(name string) string {
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		return name[i+1:]
	}
	return name
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
	// Silent-orchestrator verification modes (tracker: CLI flags spec
	// Enhancements). Exactly one mode per invocation — mixing them is a usage
	// error, not a best-effort union.
	modes := 0
	if f.verifyPipeline {
		modes++
	}
	if f.verifySilent {
		modes++
	}
	if f.verifyTokenReduction {
		modes++
	}
	if f.evalDir != "" {
		modes++
	}
	if f.skillDir != "" {
		modes++
	}
	if f.scanPath != "" {
		modes++
	}
	if modes > 1 {
		fatal("use only one of --verify-pipeline/--verify-silent/--verify-token-reduction/--eval/--skill/--scan")
	}
	// Optional positional symbol for the Verify* helpers: `kern verify
	// --verify-pipeline <symbol>` targets one symbol; without it the helpers
	// run on "" and report the failed step (still exit 0, degraded).
	symbol := ""
	if len(args) > 0 {
		symbol = args[0]
	}
	switch {
	case f.verifyPipeline:
		// End-to-end silent-orchestration run: index → context envelope →
		// deterministic planner → progressive-disclosure retrieval → host
		// injection/extraction into a temp copy of the repo.
		rep := verification.VerifyFullPipeline(root, symbol)
		if f.json {
			rep.Version = version
			printJSON(rep)
			return
		}
		fmt.Printf("envelope_valid: %t\n", rep.EnvelopeValid)
		fmt.Printf("plan_produced: %t\n", rep.PlanProduced)
		fmt.Printf("handles_resolved: %t\n", rep.HandlesResolved)
		fmt.Printf("injected: %t\n", rep.Injected)
		fmt.Printf("extracted: %t\n", rep.Extracted)
		fmt.Printf("silent: %t\n", rep.Silent)
		fmt.Printf("token_reduction: %.2f\n", rep.TokenReduction)
		fmt.Printf("evidence_retained: %.2f\n", rep.EvidenceRetained)
		for _, s := range rep.Steps {
			fmt.Printf("  - %s\n", s)
		}
		return
	case f.verifySilent:
		// Kern-invisibility check: the rendered pipeline must not leak
		// kern-internal markers to a user/LLM. Without a target symbol,
		// report the pass rate over a deterministic sample of core symbols
		// instead of a guaranteed failure (north-star NS-4).
		if symbol == "" {
			rep, err := verification.ScanSilent(root, "internal", 100)
			if err != nil {
				fatal("silent scan: %v", err)
			}
			if f.json {
				printJSON(map[string]any{
					"version":    version,
					"scanned":    rep.Symbols,
					"silent":     rep.Silent,
					"violations": rep.Violations,
					"findings":   rep.Findings,
				})
				return
			}
			rate := 0.0
			if rep.Symbols > 0 {
				rate = float64(rep.Silent) / float64(rep.Symbols)
			}
			fmt.Printf("silent orchestration: %d/%d symbols silent (%.0f%%)\n", rep.Silent, rep.Symbols, rate*100)
			for _, fnd := range rep.Findings {
				fmt.Printf("  - %s (%s): %s\n", fnd.Symbol, fnd.File, strings.Join(fnd.Reasons, "; "))
			}
			return
		}
		ok, reasons := verification.VerifySilentOrchestration(root, symbol)
		if f.json {
			printJSON(map[string]any{"version": version, "ok": ok, "reasons": reasons})
			return
		}
		if ok {
			fmt.Println("silent orchestration: PASS")
		} else {
			fmt.Println("silent orchestration: FAIL")
		}
		for _, r := range reasons {
			fmt.Printf("  - %s\n", r)
		}
		return
	case f.verifyTokenReduction:
		// Token-reduction proof without critical-evidence loss via the eval
		// harness (baseline = full packet, candidate = ~50% budget fit).
		res, err := verification.VerifyTokenReduction(root, symbol)
		if err != nil {
			fatal("VerifyTokenReduction: %v", err)
		}
		if f.json {
			res.Version = version
			printJSON(res)
			return
		}
		fmt.Printf("score: %.2f\n", res.Score)
		fmt.Printf("token_reduction: %.2f\n", res.TokenReduction)
		fmt.Printf("evidence_retention: %.2f\n", res.EvidenceRetention)
		fmt.Printf("error_rate: %.2f\n", res.ErrorRate)
		fmt.Printf("reproducible: %t\n", res.Reproducible)
		fmt.Printf("samples: %d\n", len(res.Samples))
		return
	case f.evalDir != "":
		// --eval DIR: run the deterministic eval harness over user-supplied
		// baseline/candidate samples (one JSON file per sample).
		samples, err := eval.LoadSamplesFromDir(f.evalDir)
		if err != nil {
			fatal("Eval: %v", err)
		}
		// Standard rubric (the eval package's own tests use the same
		// assertions): at least half the critical evidence must survive and
		// at most half the samples may fail.
		h := eval.NewEvalHarness(samples, 0, []eval.Assertion{
			eval.AssertEvidenceRetention(0.5),
			eval.AssertErrorRate(0.5),
		})
		res := h.Run()
		if f.json {
			res.Version = version
			printJSON(res)
			return
		}
		fmt.Printf("score: %.2f\n", res.Score)
		fmt.Printf("token_reduction: %.2f\n", res.TokenReduction)
		fmt.Printf("evidence_retention: %.2f\n", res.EvidenceRetention)
		fmt.Printf("error_rate: %.2f\n", res.ErrorRate)
		fmt.Printf("reproducible: %t\n", res.Reproducible)
		fmt.Printf("samples: %d\n", len(res.Samples))
		return
	case f.skillDir != "":
		// --skill DIR: validate every portable skill (each immediate
		// subdirectory containing a SKILL.md). The Skill struct carries no
		// signature field, so only ValidateSkill applies.
		skillList, err := skills.LoadSkillsFromDir(f.skillDir)
		if err != nil {
			fatal("Skills: %v", err)
		}
		valid := []string{}
		invalid := []string{}
		for _, s := range skillList {
			if verr := skills.ValidateSkill(s); verr != nil {
				fmt.Printf("skill %s: INVALID: %v\n", s.Name, verr)
				invalid = append(invalid, fmt.Sprintf("%s: %v", s.Name, verr))
				continue
			}
			fmt.Printf("skill %s (%s): valid\n", s.Name, s.Source)
			valid = append(valid, s.Name)
		}
		if f.json {
			printJSON(map[string]any{"version": version, "valid": valid, "invalid": invalid})
			return
		}
		return
	case f.scanPath != "":
		// Path-aware silent scan: check every indexed symbol whose file
		// matches the scan path (exact file or directory prefix) for
		// silent-orchestration marker leaks.
		rep, err := verification.ScanSilent(root, f.scanPath, 0)
		if err != nil {
			fatal("Scan: %v", err)
		}
		if f.json {
			rep.Version = version
			printJSON(rep)
			return
		}
		fmt.Printf("silent scan: %s — %d/%d symbols clean, %d violations\n", rep.Path, rep.Silent, rep.Symbols, rep.Violations)
		for _, fi := range rep.Findings {
			fmt.Printf("  - %s (%s): %s\n", fi.Symbol, fi.File, strings.Join(fi.Reasons, "; "))
		}
		fmt.Printf("- scanned %d symbols (limit 200)\n", rep.Symbols)
		return
	}
	// Two forms share this subcommand. The high-level form is
	// `kern verify <types>` (or `kern verify` with no positional, defaulting
	// to build,test); the classic claims form is `kern verify <file|-> [root]`.
	// `--types` is accepted as an explicit alias for the positional form so
	// CLI matches the MCP `kern_verify(types=...)` surface.
	if f.types != "" || len(args) == 0 || isVerifyTypes(args[0]) {
		typesArg := "build,test"
		if f.types != "" {
			typesArg = f.types
		} else if len(args) > 0 && args[0] != "" {
			typesArg = args[0]
		}
		var types []string
		for _, t := range strings.Split(typesArg, ",") {
			if t = strings.TrimSpace(t); t != "" {
				types = append(types, t)
			}
		}
		// Verification runs build/test commands (arbitrary host code); it must
		// pass the governance firewall, fail closed (same gate as kern_validate
		// and the MCP kern_verify tool). Without KERN_ALLOW_EXEC=1 (or an exec
		// tool in the KERN_TOOLS allowlist) the high-level form is refused.
		if err := governance.CheckExec(); err != nil {
			fatal("Verify: %v", err)
		}
		p, perr := app.New(root)
		if perr != nil {
			fatal("%v — run kern index to rebuild it", perr)
		}
		ts := app.NewTaskService(p, nil).WithPRProvider(app.AutoPRProvider())
		_, v, err := ts.Verify(types)
		if err != nil {
			// A FAIL verdict is a valid outcome: surface the typed verdict and
			// per-check status (report A11) instead of a bare error.
			if v.Verdict != "" || v.Build != nil || v.UnitTests != nil || v.Security != nil || v.Architecture != nil || v.Dependency != nil {
				fmt.Println(verification.RenderCompact(v))
				fatal("verification FAILED — see report above; fix failing checks and rerun kern verify")
			}
			fatal("Verify: %v", err)
		}
		if f.json {
			v.Version = version
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
		fatal("Verify: %v", err)
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
		rep.Version = version
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
		fatal("CheckDraft: %v", err)
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
		fatal("Changes: %v", err)
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
			fatal("Changes: %v", err)
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
		// --runtime overlays each changed file with its service profile
		// (directory base name matched against runtime service names).
		var out string
		if f.runtime {
			out = intel.ReviewRanged(ix, changes, f.max, runtime.Overlay(runtime.LoadSource(root)))
		} else {
			out = intel.ReviewRanged(ix, changes, f.max)
		}
		// Review lens (mirrors the kern_review MCP tool): a named lens
		// prepends its evidence-priority line so the caller knows which
		// review posture the context is sized for. No lens arg -> output
		// unchanged.
		if f.lens != "" {
			l, err := lenses.Resolve(f.lens)
			if err != nil {
				fatal("%v", err)
			}
			out = fmt.Sprintf("lens: %s (%s)\n", l.Name, lenses.RenderPriorities(l)) + out
		}
		// --profile shapes how the review is presented without changing the
		// findings (deterministic, no LLM).
		if f.profile != "" {
			p, ok := profiles.NewRegistryWithUserProfiles(root).Select(f.profile)
			if !ok {
				fatal("unknown profile %q", f.profile)
			}
			out = profiles.ApplyProfile(p, out)
		}
		fmt.Println(out)
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

// planSymbolDegrade handles a plan that could not resolve the free-text
// change to a concrete symbol (F-013). The planner is symbol-index-bound:
// instead of a bare `no symbol named "X"` error we degrade gracefully with an
// actionable message — close symbol candidates from the index (when any
// exist) and a pointer to `kern search` so the caller can find the concrete
// symbol and re-run `kern plan <symbol>`. Deterministic, no LLM. Returns
// false when the error is not a symbol-resolution miss, so the caller
// surfaces the original error unchanged.
func planSymbolDegrade(change, root string, err error) bool {
	msg := err.Error()
	if !strings.Contains(msg, "no symbol named") && !strings.Contains(msg, "could not identify a symbol") {
		return false
	}
	var b strings.Builder
	fmt.Fprintf(&b, "no matching symbol for %q — kern plan plans against concrete symbols in the index\n", change)
	if ix, ierr := loadOrBuild(root); ierr == nil {
		if cands := intel.RankedSearch(ix, change, 8); len(cands) > 0 {
			b.WriteString("close candidates:\n")
			for _, c := range cands {
				fmt.Fprintf(&b, "  %-10s %-7s %-24s %s:%d\n", c.Kind, c.Lang, c.FullName(), c.File, c.Line)
			}
		}
	}
	fmt.Fprintf(&b, "hint: run `kern search %q` to find the concrete symbol, then re-run `kern plan <symbol>`", change)
	fatal("%s", b.String())
	return true
}

// renderStatelessPlan renders a domain.Plan-shaped text from a context packet
// for the stateless `kern plan` path (no --task flag). It mirrors the
// TaskService.Plan output shape so callers see the same sections regardless
// of whether a Task was created.
func renderStatelessPlan(change string, pkt domain.ContextPacket) string {
	var b strings.Builder
	fmt.Fprintf(&b, "PLAN\n")
	fmt.Fprintf(&b, "Objective: %s\n", change)
	if whatif.IsNetNewFeature(change) {
		fmt.Fprintf(&b, "Scope: net-new feature (no existing components affected)\n")
	} else {
		fmt.Fprintf(&b, "Scope: %d symbols, %d files\n", len(pkt.Symbols), len(pkt.Files))
	}
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
	if !whatif.IsNetNewFeature(change) {
		fmt.Fprintf(&b, "Affected components:\n")
		for _, sym := range pkt.Symbols {
			fmt.Fprintf(&b, "  - %s\n", sym.Name)
		}
		for _, f := range pkt.Files {
			fmt.Fprintf(&b, "  - %s\n", f.Path)
		}
	}
	fmt.Fprintf(&b, "Implementation steps:\n")
	if whatif.IsNetNewFeature(change) {
		fmt.Fprintf(&b, "  1. Implement the new feature according to specifications.\n")
	} else if len(pkt.Symbols)+len(pkt.Files) > 0 {
		fmt.Fprintf(&b, "  1. Implement the change in the affected components above.\n")
	} else {
		fmt.Fprintf(&b, "  1. Implement the requested change.\n")
	}
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
		fatal("Correlate: %v", err)
	}
	ts := app.NewTaskService(p, eventbus.New()).WithPRProvider(app.AutoPRProvider())
	t, _, text, err := ts.Correlate(al)
	if err != nil {
		fatal("Correlate: %v", err)
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
		fatal("Learn: %v", err)
	}
	ts := app.NewTaskService(p, eventbus.New()).WithPRProvider(app.AutoPRProvider())
	t, _, text, err := ts.Learn(threshold)
	if err != nil {
		fatal("Learn: %v", err)
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
		fatal("Modernize: %v", err)
	}
	ts := app.NewTaskService(p, eventbus.New()).WithPRProvider(app.AutoPRProvider())
	t, _, text, err := ts.Modernize()
	if err != nil {
		fatal("Modernize: %v", err)
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
		fatal("Run: %v", err)
	}
	ts := app.NewTaskService(p, eventbus.New()).WithPRProvider(app.AutoPRProvider())
	result, err := ts.Run(intent)
	if err != nil {
		fatal("Run: %v", err)
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
