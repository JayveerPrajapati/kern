package verifycmd

// verify_modes.go holds the three `kern verify` execution forms extracted
// from runVerify (the silent-orchestrator/harness modes, the high-level
// types form, and the classic claims-file form). Each was moved verbatim —
// only the enclosing control flow (the mode-count guard, the form
// disambiguation) stays in runVerify, which now delegates. The exit-code
// contract (2 usage / 1 runtime) is unchanged and pinned by the moved
// cmd/kern tests.

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/app"
	"github.com/JayveerPrajapati/kern/internal/claims"
	"github.com/JayveerPrajapati/kern/internal/cliflags"
	"github.com/JayveerPrajapati/kern/internal/config"
	"github.com/JayveerPrajapati/kern/internal/eval"
	"github.com/JayveerPrajapati/kern/internal/gitrun"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/mcpcatalog"
	"github.com/JayveerPrajapati/kern/internal/metrics"
	"github.com/JayveerPrajapati/kern/internal/skills"
	"github.com/JayveerPrajapati/kern/internal/tasklife"
	"github.com/JayveerPrajapati/kern/internal/verdict"
	"github.com/JayveerPrajapati/kern/internal/verification"
)

// indexOnlyVerifyTypes reports whether every requested check is served by the
// index alone. Those checks spawn no build/test process and need none of the
// application Platform's twin extractors, memory store, firewall, or audit
// replay — constructing it was ~3 s of the machine-wide pre-commit hook.
func indexOnlyVerifyTypes(types []string) bool {
	if len(types) == 0 {
		return false
	}
	for _, t := range types {
		switch strings.ToLower(strings.TrimSpace(t)) {
		case "security", "sec", "architecture", "arch", "dependency", "depend", "dep", "reuse":
		default:
			return false
		}
	}
	return true
}

// runVerifyMode handles the silent-orchestrator and harness verification
// modes (--verify-pipeline / --verify-silent / --verify-token-reduction /
// --eval / --skill / --scan). Exactly one mode per invocation — mixing them
// is a usage error, not a best-effort union. It reports whether a mode
// matched (and was fully handled, including any fatal); when no mode is set
// it returns false and the caller falls through to the types/claims forms.
func runVerifyMode(f verifyFlags, args []string, root string) bool {
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
			return true
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
		return true
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
				return true
			}
			rate := 0.0
			if rep.Symbols > 0 {
				rate = float64(rep.Silent) / float64(rep.Symbols)
			}
			fmt.Printf("silent orchestration: %d/%d symbols silent (%.0f%%)\n", rep.Silent, rep.Symbols, rate*100)
			for _, fnd := range rep.Findings {
				fmt.Printf("  - %s (%s): %s\n", fnd.Symbol, fnd.File, strings.Join(fnd.Reasons, "; "))
			}
			return true
		}
		ok, reasons := verification.VerifySilentOrchestration(root, symbol)
		if f.json {
			printJSON(map[string]any{"version": version, "ok": ok, "reasons": reasons})
			if !ok {
				fatal("verify-silent: orchestration leak detected — see JSON output above")
			}
			return true
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
		return true
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
			return true
		}
		fmt.Printf("score: %.2f\n", res.Score)
		fmt.Printf("token_reduction: %.2f\n", res.TokenReduction)
		fmt.Printf("evidence_retention: %.2f\n", res.EvidenceRetention)
		fmt.Printf("error_rate: %.2f\n", res.ErrorRate)
		fmt.Printf("reproducible: %t\n", res.Reproducible)
		fmt.Printf("samples: %d\n", len(res.Samples))
		return true
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
			return true
		}
		fmt.Printf("score: %.2f\n", res.Score)
		fmt.Printf("token_reduction: %.2f\n", res.TokenReduction)
		fmt.Printf("evidence_retention: %.2f\n", res.EvidenceRetention)
		fmt.Printf("error_rate: %.2f\n", res.ErrorRate)
		fmt.Printf("reproducible: %t\n", res.Reproducible)
		fmt.Printf("samples: %d\n", len(res.Samples))
		return true
	case f.skillDir != "":
		// --skill DIR: validate every portable skill (each immediate
		// subdirectory containing a SKILL.md). The Skill struct carries no
		// signature field, so only ValidateSkill applies.
		skillList, err := skills.LoadSkillsFromDir(f.skillDir)
		if err != nil {
			fatal("Skills: %v", err)
		}
		if len(skillList) == 0 {
			// Deep-verify L2 F2-10: a directory with no loadable skills used
			// to verify as a silent no-op (exit 0, no output). A wrong path or
			// a dir without SKILL.md subdirectories must fail loudly instead
			// of "verifying" nothing.
			fatal("verify: no skills found in %q — expected a directory whose immediate subdirectories each contain a SKILL.md", f.skillDir)
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
			return true
		}
		if len(invalid) > 0 {
			fatal("verify: %d invalid skill(s) — see output above", len(invalid))
		}
		return true
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
			return true
		}
		fmt.Printf("silent scan: %s — %d/%d symbols clean, %d violations\n", rep.Path, rep.Silent, rep.Symbols, rep.Violations)
		for _, fi := range rep.Findings {
			fmt.Printf("  - %s (%s): %s\n", fi.Symbol, fi.File, strings.Join(fi.Reasons, "; "))
		}
		fmt.Printf("- scanned %d symbols (limit 200)\n", rep.Symbols)
		return true
	}
	return false
}

// runVerifyTypesForm implements the high-level `kern verify <types>` form
// (or `kern verify` with no positional, defaulting to build,test). It runs
// the requested checks through the task service and renders the typed
// verdict, honoring --json, --fast/--full, the compliance trio and the
// changed/staged scoping flags.
// fastTypes applies the --fast tier to the requested type set. The implicit
// default becomes build+test (the fast tier's scope); an explicit list is
// returned unchanged so a requested check is never silently dropped.
func fastTypes(types []string, explicit bool) []string {
	if explicit {
		return types
	}
	return []string{"build", "test"}
}

// hasClaimType reports whether the requested types include the claims gate.
func hasClaimType(types []string) bool {
	for _, t := range types {
		if strings.EqualFold(strings.TrimSpace(t), "claims") {
			return true
		}
	}
	return false
}

// runClaimsGate runs the deterministic claims gate and renders its report.
// A FAIL exits 1 (the findings tier); a SKIP never fails the run but is always
// printed with its reason, so an unexercised claim cannot look like a pass.
func runClaimsGate(root string, jsonOut bool) {
	rep := claims.Run(root)
	if jsonOut {
		printJSON(rep)
		if !rep.OK {
			fatal("verify claims: %d claim(s) FAILED — the artifact does not meet its public claims", rep.Failed)
		}
		return
	}
	for _, c := range rep.Claims {
		fmt.Println(c.String())
		for _, e := range c.Evidence {
			fmt.Println("    " + e)
		}
	}
	fmt.Printf("claims: %d passed, %d failed, %d skipped (%s)\n",
		len(rep.Claims)-rep.Failed-rep.Skipped, rep.Failed, rep.Skipped, rep.Elapsed)
	if !rep.OK {
		fatal("verify claims: %d claim(s) FAILED — the artifact does not meet its public claims", rep.Failed)
	}
}

func runVerifyTypesForm(f verifyFlags, args []string, root string) {
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
		if !catalog.KnownVerifyType(t) {
			fatalUsage("invalid --types value '%s' (valid: %s)", t, strings.Join(catalog.VerifyTypesKnown, ", "))
		}
	}
	// The claims gate proves the public product claims (blast-radius
	// reference evidence, audit-chain integrity, guard gate corpus, tool
	// catalog contract). It is not an engine check and does not merge into a
	// Code-verification verdict, so it runs on its own: mixing it with build/
	// test would produce two independent outcomes behind one exit code.
	if hasClaimType(types) {
		if len(types) > 1 {
			fatalUsage("verify: \"claims\" runs the claims gate on its own — run `kern verify claims` and your other checks as separate commands")
		}
		runClaimsGate(root, f.json)
		return
	}
	// --fast: pre-commit tier — build + changed-package tests only.
	// It scopes the IMPLICIT default (build+test) and skips the heavier
	// checks then. An EXPLICIT type list is honored verbatim: `kern verify
	// build,security,architecture --fast` must not silently drop security
	// and architecture and report a green PASS (2026-10-26 audit). The
	// opt-in compliance flags below still append. --full beats --fast: when
	// both are given, --fast is ignored entirely (full suite, default scope).
	fastMode := f.fast && !f.full
	explicitTypes := f.types != "" || len(args) > 0
	if fastMode {
		types = fastTypes(types, explicitTypes)
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
	// Verification runs build/test commands (arbitrary host code), but the
	// CLI surface is the local OPERATOR — a trusted principal. Governance
	// gates live at the MCP (agent) boundary, never on the CLI (the
	// established internal/mcp mutation/refactor pattern). The agent
	// surface stays fail-closed: the MCP kern_verify types path gates the
	// command-executing types (build/test/e2e/static-analysis/performance)
	// via governance.CheckExecCommand at internal/mcp/highlevel/highlevel.go
	// before any check runs, and `kern verify --command` keeps its own gate
	// (verify_command.go). This CLI path must NOT require the exec
	// allowlist: the machine-wide pre-commit hook runs exactly this code
	// (`kern verify --staged security,architecture`) from every git commit,
	// so an allowlist requirement here blocks commits on any host that does
	// not export KERN_ALLOW_EXEC.
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
	if !f.json && catalog.ContainsVerifyTestType(types) {
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
	// --changed: scope the test step to packages with uncommitted
	// changes vs HEAD (incremental verification; the 2-minute full
	// suite stays the default for --full). --fast implies the same
	// scoping (fastMode is false when --full is present, so the
	// combination stays a --full run). No changed Go packages: --changed
	// falls back to the default scope; the fast tier falls back to
	// build-only — never a silent empty run.
	// --staged with NOTHING staged (a manual run on a clean tree, or the
	// fresh-checkout CI case): say so explicitly instead of letting the
	// run look like a staged-diff gate. --staged scopes only the TEST step;
	// the later branch drops that step and runs build only. Never printed in
	// --json mode (machine-readable contract).
	if f.staged && !f.json {
		if out, err := gitrun.Output(root, "diff", "--cached", "--name-only", "--"); err == nil && strings.TrimSpace(out) == "" {
			fmt.Println("nothing staged — the test step has no staged scope")
		}
	}
	if (f.changed || fastMode || f.staged) && catalog.ContainsVerifyTestType(types) {
		var pkgs []string
		if f.staged {
			pkgs = stagedPackages(root)
		} else {
			pkgs = verification.ChangedTestPackages(root)
		}
		if len(pkgs) > 0 {
			verifyOpts = append(verifyOpts, verification.TestPackages(pkgs))
			if !f.json {
				scope := "changed packages"
				if f.staged {
					scope = "staged packages"
				}
				fmt.Printf("%s (%d): %s\n", scope, len(pkgs), strings.Join(pkgs, " "))
			}
		} else if fastMode || f.staged {
			types = catalog.DropVerifyTestTypes(types)
			if len(types) == 0 {
				// An empty types list means "defaults", not "nothing":
				// without this the dropped test step silently comes back as
				// the default build+test pair (QA 2026-10-26).
				types = []string{"build"}
			}
			if !f.json {
				scope := "no changed Go packages"
				if f.staged {
					scope = "no staged Go packages"
				}
				fmt.Printf("%s — running build only\n", scope)
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
	// Index-only check types (security/architecture/dependency/reuse) never
	// touch the twin extractors, memory store, firewall, or audit replay that
	// app.New builds — that Platform construction was ~3 s of the 15 s
	// pre-commit hook. Build the verification engine directly over the index;
	// the full Platform is only for checks that actually need it (the pre-commit hook regression).
	var v verdict.VerificationResult
	var err error
	verifyStart := time.Now()
	if indexOnlyVerifyTypes(types) {
		ix, ierr := index.LoadOrBuild(root)
		if ierr != nil {
			fatal("%v — run kern index to rebuild it", ierr)
		}
		eng := verification.NewEngineWithIndex(root, ix)
		for _, o := range verifyOpts {
			o(eng)
		}
		v = eng.Verify(types)
	} else {
		p, perr := app.New(root)
		if perr != nil {
			fatal("%v — run kern index to rebuild it", perr)
		}
		ts := tasklife.NewTaskService(p, nil).WithPRProvider(tasklife.AutoPRProvider())
		_, v, err = ts.Verify(types, verifyOpts...)
	}
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
				// itself is the report. Both paths share VerifyExitCode so
				// they can never drift.
				if VerifyExitCode(v.Verdict) != 0 {
					fatal("verify: FAIL — see JSON output above")
				}
				return
			}
			fmt.Println(verdict.RenderCompact(v))
			// Calibration (Feature Batch C): aggregate confidence line on the
			// non-pass path too (best-effort; omitted when there is no data).
			if line := tasklife.VerifyConfidenceLine(root); line != "" {
				fmt.Println(line)
			}
			if VerifyExitCode(v.Verdict) != 0 {
				fatal("verification FAILED — see report above; fix failing checks and rerun kern verify")
			}
			// WARN/SKIPPED are reported outcomes, not failures: exit 0 and
			// never print "verification FAILED" for them (F7).
			fmt.Println(VerifyOutcomeLine(v))
			return
		}
		fatal("Verify: %v", err)
	}
	if f.json {
		v.Version = version
		printJSON(v)
		// The JSON payload IS the report, but a FAIL verdict must still be the
		// process outcome: the machine-wide pre-commit hook gates on the exit
		// status, so a rendered FAIL with exit 0 lets a critical secret commit
		// through (2026-10-28 adversarial audit P0-1). Same mapping as the
		// error branch above — never a second, drifting exit contract.
		if VerifyExitCode(v.Verdict) != 0 {
			fatal("verify: FAIL — see JSON output above")
		}
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
			for _, ln := range VerifyTestsDetail(v.UnitTests) {
				fmt.Println(ln)
			}
		}
	}
	if v.Security != nil {
		st := securityStatusLabel(v.Security)
		detail := fmt.Sprintf("security: %s findings=%d critical=%d high=%d low=%d", st, v.Security.Count, v.Security.Critical, v.Security.High, v.Security.Low)
		if v.Security.Suppressed > 0 {
			detail += fmt.Sprintf(" (%d suppressed)", v.Security.Suppressed)
		}
		fmt.Println(detail)
		// Live findings first (F6, mirrors verdict.RenderCompact): raw
		// scan order could fill the visible window entirely with
		// [suppressed] entries while the actionable findings never
		// printed (observed live: 19 suppressed crowded out all 67
		// actionable ones). Stable-partition so unsuppressed findings
		// render first, and say how many were cut by the cap.
		visible := v.Security.Findings
		if len(visible) > 10 {
			part := make([]verdict.Finding, 0, len(visible))
			for _, fd := range visible {
				if !fd.Suppressed {
					part = append(part, fd)
				}
			}
			for _, fd := range visible {
				if fd.Suppressed {
					part = append(part, fd)
				}
			}
			visible = part
		}
		for i, fd := range visible {
			if i >= 10 {
				fmt.Printf("  ... and %d more findings\n", len(visible)-10)
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
	if line := tasklife.VerifyConfidenceLine(root); line != "" {
		fmt.Println(line)
	}
	// The report is fully rendered above; the verdict now decides the process
	// outcome. FAIL → exit 1 so `kern verify --staged security,architecture`
	// (the global pre-commit hook) actually blocks the commit; WARN/SKIPPED
	// stay 0 (reported outcomes, F7). P0-1, 2026-10-28 adversarial audit.
	if VerifyExitCode(v.Verdict) != 0 {
		fatal("verification FAILED — see report above; fix failing checks and rerun kern verify")
	}
}

// runVerifyClaimsForm implements the classic claims-verification form:
// `kern verify <file|-> [root]` verifies a file (or stdin) of agent
// output/references against the symbol index and renders the result.
func runVerifyClaimsForm(f verifyFlags, args []string, root string) {
	in := "-"
	if len(args) > 0 && args[0] != "" {
		in = args[0]
		args = args[1:]
	}
	if len(args) > 0 {
		root = args[0]
	}
	var b []byte
	var err error
	if in == "-" {
		b, err = cliflags.ReadStdin()
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
		// (no separator), point at the types form explicitly and exit 2
		// (usage error — the same mistyped value via --types exits 2 too;
		// blind-critique Phase 3 unified them). Path-shaped positionals
		// keep the read-failure exit 1 below.
		if !strings.ContainsAny(in, "/\\") {
			fatalUsage("Verify: %v — did you mean a check type? run `kern verify build,test` (or `kern verify --types build,test`); valid types: %s", err, strings.Join(catalog.VerifyTypesKnown, ", "))
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
