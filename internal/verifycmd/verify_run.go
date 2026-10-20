package verifycmd

// verify_run.go implements the `kern verify` run family — the default
// verification path (types / claims-file / incremental / silent-orchestrator
// modes) plus its verify-specific helpers. It was extracted from cmd/kern
// (workstream 3.6-A — verify family extraction) following the
// internal/approvalcli and internal/enterprise precedents: this file owns the
// family's flag parsing, rendering and internal helpers, and exports
// VerifyRun — the entrypoint that returns the process exit code (int) to the
// cmd/kern dispatch table.
//
// Exit-code contract (pinned by the moved cmd/kern tests, must survive
// EXACTLY):
//
//	usage errors  -> 2 (no stack trace)
//	runtime errors -> 1
//
// The command body keeps the house fatal style (panic with the exitError
// sentinel); VerifyRun recovers the sentinel and converts it back into the
// exit code, so cmd/kern's main() never sees a foreign panic and the
// dispatch table always receives a plain int. The `kern verify --command`
// command mode is NOT part of this file — cmd/kern's runVerifyCommand (a thin
// delegate of VerifyCommand) pre-checks it before VerifyRun is reached.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/app"
	"github.com/JayveerPrajapati/kern/internal/cliflags"
	"github.com/JayveerPrajapati/kern/internal/cliout"
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
	kversion "github.com/JayveerPrajapati/kern/internal/version"
)

// version is the build-stamped release version reported inside `kern verify`
// JSON payloads. It mirrors the cmd/kern main.go pattern: a literal "dev"
// initializer keeps the legacy `-ldflags "-X main.version=..."` rewrite
// working for the CLI binary, and init() adopts the shared
// internal/version.Version (either "dev" or the newer
// "-X .../internal/version.Version=..." stamp) when unstamped.
var version = "dev"

func init() {
	version = kversion.Adopt(version)
}

// exitError is the sentinel panic type used by fatal and fatalUsage — the
// cmd/kern helpers.go convention, preserved verbatim. It is recovered inside
// this package by run (the exported entrypoint wrapper), which converts it
// back into the process exit code.
type exitError struct{ code int }

// fatal prints an error to stderr and signals exit code 1 (the Unix
// convention for runtime errors).
func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "kern: "+format+"\n", args...)
	panic(exitError{code: 1})
}

// fatalUsage prints an error to stderr and signals exit code 2 (the Unix
// convention for usage errors). Use it for bad flags, missing required
// arguments, and unknown subcommands. It must never print a stack trace.
func fatalUsage(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "kern: "+format+"\n", args...)
	panic(exitError{code: 2})
}

// run executes a family command body, recovering the exitError sentinel and
// returning its code. Non-sentinel panics are re-panicked (a genuine bug
// must not be swallowed into an exit code).
func run(fn func() int) (code int) {
	defer func() {
		if r := recover(); r != nil {
			if e, ok := r.(exitError); ok {
				code = e.code
				return
			}
			panic(r) // re-panic non-sentinel
		}
	}()
	return fn()
}

// VerifyRun executes `kern verify [<types>|<file|->] [flags]` — the default
// verification path shared by the CLI and the tests that pin it. It returns
// the process exit code (0 ok, 1 runtime failure / FAIL verdict, 2 usage
// error). The `kern verify --command` command mode is handled by
// cmd/kern's runVerifyCommand before this entrypoint is reached.
func VerifyRun(rest []string) int {
	return run(func() int { runVerify(rest); return 0 })
}

// verifyFlags holds the parsed `kern verify` flags the verify family reads —
// the cmd/kern unified flags struct's subset for this command family. The
// parser below replicates parseFlags' documented semantics for exactly these
// flags (single-dash long forms normalize to double-dash, --flag=value and
// --flag value both work, bool flags accept --flag/--flag=true/--flag=false,
// a trailing value-less flag is accept-and-default, and unknown flags are a
// sticky parse error).
type verifyFlags struct {
	root                 string
	types                string
	changedSince         string
	evalDir              string
	skillDir             string
	scanPath             string
	json                 bool
	verifyPipeline       bool
	verifySilent         bool
	verifyTokenReduction bool
	fast                 bool
	full                 bool
	staged               bool
	cve                  bool
	license              bool
	secrets              bool
	changed              bool
}

// parseVerifyFlags parses the verify family's flags from rest, replicating
// the unified cmd/kern parser's semantics for the flags runVerify reads (and
// --short/--help, which the CLI declares for `kern verify` but the handler
// accept-and-ignores), via the shared cliflags.Parser (single-dash long forms
// normalize, --flag=value and --flag value both work, a trailing value-less
// flag is accept-and-default, unknown flags are a sticky parse error). It
// returns the parsed flags, the positional args, and the first parse error
// (sticky).
func parseVerifyFlags(args []string) (verifyFlags, []string, error) {
	var f verifyFlags
	p := cliflags.NewParser(args)
	for {
		name, inline, hasInline, ok := p.Next()
		if !ok {
			break
		}
		switch name {
		case "--root":
			p.SetStr(&f.root, inline, hasInline)
		case "--types":
			p.SetStr(&f.types, inline, hasInline)
		case "--changed-since":
			p.SetStr(&f.changedSince, inline, hasInline)
		case "--eval":
			p.SetStr(&f.evalDir, inline, hasInline)
		case "--skill":
			p.SetStr(&f.skillDir, inline, hasInline)
		case "--scan":
			p.SetStr(&f.scanPath, inline, hasInline)
		case "--json":
			p.SetBool(&f.json, "--json", inline, hasInline)
		case "--verify-pipeline":
			p.SetBool(&f.verifyPipeline, "--verify-pipeline", inline, hasInline)
		case "--verify-silent":
			p.SetBool(&f.verifySilent, "--verify-silent", inline, hasInline)
		case "--verify-token-reduction":
			p.SetBool(&f.verifyTokenReduction, "--verify-token-reduction", inline, hasInline)
		case "--fast":
			p.SetBool(&f.fast, "--fast", inline, hasInline)
		case "--full":
			p.SetBool(&f.full, "--full", inline, hasInline)
		case "--staged":
			p.SetBool(&f.staged, "--staged", inline, hasInline)
		case "--cve":
			p.SetBool(&f.cve, "--cve", inline, hasInline)
		case "--license":
			p.SetBool(&f.license, "--license", inline, hasInline)
		case "--secrets":
			p.SetBool(&f.secrets, "--secrets", inline, hasInline)
		case "--changed":
			p.SetBool(&f.changed, "--changed", inline, hasInline)
		case "--short", "--help", "-h":
			// Accepted and ignored: --short is declared for `kern verify`
			// (the test step defaults to -short already) and help flags are
			// consumed before dispatch by the CLI; neither is read here.
		default:
			p.Unknown(name)
		}
	}
	return f, p.Rest, p.Err()
}

// verifyRoot returns the command's --root value, defaulting to the current
// directory — the root-resolution preamble every subcommand repeats.
func verifyRoot(f verifyFlags) string {
	if f.root != "" {
		return f.root
	}
	return "."
}

// securityStatusLabel renders the security detail line's status word. OK
// means "not blocking" (no unsuppressed critical) — but "security: OK
// findings=86 high=67" reads as a contradiction to a human, so findings
// present without a blocking critical render WARN, matching the verdict-level
// WARN the summary line already prints.
func securityStatusLabel(s *verdict.SecurityResult) string {
	if !s.OK {
		return "FAIL"
	}
	if s.Count > 0 {
		return "WARN"
	}
	return "OK"
}

// stagedPackages derives the Go packages whose files are STAGED for commit
// (`git diff --cached --name-only`), for the pre-commit hook tier
// (`kern verify --staged`). Unlike the working-tree scope, untracked files are
// excluded — a staged commit validates exactly what will be committed. An
// empty result means "no staged Go changes"; the caller decides between
// build-only and NO-GO-CHANGES semantics.
func stagedPackages(root string) []string {
	out, err := gitrun.Output(root, "diff", "--cached", "--name-only", "--")
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		path := strings.TrimSpace(line)
		if path == "" || !strings.HasSuffix(path, ".go") {
			continue
		}
		// strip rename "old -> new": the new path is the one that exists
		if i := strings.Index(path, " -> "); i >= 0 {
			path = path[i+4:]
		}
		dir := filepath.Dir(filepath.ToSlash(path))
		if dir == "." {
			dir = "" // root-package files
		}
		seen["./"+dir] = true
	}
	pkgs := make([]string, 0, len(seen))
	for pkg := range seen {
		pkgs = append(pkgs, pkg)
	}
	sort.Strings(pkgs)
	return pkgs
}

// VerifyExitCode maps a verification verdict to its process exit code:
// FAIL is the only hard failure (1); WARN and SKIPPED
// are reported outcomes and exit 0; PASS/PASS_WITH_WARNING exit 0. The --json
// and text paths share this single mapping so they can never drift.
func VerifyExitCode(v verdict.Verdict) int {
	if v == verdict.VerdictFail {
		return 1
	}
	return 0
}

// VerifyOutcomeLine returns the human-readable outcome line for a
// verification verdict (F7/F19): "verification FAILED" is reserved for a FAIL
// verdict; WARN and SKIPPED are reported outcomes with their own wording, and
// a SKIPPED verdict names the reason (e.g. "govulncheck not installed",
// "license manifest missing") when one is recorded.
func VerifyOutcomeLine(v verdict.VerificationResult) string {
	switch v.Verdict {
	case verdict.VerdictFail:
		return "verification FAILED — see report above; fix failing checks and rerun kern verify"
	case verdict.VerdictWarn:
		return "verification WARNED — see report above; address the warnings and rerun kern verify"
	case verdict.VerdictSkipped:
		line := "verification SKIPPED — missing tools or dependencies are not a hard failure"
		if reasons := VerifySkippedReasons(&v); len(reasons) > 0 {
			line += ": " + strings.Join(reasons, "; ")
		}
		return line + " (install the tool or provide the manifest, then rerun kern verify)"
	case verdict.VerdictPass, verdict.VerdictPassWithWarning:
		return "verification PASSED — see report above"
	default:
		return "verification incomplete (" + string(v.Verdict) + ") — see report above"
	}
}

// VerifySkippedReasons collects the first-line SKIPPED reasons from a
// verification result's sub-checks (govulncheck absent, license/dependency
// manifest missing, unisolated test set, ...), so a SKIPPED outcome line
// explains why instead of a bare verdict.
func VerifySkippedReasons(v *verdict.VerificationResult) []string {
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

// VerifyTestsDetail returns the detail lines printed after the tests status
// line in the default `kern verify` render (F13): nothing for a green run
// (passing-test spam suppressed), the failing tests' excerpts for a failed
// one, and a pointer to the audit log that holds the FULL output whenever
// the engine wrote one.
func VerifyTestsDetail(t *verdict.TestResult) []string {
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
	if _, err := gitrun.Output(root, "rev-parse", "--verify", "--quiet", ref+"^{commit}"); err != nil {
		return nil, fmt.Errorf("invalid git ref %q", ref)
	}
	out, err := gitrun.Output(root, "diff", "--name-only", ref, "--")
	if err != nil {
		return nil, err
	}
	files := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		if p := strings.TrimSpace(line); p != "" {
			files[p] = true
		}
	}
	status, err := gitrun.Output(root, "status", "--porcelain", "-uall")
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

// isVerifyTypes reports whether s is a comma-separated list of known
// verification check types. Used to disambiguate the high-level
// `kern verify <types>` form from the classic claims-verification form.
func isVerifyTypes(s string) bool {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return false
	}
	for _, t := range strings.Split(trimmed, ",") {
		switch strings.TrimSpace(t) {
		case "build", "test", "security", "architecture", "dependency", "reuse", "cve", "license", "secrets":
		default:
			return false
		}
	}
	return true
}

// loadOrBuild delegates to index.LoadOrBuild — the canonical shared
// implementation. Kept as a thin wrapper for its verify-family callers.
func loadOrBuild(root string) (*index.Index, error) {
	return index.LoadOrBuild(root)
}

// clipText truncates a long string for the terminal render, appending an
// ellipsis. Same contract as the cmd/kern helper it replaces.
func clipText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.TrimSpace(s[:n]) + "…"
}

// printJSON renders v as indented JSON on stdout — the verify family's --json
// outputs. Same contract as the cmd/kern helper it replaces: a marshal
// failure is a fatal error, and oversized string fields are clipped by
// clipJSONStrings before marshaling.
func printJSON(v any) {
	if err := cliout.Print(os.Stdout, v); err != nil {
		fatal("printJSON: %v", err)
	}
}

// runVerify implements `kern verify` — the default verification path. It was
// moved verbatim from cmd/kern (workstream 3.6-A — verify family extraction);
// only the `kern verify --command` pre-check was dropped (cmd/kern's
// runVerifyCommand owns it) and the flag parser/exit hubs were swapped for
// this package's local equivalents.
func runVerify(rest []string) {
	f, args, err := parseVerifyFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	root := verifyRoot(f)
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
	if f.types == "" && len(args) == 0 && !f.fast && !f.cve && !f.license && !f.secrets && f.changedSince == "" && !f.staged {
		fatalUsage("usage: kern verify [<types>|<file|->] [--types T] [--root ROOT]\n"+
			"  no checks requested — try 'kern verify build,test', or run your own gate with\n"+
			"  'kern verify --command \"go test ./...\"'; valid types: %s", strings.Join(catalog.VerifyTypesKnown, ", "))
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
			if !catalog.KnownVerifyType(t) {
				fatalUsage("invalid --types value '%s' (valid: %s)", t, strings.Join(catalog.VerifyTypesKnown, ", "))
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
		// --staged with NOTHING staged (a manual run on a clean tree, or the
		// fresh-checkout CI case): say so explicitly instead of letting the
		// run look like a staged-diff gate. --staged scopes the TEST step;
		// security and architecture are whole-tree checks by design, so the
		// run still verifies something — the notice teaches both facts. Never
		// printed in --json mode (machine-readable contract).
		if f.staged && !f.json {
			if out, err := gitrun.Output(root, "diff", "--cached", "--name-only", "--"); err == nil && strings.TrimSpace(out) == "" {
				fmt.Println("nothing staged — the test step has no staged scope; security and architecture scan the whole tree (not just staged changes)")
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
				if line := tasklife.VerifyConfidenceLine(p.Root()); line != "" {
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
		// (no separator), point at the types form explicitly.
		if !strings.ContainsAny(in, "/\\") {
			fatal("Verify: %v — did you mean a check type? run `kern verify build,test` (or `kern verify --types build,test`); valid types: %s", err, strings.Join(catalog.VerifyTypesKnown, ", "))
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
