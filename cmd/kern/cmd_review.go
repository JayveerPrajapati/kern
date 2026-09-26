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
	"github.com/JayveerPrajapati/kern/internal/intel"
	"github.com/JayveerPrajapati/kern/internal/metrics"
	"github.com/JayveerPrajapati/kern/internal/ownership"
	"github.com/JayveerPrajapati/kern/internal/profiles"
	"github.com/JayveerPrajapati/kern/internal/skills"
	"github.com/JayveerPrajapati/kern/internal/verification"
	"github.com/JayveerPrajapati/kern/internal/whatif"
	"os"
	"sort"
	"strings"
	"time"
)

func runAnalyze(cmd string, rest []string) {
	f, args := parseFlagsOrDie(rest)
	root := projectRoot(f)
	if len(args) < 1 || args[0] == "" {
		fatalUsage("usage: kern %s <change> [--root ROOT]", cmd)
	}
	// --lens/--profile are only honored for kern analyze: AnalyzeWithLens
	// re-ranks packet facts via TaskService, and plan/risk have no lens or
	// profile surface. Reject them loudly instead of silently dropping.
	if cmd != "analyze" && (f.lens != "" || f.profile != "") {
		fatal("--lens/--profile are only supported for kern analyze")
	}
	// plan/risk print deterministic text plans; the shared parser accepted
	// --json but the command ignored it. Reject loudly instead of silently
	// dropping the flag.
	if cmd != "analyze" && f.json {
		fatal("--json is only supported for kern analyze")
	}
	args = joinVerbPositionals(args)
	change := args[0]
	p, err := app.New(root)
	if err != nil {
		fatal("Analyze: %v", err)
	}
	// --lens risk renders the focused governance risk assessment (the
	// former `kern risk` output: level, factors, mitigation) instead of the
	// re-ranked analysis packet. The lens is served directly by
	// TaskService.Risk so `kern analyze --lens risk <change>` is byte-identical
	// to `kern risk <change>`; `kern risk` is now a thin wrapper that presets
	// this lens (surface consolidation T2b).
	if f.lens == "risk" {
		ts := app.NewTaskService(p, eventbus.New()).WithPRProvider(app.AutoPRProvider())
		_, text, err := ts.Risk(change)
		if err != nil {
			fatal("Risk: %v", err)
		}
		if f.profile != "" {
			pf, ok := profiles.NewRegistryWithBuiltins().Select(f.profile)
			if !ok {
				fatal("unknown profile %q", f.profile)
			}
			text = profiles.ApplyProfile(pf, text)
		}
		fmt.Print(text)
		return
	}
	// When --task is set, create an authoritative Task record that tracks the
	// full lifecycle (context packet, risks, evidence) and can be queried via
	// `kern task <id>`. Without --task, the analysis runs stateless (the fast
	// backward-compatible path).
	if f.task != "" || f.lens != "" {
		// --lens requires the taskful path: AnalyzeWithLens re-ranks the
		// packet facts via TaskService, which the stateless p.Analyze path
		// cannot do. Task persistence is gated on --task (F9): only an
		// explicit --task asks for an authoritative persisted record
		// (t-<n>, queryable via `kern task <id>`); a --lens-only run keeps
		// the ephemeral analysis path (no store pollution).
		ts := app.NewTaskService(p, eventbus.New()).WithPRProvider(app.AutoPRProvider()).WithTaskPersistence(f.task != "")
		if cmd == "plan" {
			// Kern plan produces a structured domain.Plan via the
			// control-plane Plan workflow (analyze → memory → impact → risk →
			// architecture → plan artifact).
			t, plan, text, err := ts.Plan(change)
			if err != nil {
				if symbolDegrade("plan", change, root, err) {
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
			// Analyze must say explicitly that it could not resolve the
			// symbol, with candidates, instead of failing opaque (F12).
			if symbolDegrade("analyze", change, root, err) {
				return
			}
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
			if symbolDegrade("plan", change, root, err) {
				return
			}
			fatal("Analyze: %v", err)
		}
		fmt.Println("PLAN for: " + change)
		fmt.Print(app.RenderStatelessPlan(change, pkt, root))
		return
	}
	_, text, err := p.Analyze(change)
	if err != nil {
		// Analyze must say explicitly that it could not resolve the symbol,
		// with candidates, instead of failing opaque (F12).
		if symbolDegrade("analyze", change, root, err) {
			return
		}
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
	f, args := parseFlagsOrDie(rest)
	root := projectRoot(f)
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
	root := projectRoot(f)
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
	const maxDiff = 1 << 20 // 1 MiB print cap (a sandbox diff can reach tens of MB)
	if len(diff) > maxDiff {
		fmt.Printf("diff: (%d bytes, showing first %d)\n%s\n", len(diff), maxDiff, diff[:maxDiff])
	} else {
		fmt.Printf("diff:\n%s\n", diff)
	}
	fmt.Printf("\n[task: %s — state: %s]\n", t.ID, t.State)
}

// joinVerbPositionals detects the unquoted-sentence pattern on the analysis
// doors (impact/analyze/what-if/plan): `kern impact remove WriteFileAtomic`
// parses as args[0]="remove" (the change) + args[1]="WriteFileAtomic" (the
// [kind] positional). A leading change-verb means the user forgot to quote a
// sentence — joining the positionals lets the extractor find the real symbol
// instead of fuzzy-resolving the bare verb to an unrelated one (QA: 'remove'
// resolved to Client.Remove and reported on the wrong symbol, exit 0).
func joinVerbPositionals(args []string) []string {
	if len(args) >= 2 && whatif.IsChangeVerb(args[0]) {
		return []string{strings.Join(args, " ")}
	}
	return args
}

func runWhatIf(cmd string, rest []string) {
	f, args := parseFlagsOrDie(rest)
	root := projectRoot(f)
	if len(args) < 1 || args[0] == "" {
		fatalUsage("usage: kern %s <change> [kind] [new-target] [--root ROOT]", cmd)
	}
	args = joinVerbPositionals(args)
	change := args[0]
	kind := string(app.ParseChangeKind(change))
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
	ts := app.NewTaskService(p, nil).WithPRProvider(app.AutoPRProvider()).WithTaskPersistence(f.task != "")
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
	f, args := parseFlagsOrDie(rest)
	root := projectRoot(f)
	if len(args) < 1 || args[0] == "" {
		fatalUsage("usage: kern impact <change> [kind] [new-target] [--root ROOT]")
	}
	// F22: --precision previously accepted any value and silently ignored
	// everything except "strict". Reject unknown values as a usage error
	// (rc=2) instead of pretending they took effect.
	switch f.precision {
	case "", "default", "strict":
	default:
		fatalUsage("impact: invalid --precision %q (want \"default\" or \"strict\")", f.precision)
	}
	args = joinVerbPositionals(args)
	change := args[0]
	p, err := app.New(root)
	if err != nil {
		fatal("Impact: %v", err)
	}
	// --risk renders the focused governance risk assessment (p.Risk /
	// ts.Risk — the same output as `kern analyze --lens risk` and the
	// former `kern risk`) instead of the 11-question impact report. It is
	// the CLI backing for the plugin's kern_impact risk=true flag
	// (surface consolidation T2b).
	if f.risk {
		ts := app.NewTaskService(p, eventbus.New()).WithPRProvider(app.AutoPRProvider())
		_, text, err := ts.Risk(change)
		if err != nil {
			if symbolDegrade("risk", change, root, err) {
				return
			}
			fatal("Risk: %v", err)
		}
		fmt.Print(text)
		return
	}
	// Kern impact now produces the 11-question deterministic ImpactReport
	// via TaskService.Impact (graph-driven, no LLM). The what-if kind/new-target
	// args are still honored for backward compatibility but the primary output is
	// the structured impact report. Task persistence is gated on --task (F9):
	// only an explicit --task asks for an authoritative persisted record.
	ts := app.NewTaskService(p, eventbus.New()).WithPRProvider(app.AutoPRProvider()).WithTaskPersistence(f.task != "")
	var impactOpts []app.ImpactOption
	if f.precision == "strict" {
		// Strict precision: skip call edges whose caller language is not
		// "resolved"-precision in the index (they are unknown, not guessable).
		impactOpts = append(impactOpts, app.ImpactStrict())
	}
	t, rep, text, err := ts.Impact(change, impactOpts...)
	if err != nil {
		if symbolDegrade("impact", change, root, err) {
			return
		}
		fatal("Impact: %v", err)
	}
	// --json is honored (F-IM2): impact is a CI-valuable report whose
	// review/changes siblings have JSON; emit the structured ImpactReport.
	// Exit semantics match the text path (findings are outcomes, exit 0).
	if f.json {
		printJSON(map[string]any{
			"change":  change,
			"task_id": t.ID,
			"state":   t.State,
			"impact":  rep,
		})
		return
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
	// here again produced a duplicated header. The transitive callees in
	// the "What it calls" section are relabeled against the index's direct call
	// edges so transitive entries are no longer indistinguishable from direct
	// ones.
	fmt.Print(app.AnnotateImpactCallees(text, change, root))
	if f.precision == "strict" {
		fmt.Println("precision: strict — call edges from non-resolved languages were skipped (unknown)")
	}
	if len(teams) > 0 {
		fmt.Println("Affected teams: " + strings.Join(teams, ", "))
	}
	fmt.Printf("\n[task: %s — state: %s — risk=%s]\n", t.ID, t.State, rep.Risk)
}

// verifyExitCode maps a verification verdict to its process exit code
// (dogfooding F7/F19): FAIL is the only hard failure (1); WARN and SKIPPED
// are reported outcomes and exit 0; PASS/PASS_WITH_WARNING exit 0. The --json
// and text paths share this single mapping so they can never drift.
func verifyExitCode(verdict verification.Verdict) int {
	if verdict == verification.VerdictFail {
		return 1
	}
	return 0
}

// verifyOutcomeLine returns the human-readable outcome line for a
// verification verdict (F7/F19): "verification FAILED" is reserved for a FAIL
// verdict; WARN and SKIPPED are reported outcomes with their own wording, and
// a SKIPPED verdict names the reason (e.g. "govulncheck not installed",
// "license manifest missing") when one is recorded.
func verifyOutcomeLine(v verification.VerificationResult) string {
	switch v.Verdict {
	case verification.VerdictFail:
		return "verification FAILED — see report above; fix failing checks and rerun kern verify"
	case verification.VerdictWarn:
		return "verification WARNED — see report above; address the warnings and rerun kern verify"
	case verification.VerdictSkipped:
		line := "verification SKIPPED — missing tools or dependencies are not a hard failure"
		if reasons := verifySkippedReasons(&v); len(reasons) > 0 {
			line += ": " + strings.Join(reasons, "; ")
		}
		return line + " (install the tool or provide the manifest, then rerun kern verify)"
	case verification.VerdictPass, verification.VerdictPassWithWarning:
		return "verification PASSED — see report above"
	default:
		return "verification incomplete (" + string(v.Verdict) + ") — see report above"
	}
}

// verifySkippedReasons collects the first-line SKIPPED reasons from a
// verification result's sub-checks (govulncheck absent, license/dependency
// manifest missing, unisolated test set, ...), so a SKIPPED outcome line
// explains why instead of a bare verdict.
func verifySkippedReasons(v *verification.VerificationResult) []string {
	if v == nil {
		return nil
	}
	var reasons []string
	collect := func(s string) {
		if s = strings.TrimSpace(strings.SplitN(s, "\n", 2)[0]); s != "" {
			reasons = append(reasons, s)
		}
	}
	if v.CVE != nil && v.CVE.Status == verification.StatusSkipped {
		collect(v.CVE.Detail)
	}
	if v.License != nil && v.License.Skipped != "" {
		collect(v.License.Skipped)
	}
	if v.Dependency != nil && v.Dependency.Skipped != "" {
		collect(v.Dependency.Skipped)
	}
	if v.Secrets != nil && v.Secrets.Status == verification.StatusSkipped {
		collect(v.Secrets.Detail)
	}
	if v.UnitTests != nil && v.UnitTests.Status == verification.StatusSkipped {
		collect(v.UnitTests.Output)
	}
	if v.Integration != nil && v.Integration.Status == verification.StatusSkipped {
		collect(v.Integration.Output)
	}
	return reasons
}

func runVerify(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	root := projectRoot(f)
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
			if !rep.EnvelopeValid {
				fatal("verify-pipeline: envelope invalid — see JSON output above")
			}
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
		if !rep.EnvelopeValid {
			fatal("verify-pipeline: envelope invalid — see output above")
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
			if !ok {
				fatal("verify-silent: orchestration leak detected — see JSON output above")
			}
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
		if !ok {
			fatal("verify-silent: orchestration leak detected — see output above")
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
			if len(invalid) > 0 {
				fatal("verify: %d invalid skill(s) — see JSON output above", len(invalid))
			}
			return
		}
		if len(invalid) > 0 {
			fatal("verify: %d invalid skill(s) — see output above", len(invalid))
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
		// Opt-in compliance checks (--cve/--license/--secrets): each flag
		// appends its check to the requested types — the compliance trio runs
		// ONLY when explicitly requested, never by default.
		if f.cve {
			types = append(types, "cve")
		}
		if f.license {
			types = append(types, "license")
		}
		if f.secrets {
			types = append(types, "secrets")
		}
		// Verification runs build/test commands (arbitrary host code); it must
		// pass the governance firewall, fail closed (same gate as kern_validate
		// and the MCP kern_verify tool). The concrete verify command is bound to
		// any approval, so a HIGH/CRITICAL denial persists a command-bound,
		// human-resolvable approval (`kern approve <id>`) under the project root
		// instead of an in-memory approval nobody can resolve (oracle-gate).
		if err := governance.CheckExecCommand("kern verify "+strings.Join(types, " "), root); err != nil {
			fatal("Verify: %v", err)
		}
		p, perr := app.New(root)
		if perr != nil {
			fatal("%v — run kern index to rebuild it", perr)
		}
		ts := app.NewTaskService(p, nil).WithPRProvider(app.AutoPRProvider())
		verifyStart := time.Now()
		_, v, err := ts.Verify(types)
		// CLI telemetry: the in-process recorder is loaded/saved by main()
		// for every invocation, so this lands in the persisted snapshot
		// (`kern stats performance`).
		metrics.Default().RecordVerification(time.Since(verifyStart))
		if err != nil {
			// A FAIL/WARN/SKIPPED verdict is a valid outcome: surface the typed
			// verdict and per-check status instead of a bare error.
			if v.Verdict != "" || v.Build != nil || v.UnitTests != nil || v.Security != nil || v.Architecture != nil || v.Dependency != nil || v.CVE != nil || v.License != nil || v.Secrets != nil {
				if f.json {
					v.Version = version
					printJSON(v)
					// F7/F19 exit contract: FAIL is the only hard failure (1);
					// WARN/SKIPPED are reported outcomes (0) — the JSON payload
					// itself is the report. Both paths share verifyExitCode so
					// they can never drift.
					if verifyExitCode(v.Verdict) != 0 {
						fatal("verify: FAIL — see JSON output above")
					}
					return
				}
				fmt.Println(verification.RenderCompact(v))
				// Calibration (Feature Batch C): aggregate confidence line on the
				// non-pass path too (best-effort; omitted when there is no data).
				if line := app.VerifyConfidenceLine(p.Root()); line != "" {
					fmt.Println(line)
				}
				if verifyExitCode(v.Verdict) != 0 {
					fatal("verification FAILED — see report above; fix failing checks and rerun kern verify")
				}
				// WARN/SKIPPED are reported outcomes, not failures: exit 0 and
				// never print "verification FAILED" for them (F7).
				fmt.Println(verifyOutcomeLine(v))
				return
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
			if v.Dependency.Skipped != "" {
				fmt.Printf("dependency: SKIPPED %s\n", v.Dependency.Skipped)
			} else {
				st := "OK"
				if !v.Dependency.OK {
					st = "FAIL"
				}
				fmt.Printf("dependency: %s nodes=%d edges=%d\n", st, v.Dependency.GraphNodes, v.Dependency.GraphEdges)
				for _, fd := range v.Dependency.Findings {
					fmt.Printf("  - %s\n", fd)
				}
			}
		}
		if v.CVE != nil {
			if v.CVE.Status == "SKIPPED" {
				fmt.Printf("cve: SKIPPED %s\n", v.CVE.Detail)
			} else {
				st := "OK"
				if !v.CVE.OK {
					st = "FAIL"
				}
				fmt.Printf("cve: %s vulnerabilities=%d\n", st, v.CVE.Count)
				for i, fd := range v.CVE.Findings {
					if i >= 10 {
						fmt.Printf("  ... and %d more vulnerabilities\n", len(v.CVE.Findings)-10)
						break
					}
					fmt.Printf("  - %s %s: %s\n", fd.ID, fd.Module, fd.Summary)
				}
			}
		}
		if v.License != nil {
			if v.License.Skipped != "" {
				fmt.Printf("license: SKIPPED %s\n", v.License.Skipped)
			} else {
				st := "OK"
				if !v.License.OK {
					st = "FAIL"
				}
				fmt.Printf("license: %s modules=%d\n", st, len(v.License.Modules))
				for _, m := range v.License.Modules {
					fmt.Printf("  - %s: %s\n", m.Module, m.License)
				}
				for i, fd := range v.License.Findings {
					if i >= 10 {
						fmt.Printf("  ... and %d more license findings\n", len(v.License.Findings)-10)
						break
					}
					fmt.Printf("  ! %s\n", fd)
				}
			}
		}
		if v.Secrets != nil {
			if v.Secrets.Status == "SKIPPED" {
				fmt.Printf("secrets: SKIPPED %s\n", v.Secrets.Detail)
			} else {
				st := "OK"
				if !v.Secrets.OK {
					st = "FAIL"
				}
				fmt.Printf("secrets: %s findings=%d\n", st, v.Secrets.Count)
				if v.Secrets.Detail != "" {
					fmt.Printf("  %s\n", v.Secrets.Detail)
				}
				for i, fd := range v.Secrets.Findings {
					if i >= 10 {
						fmt.Printf("  ... and %d more findings\n", len(v.Secrets.Findings)-10)
						break
					}
					fmt.Printf("  - %s %s:%d [%s] %s\n", fd.Commit, fd.File, fd.Line, fd.Kind, fd.Snippet)
				}
			}
		}
		// Calibration (Feature Batch C): append the aggregate confidence line at
		// the end of the rendered report (best-effort; omitted when the model has
		// no data or a read fails).
		if line := app.VerifyConfidenceLine(p.Root()); line != "" {
			fmt.Println(line)
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
	rep := verification.Sorted(verification.Verify(ix, root, string(b)))
	if f.json {
		rep.Version = version
		printJSON(rep)
		if !rep.OK {
			fatal("verify: %d unverifiable/missing references — see JSON output above", len(rep.Missing))
		}
		return
	}
	fmt.Println(verification.Render(rep))
	if !rep.OK {
		fatal("verify: %d unverifiable/missing references", len(rep.Missing))
	}
}

// runCheckDraft implements `kern check-draft <file|-> [root] [--lang LANG]`:
// validate a draft code snippet against the project index. The MCP tool
// kern_check_draft is the primary surface (this thin CLI form exists so the
// opencode plugin, which shells out to the CLI, can reach the same check).
func runCheckDraft(rest []string) int {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	root := projectRoot(f)
	in := "-"
	if f.file != "" {
		in = f.file
	}
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
	findings := verification.CheckDraft(ix, root, b, f.lang)
	if len(findings) == 0 {
		fmt.Println("OK: draft validates cleanly — no issues found")
		return 0
	}
	for _, fd := range findings {
		fmt.Printf("draft.go:%d [%s] %s\n", fd.Line, fd.Kind, fd.Message)
	}
	fmt.Printf("%d issue(s) found\n", len(findings))
	// Issues found is a failure for CI-style consumers; previously the
	// command exited 0 despite reporting issues.
	return 1
}

func runChanges(cmd string, rest []string) {
	f, args := parseFlagsOrDie(rest)
	root := projectRoot(f)
	if f.root == "" && len(args) > 0 {
		root = args[0]
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
			// non-empty path, with empty/zero values. Slices are initialized
			// (not nil) so they marshal as [] instead of null — strict
			// parsers (and agents calling .map()) choke on null arrays
			// (QA Pick #15, F-RV2).
			printJSON(&intel.ChangesReport{Files: []string{}, Changes: []intel.Change{}})
			return
		}
		// Clean tree is a success for CI: nothing to report.
		fmt.Println("no changed files (clean)")
		return
	}
	if cmd == "review" {
		// The JSON surface is report-only: compute the risk report inline and
		// emit it without the presentation filters (lens/profile/runtime) —
		// the same contract as the text path, minus the render.
		report := intel.AnalyzeChangesRanged(ix, changes)
		if f.json {
			// --json is honored here too (same shape as `kern changes --json`),
			// not silently dropped for the markdown view.
			printJSON(report)
			if report.TotalRisk > 0 {
				fatalPolicy("review: %d changed file(s) with risk (total %.1f) — exit 3 (policy family); see JSON output above", len(report.Changes), report.TotalRisk)
			}
			return
		}
		// The render + filter orchestration (runtime overlay, lens, profile)
		// lives in the app layer (ReviewChanges) — this shell only prints and
		// applies the policy-family exit contract.
		report, out, err := app.ReviewChanges(ix, changes, app.ReviewOptions{
			Max:     f.max,
			Runtime: f.runtime,
			Lens:    f.lens,
			Profile: f.profile,
			Root:    root,
		})
		if err != nil {
			fatal("%v", err)
		}
		fmt.Println(out)
		if report.TotalRisk > 0 {
			fatalPolicy("%d changed file(s) with risk (total %.1f); exit 3 (policy family)", len(report.Changes), report.TotalRisk)
		}
		return
	}
	report := intel.AnalyzeChangesRanged(ix, changes)
	if f.json {
		printJSON(report)
		if report.TotalRisk > 0 {
			fatalPolicy("changes: %d changed file(s) with risk (total %.1f) — exit 3 (policy family); see JSON output above", len(report.Changes), report.TotalRisk)
		}
		return
	}
	fmt.Println(intel.RenderChanges(report))
	if report.TotalRisk > 0 {
		fatalPolicy("%d changed file(s) with risk (total %.1f); exit 3 (policy family)", len(report.Changes), report.TotalRisk)
	}

}

// symbolDegrade handles a plan/analyze/impact that could not resolve the
// free-text change to a concrete symbol (dogfooding F12). The planners are
// symbol-index-bound: instead of a bare `no symbol named "X"` error we degrade
// gracefully with an actionable message — close symbol candidates from the
// index (when any exist) and a pointer to `kern search` so the caller can find
// the concrete symbol and re-run the command with it. Deterministic, no LLM.
// Returns false when the error is not a symbol-resolution miss, so the caller
// surfaces the original error unchanged.
func symbolDegrade(cmd, change, root string, err error) bool {
	msg := err.Error()
	if !strings.Contains(msg, "no symbol named") &&
		!strings.Contains(msg, "could not identify a symbol") &&
		!strings.Contains(msg, "not found in graph") {
		return false
	}
	var b strings.Builder
	fmt.Fprintf(&b, "no matching symbol for %q — kern %s works against concrete symbols in the index\n", change, cmd)
	if ix, ierr := loadOrBuild(root); ierr == nil {
		if cands := intel.RankedSearch(ix, change, 8); len(cands) > 0 {
			b.WriteString("close candidates:\n")
			for _, c := range cands {
				fmt.Fprintf(&b, "  %-10s %-7s %-24s %s:%d\n", c.Kind, c.Lang, c.FullName(), c.File, c.Line)
			}
		}
	}
	fmt.Fprintf(&b, "hint: run `kern search %q` to find the concrete symbol, then re-run `kern %s <symbol>`", change, cmd)
	fatal("%s", b.String())
	return true
}

func runCorrelate(rest []string) {
	f, args := parseFlagsOrDie(rest)
	root := projectRoot(f)
	if len(args) < 1 || args[0] == "" {
		fatalUsage("usage: kern correlate <alert-json> [--root ROOT]")
	}
	var al domain.Alert
	alertText := args[0]
	// Accept a file path to the alert JSON as well as inline JSON — mirror
	// the kern incident handler so both commands share the same UX;
	// a literal path previously produced a cryptic "invalid character '/'".
	if _, serr := os.Stat(alertText); serr == nil {
		if b, rerr := os.ReadFile(alertText); rerr == nil {
			alertText = string(b)
		}
	}
	if err := json.Unmarshal([]byte(alertText), &al); err != nil {
		fatal("invalid alert JSON (pass JSON inline or a file path): %v", err)
	}
	p, err := app.New(root)
	if err != nil {
		fatal("Correlate: %v", err)
	}
	ts := app.NewTaskService(p, eventbus.New()).WithPRProvider(app.AutoPRProvider())
	// --code: extend the runtime correlation with the incident→twin→code
	// correlation report (Feature Batch D).
	if f.code {
		t, _, text, err := ts.CorrelateCode(al)
		if err != nil {
			fatal("Correlate: %v", err)
		}
		fmt.Print(text)
		fmt.Printf("\n[task: %s — state: %s]\n", t.ID, t.State)
		return
	}
	t, _, text, err := ts.Correlate(al)
	if err != nil {
		fatal("Correlate: %v", err)
	}
	fmt.Print(text)
	fmt.Printf("\n[task: %s — state: %s]\n", t.ID, t.State)
}

func runLearn(rest []string) {
	f, args := parseFlagsOrDie(rest)
	root := projectRoot(f)
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
	f, args := parseFlagsOrDie(rest)
	root := projectRoot(f)
	if f.root == "" && len(args) > 0 && args[0] != "" {
		root = args[0]
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
	f, args := parseFlagsOrDie(rest)
	root := projectRoot(f)
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
