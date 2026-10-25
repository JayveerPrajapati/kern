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

	"github.com/JayveerPrajapati/kern/internal/cliflags"
	"github.com/JayveerPrajapati/kern/internal/cliout"
	"github.com/JayveerPrajapati/kern/internal/gitrun"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/mcpcatalog"
	"github.com/JayveerPrajapati/kern/internal/verdict"
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
// runVerify implements `kern verify` — the default verification path. The
// three execution forms were extracted to verify_modes.go: the
// silent-orchestrator/harness modes (runVerifyMode), the high-level types
// form (runVerifyTypesForm) and the classic claims-file form
// (runVerifyClaimsForm). This dispatcher owns the mode gate, the bare
// `kern verify` usage check and the types-vs-claims disambiguation, then
// delegates. The exit-code contract (2 usage / 1 runtime) is unchanged.
func runVerify(rest []string) {
	f, args, err := parseVerifyFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	root := verifyRoot(f)
	// Silent-orchestrator/harness modes are self-contained: each handled
	// mode returns or fatals. Only when none is set do we fall through to
	// the types/claims forms.
	if runVerifyMode(f, args, root) {
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
		runVerifyTypesForm(f, args, root)
		return
	}
	runVerifyClaimsForm(f, args, root)
}
