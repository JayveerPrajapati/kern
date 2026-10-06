package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/agent"
	"github.com/JayveerPrajapati/kern/internal/app"
	"github.com/JayveerPrajapati/kern/internal/config"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/draft"
	"github.com/JayveerPrajapati/kern/internal/eval"
	"github.com/JayveerPrajapati/kern/internal/eventbus"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/intel"
	"github.com/JayveerPrajapati/kern/internal/metrics"
	"github.com/JayveerPrajapati/kern/internal/ownership"
	"github.com/JayveerPrajapati/kern/internal/profiles"
	"github.com/JayveerPrajapati/kern/internal/skills"
	"github.com/JayveerPrajapati/kern/internal/tasklife"
	"github.com/JayveerPrajapati/kern/internal/verdict"
	"github.com/JayveerPrajapati/kern/internal/verification"
	"github.com/JayveerPrajapati/kern/internal/whatif"

	"github.com/JayveerPrajapati/kern/internal/bpcli/mcp"
	"path/filepath"
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
	// plan honors --json (it renders the structured domain.Plan); the risk
	// lens prints a deterministic text report. The shared parser accepted
	// --json for risk but the command ignored it — reject loudly instead of
	// silently dropping the flag.
	if (cmd == "risk" || f.lens == "risk") && f.json {
		fatal("--json is only supported for kern plan")
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
		ts := tasklife.NewTaskService(p, eventbus.New()).WithPRProvider(tasklife.AutoPRProvider())
		_, text, err := ts.Risk(change)
		if err != nil {
			// Audit L7: the error already carries a "risk:" prefix (Platform.Risk
			// wraps with fmt.Errorf("risk: %w", ...)) — a "Risk:" label here
			// doubled it into "kern: Risk: risk: …". Single prefix only.
			fatal("%v", err)
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
		ts := tasklife.NewTaskService(p, eventbus.New()).WithPRProvider(tasklife.AutoPRProvider()).WithTaskPersistence(f.task != "")
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
			// --json renders the structured domain.Plan (same shape as the
			// taskful Plan workflow's artifact) so agents can parse it.
			if f.json {
				printJSON(map[string]any{
					"task_id": t.ID,
					"state":   t.State,
					"plan":    plan,
				})
				return
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
		// --json renders the structured domain.Plan assembled by the
		// stateless path (same shape as the taskful Plan workflow's
		// artifact) so agents can parse it.
		if f.json {
			printJSON(map[string]any{"plan": app.BuildStatelessPlan(change, pkt, root)})
			return
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

func runExecute(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	root := projectRoot(f)
	if len(args) < 1 || args[0] == "" {
		fatalUsage("usage: kern execute <patch|patch-file> [--root ROOT] — sandbox-only: nothing is applied to the working tree")
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
	ts := tasklife.NewTaskService(p, eventbus.New()).WithAgentID("cli").WithPRProvider(tasklife.AutoPRProvider())
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
		ts := tasklife.NewTaskService(p, eventbus.New()).WithPRProvider(tasklife.AutoPRProvider())
		_, text, err := ts.Risk(change)
		if err != nil {
			if symbolDegrade("risk", change, root, err) {
				return
			}
			// Audit L7: the error already carries a "risk:" prefix — a "Risk:"
			// label here doubled it ("kern: Risk: risk: …"). Single prefix only.
			fatal("%v", err)
		}
		fmt.Print(text)
		return
	}
	// Kern impact now produces the 11-question deterministic ImpactReport
	// via TaskService.Impact (graph-driven, no LLM). The what-if kind/new-target
	// args are still honored for backward compatibility but the primary output is
	// the structured impact report. Task persistence is gated on --task (F9):
	// only an explicit --task asks for an authoritative persisted record.
	ts := tasklife.NewTaskService(p, eventbus.New()).WithPRProvider(tasklife.AutoPRProvider()).WithTaskPersistence(f.task != "")
	var impactOpts []tasklife.ImpactOption
	if f.precision == "strict" {
		// Strict precision: skip call edges whose caller language is not
		// "resolved"-precision in the index (they are unknown, not guessable).
		impactOpts = append(impactOpts, tasklife.ImpactStrict())
	}
	if f.runtime {
		// --runtime folds runtime evidence (data stores, related incidents,
		// architecture rules) into the impact report. Opt-in: the context
		// packet + memory recall phases it runs dominate impact latency, so
		// the default graph-only report stays fast (P0 #2).
		impactOpts = append(impactOpts, tasklife.ImpactRuntime())
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

// verifyExitCode maps a verification verdict to its process exit code:
// FAIL is the only hard failure (1); WARN and SKIPPED
// are reported outcomes and exit 0; PASS/PASS_WITH_WARNING exit 0. The --json
// and text paths share this single mapping so they can never drift.
func verifyExitCode(v verdict.Verdict) int {
	if v == verdict.VerdictFail {
		return 1
	}
	return 0
}

// verifyOutcomeLine returns the human-readable outcome line for a
// verification verdict (F7/F19): "verification FAILED" is reserved for a FAIL
// verdict; WARN and SKIPPED are reported outcomes with their own wording, and
// a SKIPPED verdict names the reason (e.g. "govulncheck not installed",
// "license manifest missing") when one is recorded.
func verifyOutcomeLine(v verdict.VerificationResult) string {
	switch v.Verdict {
	case verdict.VerdictFail:
		return "verification FAILED — see report above; fix failing checks and rerun kern verify"
	case verdict.VerdictWarn:
		return "verification WARNED — see report above; address the warnings and rerun kern verify"
	case verdict.VerdictSkipped:
		line := "verification SKIPPED — missing tools or dependencies are not a hard failure"
		if reasons := verifySkippedReasons(&v); len(reasons) > 0 {
			line += ": " + strings.Join(reasons, "; ")
		}
		return line + " (install the tool or provide the manifest, then rerun kern verify)"
	case verdict.VerdictPass, verdict.VerdictPassWithWarning:
		return "verification PASSED — see report above"
	default:
		return "verification incomplete (" + string(v.Verdict) + ") — see report above"
	}
}

// verifySkippedReasons collects the first-line SKIPPED reasons from a
// verification result's sub-checks (govulncheck absent, license/dependency
// manifest missing, unisolated test set, ...), so a SKIPPED outcome line
// explains why instead of a bare verdict.
func verifySkippedReasons(v *verdict.VerificationResult) []string {
	if v == nil {
		return nil
	}
	var reasons []string
	collect := func(s string) {
		if s = strings.TrimSpace(strings.SplitN(s, "\n", 2)[0]); s != "" {
			reasons = append(reasons, s)
		}
	}
	if v.CVE != nil && v.CVE.Status == verdict.StatusSkipped {
		collect(v.CVE.Detail)
	}
	if v.License != nil && v.License.Skipped != "" {
		collect(v.License.Skipped)
	}
	if v.Dependency != nil && v.Dependency.Skipped != "" {
		collect(v.Dependency.Skipped)
	}
	if v.Secrets != nil && v.Secrets.Status == verdict.StatusSkipped {
		collect(v.Secrets.Detail)
	}
	if v.UnitTests != nil && v.UnitTests.Status == verdict.StatusSkipped {
		collect(v.UnitTests.Output)
	}
	if v.Integration != nil && v.Integration.Status == verdict.StatusSkipped {
		collect(v.Integration.Output)
	}
	return reasons
}

// verifyTestsDetail returns the detail lines printed after the tests status
// line in the default `kern verify` render (F13): nothing for a green run
// (passing-test spam suppressed), the failing tests' excerpts for a failed
// one, and a pointer to the audit log that holds the FULL output whenever
// the engine wrote one.
func verifyTestsDetail(t *verdict.TestResult) []string {
	var lines []string
	if !t.OK && strings.TrimSpace(t.Output) != "" {
		if x := verdict.TestFailureExcerpt(t.Output, 40); x != "" {
			lines = append(lines, x)
		}
	}
	if t.LogPath != "" {
		lines = append(lines, "full log: "+t.LogPath)
	}
	return lines
}

// containsVerifyTestType reports whether the requested verify types include
// the test step. The substring match mirrors the engine's type dispatcher
// (test/unit/integration), so `--types "build,unit"` and positional
// "build,test" both count. Drives the -short/--full mode note: a build-only
// or compliance-only run has no test step to run short, so no note prints.
func containsVerifyTestType(types []string) bool {
	for _, t := range types {
		tl := strings.ToLower(strings.TrimSpace(t))
		if strings.Contains(tl, "test") || strings.Contains(tl, "unit") || strings.Contains(tl, "integration") {
			return true
		}
	}
	return false
}

// dropVerifyTestTypes removes the test-step entries (test/unit/integration)
// from a verify types list — the --fast build-only fallback when there are
// no changed Go packages to test. Explicit compliance checks added via
// --cve/--license/--secrets survive. The substring match mirrors
// containsVerifyTestType, so the two can never disagree about what counts
// as the test step.
func dropVerifyTestTypes(types []string) []string {
	out := make([]string, 0, len(types))
	for _, t := range types {
		tl := strings.ToLower(strings.TrimSpace(t))
		if strings.Contains(tl, "test") || strings.Contains(tl, "unit") || strings.Contains(tl, "integration") {
			continue
		}
		out = append(out, t)
	}
	return out
}

// isTestdataPath reports whether path lives under a testdata/ directory
// (fixtures — they never run in the test step, so their changes must not
// scope a verify run to a package that only touched fixtures).
func isTestdataPath(path string) bool {
	return path == "testdata" || strings.HasPrefix(path, "testdata/") || strings.Contains(path, "/testdata/")
}

// changedSincePackages derives the Go packages whose files changed since a
// git ref (committed after the ref, uncommitted, or untracked), for
// `kern verify --changed-since <ref>`. It mirrors verification.ChangedTestPackages'
// package derivation (each changed .go file's dir → ./<dir>, root-package
// files → ./) over the UNION of `git diff --name-only <ref> --` (tracked
// changes since the ref, including uncommitted ones) and
// `git status --porcelain -uall` (untracked files). testdata/ paths are
// skipped, and files that no longer exist on disk (deleted) are dropped so
// the scope never names a nonexistent package. An invalid ref returns an
// error (the caller reports it as a usage error, exit 2); an empty result
// means "no Go changes since the ref".
func changedSincePackages(root, ref string) ([]string, error) {
	// Validate the ref up front: a typo'd ref would otherwise read as "no
	// changes" (git diff against an empty tree) and silently run the full
	// suite — the caller must reject it as a usage error instead.
	if _, err := mcp.GitOutput(root, "rev-parse", "--verify", "--quiet", ref+"^{commit}"); err != nil {
		return nil, fmt.Errorf("invalid git ref %q", ref)
	}
	out, err := mcp.GitOutput(root, "diff", "--name-only", ref, "--")
	if err != nil {
		return nil, err
	}
	files := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		if p := strings.TrimSpace(line); p != "" {
			files[p] = true
		}
	}
	status, err := mcp.GitOutput(root, "status", "--porcelain", "-uall")
	if err != nil {
		return nil, err
	}
	for _, line := range strings.Split(status, "\n") {
		if len(line) < 4 {
			continue
		}
		path := strings.TrimSpace(line[3:])
		// strip rename "old -> new": the new path is the one that exists
		if i := strings.Index(path, " -> "); i >= 0 {
			path = path[i+4:]
		}
		files[path] = true
	}
	seen := map[string]bool{}
	for path := range files {
		if !strings.HasSuffix(path, ".go") {
			continue
		}
		if isTestdataPath(path) {
			continue
		}
		// Deleted files: the diff lists them, but a package whose .go files
		// no longer exist on disk must not be part of the scope.
		if _, serr := os.Stat(filepath.Join(root, filepath.FromSlash(path))); serr != nil {
			continue
		}
		dir := filepath.Dir(filepath.ToSlash(path))
		if dir == "." {
			dir = "" // root-package files
		}
		pkg := "./" + dir
		if !seen[pkg] {
			seen[pkg] = true
		}
	}
	pkgs := make([]string, 0, len(seen))
	for pkg := range seen {
		pkgs = append(pkgs, pkg)
	}
	sort.Strings(pkgs)
	return pkgs, nil
}

func runVerify(rest []string) {
	if runVerifyCommand(rest) {
		return
	}
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
	// A bare `kern verify` (no checks requested at all) would default to a full
	// build+test run (~minutes) before reporting anything — a usage error
	// instead, consistent with search/explore/impact (audit L1). The mode flags
	// above and the compliance trio below are explicit requests and keep working
	// with no positional.
	if f.types == "" && len(args) == 0 && !f.fast && !f.cve && !f.license && !f.secrets && f.changedSince == "" {
		fatalUsage("usage: kern verify [<types>|<file|->] [--types T] [--root ROOT]\n"+
			"  no checks requested — try 'kern verify build,test', or run your own gate with\n"+
			"  'kern verify --command \"go test ./...\"'; valid types: %s", verifyTypeList())
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
		// H4: --types values must map to real checks. An unknown value was
		// previously split and passed to the engine, which ran nothing and reported
		// a false-green "verdict: PASS / insufficient data" (exit 0). Reject it
		// loudly as a usage error (exit 2).
		for _, t := range types {
			if !validVerifyType(t) {
				fatalUsage("invalid --types value '%s' (valid: %s)", t, verifyTypeList())
			}
		}
		// --fast: pre-commit tier — build + changed-package tests only.
		// It skips security/architecture/dependency/reuse/e2e/static/perf
		// and keeps the -short suite (the default). --fast wins over any
		// explicit type list; the opt-in compliance flags (--cve/--license/
		// --secrets) below still append. --full beats --fast: when both are
		// given, --fast is ignored entirely (full suite, default scope).
		fastMode := f.fast && !f.full
		if fastMode {
			types = []string{"build", "test"}
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
		// Test-step mode (P1): the default test step runs `go test -short`
		// so a bare `kern verify` finishes in ~1min instead of ~4; --full
		// opts into the COMPLETE suite. -short is the default and may be
		// passed explicitly; --full wins when both are given. The visible
		// mode note teaches the lever. An explicit KERN_VERIFY_TEST env /
		// verify.test config override replaces the test command verbatim
		// (the engine honors it first), so the note says so instead of
		// claiming a mode that does not apply. The note never pollutes
		// --json output (machine-readable contract).
		var verifyOpts []verification.Option
		if !f.json && containsVerifyTestType(types) {
			if override := config.String(root, "KERN_VERIFY_TEST", "verify.test", ""); override != "" {
				fmt.Println("tests: using KERN_VERIFY_TEST / verify.test override (not -short/--full)")
			} else if f.full {
				verifyOpts = append(verifyOpts, verification.FullTests(true))
				fmt.Println("full suite (short mode: kern verify -short)")
			} else if fastMode {
				fmt.Println("fast mode (build + changed tests)")
			} else {
				fmt.Println("short mode (full suite: kern verify --full)")
			}
		}
		p, perr := app.New(root)
		if perr != nil {
			fatal("%v — run kern index to rebuild it", perr)
		}
		ts := tasklife.NewTaskService(p, nil).WithPRProvider(tasklife.AutoPRProvider())
		// --changed: scope the test step to packages with uncommitted
		// changes vs HEAD (incremental verification; the 2-minute full
		// suite stays the default for --full). --fast implies the same
		// scoping (fastMode is false when --full is present, so the
		// combination stays a --full run). No changed Go packages: --changed
		// falls back to the default scope; the fast tier falls back to
		// build-only — never a silent empty run.
		if (f.changed || fastMode) && containsVerifyTestType(types) {
			if pkgs := verification.ChangedTestPackages(root); len(pkgs) > 0 {
				verifyOpts = append(verifyOpts, verification.TestPackages(pkgs))
				if !f.json {
					fmt.Printf("changed packages (%d): %s\n", len(pkgs), strings.Join(pkgs, " "))
				}
			} else if fastMode {
				types = dropVerifyTestTypes(types)
				if !f.json {
					fmt.Println("no changed Go packages — running build only")
				}
			} else if !f.json {
				fmt.Println("no changed Go packages — running the default scope")
			}
		}
		// --changed-since <ref>: CI-oriented scoping — verify exactly the
		// packages touched since a git ref (committed after the ref,
		// uncommitted, or untracked .go files), with the static-analysis
		// check (vet) added so the run covers build+vet+test for the
		// changed surface. A ref with no Go changes since it prints
		// NO-GO-CHANGES and exits 0 — a clean diff is a success for CI,
		// never a fall-through to the full ./... suite.
		if f.changedSince != "" {
			pkgs, err := changedSincePackages(root, f.changedSince)
			if err != nil {
				fatalUsage("verify: %v", err)
			}
			if len(pkgs) == 0 {
				fmt.Printf("NO-GO-CHANGES since %s — nothing to verify\n", f.changedSince)
				return
			}
			verifyOpts = append(verifyOpts, verification.TestPackages(pkgs))
			types = append(types, "static-analysis")
			if !f.json {
				fmt.Printf("changed packages since %s (%d): %s\n", f.changedSince, len(pkgs), strings.Join(pkgs, " "))
			}
		}
		verifyStart := time.Now()
		_, v, err := ts.Verify(types, verifyOpts...)
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
				fmt.Println(verdict.RenderCompact(v))
				// Calibration (Feature Batch C): aggregate confidence line on the
				// non-pass path too (best-effort; omitted when there is no data).
				if line := tasklife.VerifyConfidenceLine(p.Root()); line != "" {
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
			if v.Build.OK && strings.HasPrefix(v.Build.Output, verdict.SkipPrefix) {
				// A build that was NOT executed (no supported project type,
				// D1): render the explicit skip, never a contradictory "OK".
				// The reason already appears in the summary line above, so the
				// detail line carries only the status (no duplication).
				fmt.Println("build: SKIPPED")
			} else {
				st := "FAIL"
				if v.Build.OK {
					st = "OK"
				}
				fmt.Printf("build: %s (duration %s)\n", st, v.Build.Duration)
				if out := clipText(v.Build.Output, 500); out != "" {
					fmt.Println(out)
				}
			}
		}
		if v.UnitTests != nil {
			switch {
			case v.UnitTests.Status == verdict.StatusSkipped, v.UnitTests.Status == verdict.StatusNoRunner:
				// F3: a not-executed suite reports SKIPPED, never "OK passed=0".
				fmt.Printf("tests: SKIPPED %s\n", verdict.FirstLine(v.UnitTests.Output))
			case v.UnitTests.Status == verdict.StatusWarn:
				// F3: a diagnostic-only run (zero failed tests) reports WARN.
				fmt.Printf("tests: WARN passed=%d failed=%d skipped=%d (duration %s)\n", v.UnitTests.Passed, v.UnitTests.Failed, v.UnitTests.Skipped, v.UnitTests.Duration)
				if out := clipText(v.UnitTests.Output, 500); out != "" {
					fmt.Println(out)
				}
			default:
				st := "FAIL"
				if v.UnitTests.OK {
					st = "OK"
				}
				fmt.Printf("tests: passed=%d failed=%d skipped=%d %s (duration %s)\n", v.UnitTests.Passed, v.UnitTests.Failed, v.UnitTests.Skipped, st, v.UnitTests.Duration)
				// F13: the default render shows counts plus the FAILING
				// tests' excerpts only — passing-test spam (=== RUN /
				// --- PASS lines) is suppressed, and the audit log holds
				// the full output for anyone who needs it.
				for _, ln := range verifyTestsDetail(v.UnitTests) {
					fmt.Println(ln)
				}
			}
		}
		if v.Security != nil {
			st := "FAIL"
			if v.Security.OK {
				st = "OK"
			}
			detail := fmt.Sprintf("security: %s findings=%d critical=%d high=%d low=%d", st, v.Security.Count, v.Security.Critical, v.Security.High, v.Security.Low)
			if v.Security.Suppressed > 0 {
				detail += fmt.Sprintf(" (%d suppressed)", v.Security.Suppressed)
			}
			fmt.Println(detail)
			for i, fd := range v.Security.Findings {
				if i >= 10 {
					break
				}
				if fd.Suppressed {
					fmt.Printf("  - [suppressed] %s:%d [%s] %s: %s — %s\n", fd.File, fd.Line, fd.Severity, fd.Rule, fd.Message, fd.SuppressionReason)
				} else {
					fmt.Printf("  - %s:%d [%s] %s: %s\n", fd.File, fd.Line, fd.Severity, fd.Rule, fd.Message)
				}
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
		if line := tasklife.VerifyConfidenceLine(p.Root()); line != "" {
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
		// Dogfooding A2-1b: `kern verify <unknown-positional>` used to fall
		// into the claims-file form and fail with a bare "open bogus: no such
		// file" — no hint that a check-TYPES value was intended. When the
		// positional is not an existing file and does not look like a path
		// (no separator), point at the types form explicitly.
		if !strings.ContainsAny(in, "/\\") {
			fatal("Verify: %v — did you mean a check type? run `kern verify build,test` (or `kern verify --types build,test`); valid types: %s", err, verifyTypeList())
		}
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
	findings := draft.CheckDraft(ix, root, b, f.lang)
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
				fatalFindings("review: %d changed file(s) with risk (total %.1f) — exit 1 (findings); see JSON output above", len(report.Changes), report.TotalRisk)
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
			fatalFindings("%d changed file(s) with risk (total %.1f); exit 1 (findings)", len(report.Changes), report.TotalRisk)
		}
		return
	}
	report := intel.AnalyzeChangesRanged(ix, changes)
	if f.json {
		printJSON(report)
		if report.TotalRisk > 0 {
			fatalFindings("changes: %d changed file(s) with risk (total %.1f) — exit 1 (findings); see JSON output above", len(report.Changes), report.TotalRisk)
		}
		return
	}
	fmt.Println(intel.RenderChanges(report))
	if report.TotalRisk > 0 {
		fatalFindings("%d changed file(s) with risk (total %.1f); exit 1 (findings)", len(report.Changes), report.TotalRisk)
	}

}

// symbolDegrade handles a plan/analyze/impact that could not resolve the
// free-text change to a concrete symbol. The planners are
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
	ts := tasklife.NewTaskService(p, eventbus.New()).WithPRProvider(tasklife.AutoPRProvider())
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
	ts := tasklife.NewTaskService(p, eventbus.New()).WithPRProvider(tasklife.AutoPRProvider())
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
	ts := tasklife.NewTaskService(p, eventbus.New()).WithPRProvider(tasklife.AutoPRProvider())
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
	ts := tasklife.NewTaskService(p, eventbus.New()).WithPRProvider(tasklife.AutoPRProvider())
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
