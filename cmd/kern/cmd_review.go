package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/agent"
	"github.com/JayveerPrajapati/kern/internal/app"
	"github.com/JayveerPrajapati/kern/internal/cliflags"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/eventbus"
	"github.com/JayveerPrajapati/kern/internal/intel"
	"github.com/JayveerPrajapati/kern/internal/ownership"
	"github.com/JayveerPrajapati/kern/internal/profiles"
	"github.com/JayveerPrajapati/kern/internal/tasklife"
	"github.com/JayveerPrajapati/kern/internal/verdict"
	"github.com/JayveerPrajapati/kern/internal/verifycmd"
	"github.com/JayveerPrajapati/kern/internal/whatif"
)

// printTaskFooter prints the task-annotation line ONLY when the Task record
// is persisted and queryable via `kern task <id>` (the store-assigned
// "t-<n>" namespace). An ephemeral "a-<n>" ID dies with the process, so
// annotating it is bookkeeping noise (QA 6.6). extra carries a per-command
// suffix (" — risk=R", " — N steps, risk=R") already prefixed with " — ".
func printTaskFooter(t *agent.Task, extra string) {
	if t == nil || !strings.HasPrefix(t.ID, "t-") {
		return
	}
	fmt.Printf("\n[task: %s — state: %s%s]\n", t.ID, t.State, extra)
}

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
			printTaskFooter(t, fmt.Sprintf(" — %d steps, risk=%s", len(plan.ImplementationSteps), plan.Risk))
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
		printTaskFooter(t, "")
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
	printTaskFooter(t, "")
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
	// Index-only fast path: the default graph impact report needs the index
	// and its code graph, never the twin extractors / memory / firewall /
	// audit replay that app.New builds (~3 s). --risk and runtime evidence
	// still need the full Platform.
	var p *app.Platform
	var err error
	if f.risk || os.Getenv("KERN_IMPACT_RUNTIME") == "1" {
		p, err = app.New(root)
	} else {
		p, err = app.NewIndexOnly(root)
	}
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
	printTaskFooter(t, " — risk="+rep.Risk)
}

// verifyExitCode maps a verification verdict to its process exit code — a
// thin delegate of verifycmd.VerifyExitCode (workstream 3.6-A — verify
// family extraction). The wrapper stays so cmd_exitcode_test.go's contract
// tests keep pinning the mapping from the CLI side.
func verifyExitCode(v verdict.Verdict) int {
	return verifycmd.VerifyExitCode(v)
}

// verifyOutcomeLine returns the human-readable outcome line for a
// verification verdict (F7/F19) — a thin delegate of
// verifycmd.VerifyOutcomeLine (workstream 3.6-A — verify family extraction).
func verifyOutcomeLine(v verdict.VerificationResult) string {
	return verifycmd.VerifyOutcomeLine(v)
}

// runVerify implements `kern verify`. The default verification path's
// business logic lives in internal/verifycmd (workstream 3.6-A — verify
// family extraction): this is a thin delegate that pre-checks command mode
// (`kern verify --command`, owned by runVerifyCommand) and forwards
// everything else to the entry point. verifycmd recovers its own exitError
// sentinel and returns the exit code; a non-zero code is re-raised here with
// cmd/kern's exitError so the pinned cmd/kern exit-contract tests
// (expectExit on runVerify) and main()'s sentinel recovery keep working
// unchanged.
func runVerify(rest []string) int {
	if runVerifyCommand(rest) {
		return 0
	}
	if code := verifycmd.VerifyRun(rest); code != 0 {
		panic(exitError{code: code})
	}
	return 0
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
		from, to := cliflags.SplitRange(f.range_)
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
		printTaskFooter(t, "")
		return
	}
	t, _, text, err := ts.Correlate(al)
	if err != nil {
		fatal("Correlate: %v", err)
	}
	fmt.Print(text)
	printTaskFooter(t, "")
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
	printTaskFooter(t, "")
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
	printTaskFooter(t, "")
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
