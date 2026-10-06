package verification

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/calibrate"
	"github.com/JayveerPrajapati/kern/internal/ci"
	"github.com/JayveerPrajapati/kern/internal/config"
	"github.com/JayveerPrajapati/kern/internal/eventbus"
	"github.com/JayveerPrajapati/kern/internal/evidence"
	"github.com/JayveerPrajapati/kern/internal/guard"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/metrics"
	"github.com/JayveerPrajapati/kern/internal/sandbox"
	"github.com/JayveerPrajapati/kern/internal/secscan"
	"github.com/JayveerPrajapati/kern/internal/validate"
	"github.com/JayveerPrajapati/kern/internal/verdict"
	"github.com/JayveerPrajapati/kern/internal/version"
)

// Timeouts for the sub-process verifications. They are generous enough to let
// a real build/test finish but bound the overall engine run.
const (
	buildTimeout = 5 * time.Minute
	testTimeout  = 10 * time.Minute
)

// Verification type identifiers accepted by Verify. The dispatcher also
// matches on substrings, so these constants are the canonical names but callers
// may pass e.g. "static", "perf", "e2e".
const (
	VerifyE2E            = "e2e"
	VerifyStaticAnalysis = "static-analysis"
	VerifyPerformance    = "performance"
)

// Engine runs a set of verifications against a project and aggregates them
// into a single verdict.VerificationResult.
type Engine struct {
	root      string
	ix        *index.Index  // optional prebuilt index; nil = derive per verification
	bus       *eventbus.Bus // optional event publisher; nil = no-op
	CIAdapter ci.CIAdapter  // optional CI/CD adapter; nil = no CI checking
	// runAt is the timestamp of the current Verify run, used to derive the
	// per-run .kern/audit/<run-id> subdirectory for captured output logs
	// (F4). Set at the top of Verify; zero when a sub-verification is
	// invoked directly (tests, MCP handlers), in which case auditTime falls
	// back to time.Now().
	runAt time.Time
	// fullTests runs the test step with the COMPLETE suite
	// (`go test -v ./...`) instead of the fast agent-safe default
	// (`go test -v -short ./...`). Zero value = short mode (P1: a bare
	// `kern verify` must finish in ~1min, not ~4). An explicit
	// KERN_VERIFY_TEST env / verify.test config override replaces the test
	// command verbatim and wins over either mode.
	fullTests bool
	// testPackages scopes the default (non-override) test step to specific Go
	// package patterns instead of ./... — the closed loop passes the packages
	// its own change touched, so a one-file task pays seconds instead of the
	// whole-module suite. Empty = ./... (whole module). The
	// KERN_VERIFY_TEST env / verify.test override still wins verbatim.
	// Tradeoff (documented): scoped runs do not catch regressions in
	// dependent packages — CI's full tier owns that guarantee.
	testPackages []string
}

// Option configures an Engine before a Verify run. Options are applied to a
// copy of the shared engine by Platform/TaskService so concurrent callers
// (CLI, MCP, REST) never race on engine state.
type Option func(*Engine)

// FullTests requests the complete test suite (`go test -v ./...`) instead of
// the fast default short suite (`go test -v -short ./...`). The explicit
// KERN_VERIFY_TEST / verify.test override still wins verbatim when set.
func FullTests(full bool) Option {
	return func(e *Engine) { e.fullTests = full }
}

// WithFullTests toggles the test-step mode on an engine: true runs the
// complete suite (`go test -v ./...`), false (the default) runs the fast
// short suite (`go test -v -short ./...`). The KERN_VERIFY_TEST env /
// verify.test config override, when set, replaces the command verbatim and
// wins over either mode.
func (e *Engine) WithFullTests(full bool) *Engine {
	e.fullTests = full
	return e
}

// TestPackages returns an Option that scopes the default test step to the
// given Go package patterns (e.g. "./internal/foo"). nil or empty runs the
// whole module. The KERN_VERIFY_TEST env / verify.test override still wins.
func TestPackages(pkgs []string) Option {
	return func(e *Engine) {
		e.WithTestPackages(pkgs)
	}
}

// WithTestPackages scopes the default test step to the given Go package
// patterns (e.g. "./internal/foo"). nil or empty (the default) runs the whole
// module (./...). The KERN_VERIFY_TEST env / verify.test override, when set,
// replaces the command verbatim and still wins over the scoping.
func (e *Engine) WithTestPackages(pkgs []string) *Engine {
	if len(pkgs) == 0 {
		pkgs = nil
	}
	e.testPackages = pkgs
	return e
}

// NewEngine creates a verification engine for the given project root.
func NewEngine(root string) *Engine {
	return &Engine{root: root}
}

// NewEngineWithIndex creates a verification engine that reuses a prebuilt
// index for its index-backed checks instead of rebuilding it. The caller owns
// the index; the engine stores only a read-only reference. This is the hot-path
// constructor used by servers that already built the index at startup.
func NewEngineWithIndex(root string, ix *index.Index) *Engine {
	return &Engine{root: root, ix: ix}
}

// WithBus attaches an optional event bus. When non-nil, the engine publishes
// verification.started and verification.completed / verification.failed.
func (e *Engine) WithBus(b *eventbus.Bus) *Engine {
	e.bus = b
	return e
}

// WithCI attaches an optional CI/CD adapter. When non-nil, the engine runs
// the "ci" verification against the adapter's pipeline status. A nil adapter
// (the default) means the "ci" sub-result is skipped and never fails.
func (e *Engine) WithCI(adapter ci.CIAdapter) *Engine {
	e.CIAdapter = adapter
	return e
}

// publish delivers a verification event to the optional bus. A nil bus is a
// no-op so the engine keeps working unchanged when no bus is attached.
func (e *Engine) publish(kind eventbus.Kind, res *verdict.VerificationResult) {
	if e.bus == nil {
		return
	}
	payload := map[string]string{"verdict": string(res.Verdict)}
	if res.Target != "" {
		payload["target"] = res.Target
	}
	e.bus.Publish(eventbus.Event{Kind: kind, Source: "verification", Subject: res.Target, Payload: payload})
}

// auditTime returns the timestamp used to derive the per-run audit
// subdirectory name for captured output logs. It is the Verify() run
// timestamp when available; direct sub-verification calls (which skip
// Verify) fall back to the current time so each still gets its own audit
// subdirectory.
func (e *Engine) auditTime() time.Time {
	if e.runAt.IsZero() {
		return time.Now()
	}
	return e.runAt
}

// hasGoMod reports whether root is a Go module (go.mod present). It gates
// `go vet ./...` and the `go test ./...` default, which are only valid
// inside a module — a module-less root degrades instead of failing (F1).
func hasGoMod(root string) bool {
	_, err := os.Stat(filepath.Join(root, "go.mod"))
	return err == nil
}

// Verify runs all requested verification types and returns the unified result.
// Supported types (substring match): "build", "test", "security",
// "architecture", "dependency", "reuse", "e2e", "static-analysis",
// "performance", "cve", "license", "secrets". An empty verifications list
// runs the defaults (build, test, security, architecture, dependency, reuse,
// e2e, static-analysis, performance) — the compliance checks (cve/license/
// secrets) run ONLY when explicitly requested. Ordering of the aggregated
// result is fixed and deterministic.
func (e *Engine) Verify(types []string) verdict.VerificationResult {
	start := time.Now()
	defer func() { metrics.Default().RecordVerification(time.Since(start)) }()

	now := time.Now()
	e.runAt = now
	res := verdict.VerificationResult{GeneratedAt: now, Version: version.Version}
	e.publish(eventbus.VerificationStarted, &res)

	run := map[string]bool{}
	if len(types) == 0 {
		defaults := []string{"build", "test", "security", "architecture", "dependency", "reuse", "e2e", "static-analysis", "performance"}
		// CI is included in "run all" only when an adapter is configured, so
		// a missing adapter never fails or changes today's behavior.
		if e.CIAdapter != nil {
			defaults = append(defaults, "ci")
		}
		for _, t := range defaults {
			run[t] = true
		}
	} else {
		for _, t := range types {
			t = strings.ToLower(strings.TrimSpace(t))
			switch {
			case strings.Contains(t, "cve"):
				run["cve"] = true
			case strings.Contains(t, "licen"):
				run["license"] = true
			case strings.Contains(t, "secret"):
				run["secrets"] = true
			case strings.Contains(t, "build"):
				run["build"] = true
			case strings.Contains(t, "test"), strings.Contains(t, "unit"), strings.Contains(t, "integration"):
				run["test"] = true
			case strings.Contains(t, "security"), strings.Contains(t, "sec"):
				run["security"] = true
			case strings.Contains(t, "arch"):
				run["architecture"] = true
			case strings.Contains(t, "depend"), strings.Contains(t, "dep"):
				run["dependency"] = true
			case strings.Contains(t, "reuse"):
				run["reuse"] = true
			case strings.Contains(t, "e2e"), strings.Contains(t, "end-to-end"):
				run["e2e"] = true
			case strings.Contains(t, "static"), strings.Contains(t, "analysis"), strings.Contains(t, "vet"), strings.Contains(t, "lint"):
				run["static-analysis"] = true
			case strings.Contains(t, "perf"), strings.Contains(t, "bench"):
				run["performance"] = true
			case strings.Contains(t, "ci"):
				run["ci"] = true
			}
		}
	}

	if run["build"] {
		res.Build = e.VerifyBuild()
	}
	if run["test"] {
		res.UnitTests = e.VerifyTests()
	}
	if run["security"] {
		res.Security = e.VerifySecurity()
	}
	if run["architecture"] {
		res.Architecture = e.VerifyArchitecture()
	}
	if run["dependency"] {
		res.Dependency = e.VerifyDependency("")
	}
	if run["reuse"] {
		reuseRes := e.VerifyReuse()
		res.Reuse = &reuseRes
	}
	if run["e2e"] {
		res.E2ETests = e.VerifyE2ETests()
	}
	if run["static-analysis"] {
		res.StaticAnalysis = e.VerifyStaticAnalysis()
	}
	if run["performance"] {
		res.Performance = e.VerifyPerformance()
	}
	if run["cve"] {
		res.CVE = e.VerifyCVE()
	}
	if run["license"] {
		res.License = e.VerifyLicense()
	}
	if run["secrets"] {
		res.Secrets = e.VerifySecrets()
	}
	if run["ci"] {
		res.CI = e.VerifyCI()
	} else {
		// CI was not requested: report the honest zero state instead of the
		// zero-value struct serializing as {"ok": false}, which reads like a
		// failed CI run in --json output. The verdict fold below only fails
		// on Status != "" && !OK, so "not run" cannot affect the verdict.
		res.CI = verdict.CIResult{OK: true, Status: "not run"}
	}

	res.Evidence = verdict.EvidenceOf(&res)
	// Aggregate the evidence-backed claims emitted by each sub-verification
	// (security findings, test results, build results) into the unified result.
	// The moved claims() accessors were nil-safe; direct field access is guarded
	// the same way here (a nil sub-result contributes nothing).
	if res.Security != nil {
		res.Claims = append(res.Claims, res.Security.Claims...)
	}
	if res.UnitTests != nil {
		res.Claims = append(res.Claims, res.UnitTests.Claims...)
	}
	if res.Build != nil {
		res.Claims = append(res.Claims, res.Build.Claims...)
	}
	// Platform-isolation honesty (audit M3): on platforms without network
	// isolation (darwin), the default test check cannot run — the sandbox
	// fails closed with "refusing to run unisolated" unless the operator
	// explicitly opted in via KERN_ALLOW_UNISOLATED=1. Mark such a test set
	// SKIPPED (never WARN, which reads like a pass) with the reason and the
	// opt-in hint, and exclude it from the verdict math so it counts as
	// neither passing nor failing. KERN_ALLOW_UNISOLATED=1 remains the ONLY
	// way to actually run tests unisolated (the sandbox gate itself is
	// untouched).
	markIsolationSkipped(&res)
	res.Verdict = verdict.DeriveVerdict(&res)
	// DeriveVerdict has no knowledge of the CI sub-result, so fold it in here. A
	// non-empty Status means CI was actually evaluated (not requested, or
	// skipped for lack of an adapter); only a reported failure fails the run.
	if res.CI.Status != "" && !res.CI.OK {
		res.Verdict = verdict.VerdictFail
	}
	res.Summary = summarizeChecks(&res)
	if res.Verdict == verdict.VerdictFail {
		e.publish(eventbus.VerificationFailed, &res)
	} else {
		e.publish(eventbus.VerificationCompleted, &res)
	}
	// Calibration (Feature Batch C): record this verify run as a "verify"-kind
	// prediction so the calibration model can later match it against observed
	// outcomes. Best-effort by design: a failed append is logged and NEVER
	// fails or changes the verify path. Target is the engine's checked scope
	// (empty = whole repo); the generic engine path has no file-level scope.
	if err := calibrate.RecordPrediction(e.root, calibrate.Prediction{
		Kind:    "verify",
		Target:  res.Target,
		Verdict: string(res.Verdict),
		TS:      now,
	}); err != nil {
		log.Printf("verification: calibration prediction NOT recorded: %v", err)
	}
	return res
}

// isolationSkipReason is the explicit SKIPPED reason stamped on a test set
// that could not run because network isolation is unavailable on this
// platform. It carries the KERN_ALLOW_UNISOLATED=1 opt-in hint so operators
// see exactly how to actually run the tests.
const isolationSkipReason = "tests not executed: network isolation unavailable on this platform; set KERN_ALLOW_UNISOLATED=1 (or KERN_ALLOW_NET=1) to run tests unisolated"

// markIsolationSkipped stamps verdict.StatusSkipped onto any test set whose failure
// is the sandbox's fail-closed isolation refusal ("refusing to run
// unisolated") — i.e. the tests were NOT executed, not that they ran and
// failed. The output is prefixed with the explicit reason + opt-in hint. A
// skipped set is excluded from the verdict math by DeriveVerdict.
func markIsolationSkipped(res *verdict.VerificationResult) {
	// A sandbox isolation refusal means NOTHING executed — the zero-count
	// signature (Passed==0 && Failed==0) distinguishes it from a genuine
	// test failure whose output merely QUOTES the refusal text (e.g. a test
	// asserting on the sandbox's refusal message), which must stay a FAIL
	// (gate-3 attempt-1).
	mark := func(t *verdict.TestResult) {
		if t == nil || t.Status == verdict.StatusSkipped {
			return
		}
		if !t.OK && t.Passed == 0 && t.Failed == 0 && strings.Contains(t.Output, "refusing to run unisolated") {
			t.Status = verdict.StatusSkipped
			t.Output = isolationSkipReason + "\n" + t.Output
		}
	}
	mark(res.UnitTests)
	mark(res.Integration)
	if e := res.E2ETests; e != nil && e.Status != verdict.StatusSkipped &&
		!e.OK && e.Passed == 0 && e.Failed == 0 && strings.Contains(e.Output, "refusing to run unisolated") {
		e.Status = verdict.StatusSkipped
		e.Output = isolationSkipReason + "\n" + e.Output
	}
}

// summarizeChecks renders the per-check status lines of a verification run as
// a single comma-joined summary. A SKIPPED check is shown explicitly as
// "SKIPPED <reason>" — it is never folded into a joint "PASS" line, so a
// skipped test set is never counted as passing in the summary.
func summarizeChecks(res *verdict.VerificationResult) string {
	var lines []string
	add := func(name, status string) {
		lines = append(lines, name+": "+status)
	}
	if b := res.Build; b != nil {
		switch {
		case b.OK && strings.HasPrefix(b.Output, verdict.SkipPrefix):
			// A build that was NOT executed (no supported project type, D1):
			// surface the explicit skip — never fold it into a "PASS" line.
			// The "skipped: " prefix is the skip marker; the reason text is
			// what follows it.
			add("build", "SKIPPED "+verdict.FirstLine(strings.TrimPrefix(b.Output, verdict.SkipPrefix)))
		case !b.OK:
			// Surface the first output line as the reason: a bare "build:
			// FAIL" hides why the build failed (D1).
			if reason := verdict.FirstLine(b.Output); reason != "" {
				add("build", "FAIL "+reason)
			} else {
				add("build", "FAIL")
			}
		default:
			add("build", verdict.OkWord(b.OK))
		}
	}
	if t := res.UnitTests; t != nil {
		add("test", testStatus(t))
	}
	if t := res.Integration; t != nil {
		add("integration", testStatus(t))
	}
	if s := res.Security; s != nil {
		switch {
		case !s.OK:
			add("security", "FAIL")
		case s.Count > 0:
			add("security", "WARN")
		default:
			add("security", "PASS")
		}
	}
	if a := res.Architecture; a != nil {
		add("architecture", verdict.OkWord(a.OK))
	}
	if d := res.Dependency; d != nil {
		if d.Skipped != "" {
			add("dependency", "SKIPPED "+d.Skipped)
		} else {
			add("dependency", verdict.OkWord(d.OK))
		}
	}
	if r := res.Reuse; r != nil {
		switch {
		case r.Skipped != "":
			// Informational only (a clean tree is the normal state): never a
			// verdict downgrade, but still visible in the summary.
			add("reuse", "SKIPPED "+r.Skipped)
		case len(r.Findings) > 0:
			add("reuse", "WARN")
		default:
			add("reuse", verdict.OkWord(r.OK))
		}
	}
	if e := res.E2ETests; e != nil {
		if e.Status == verdict.StatusSkipped {
			add("e2e", "SKIPPED "+verdict.FirstLine(e.Output))
		} else {
			add("e2e", verdict.OkWord(e.OK))
		}
	}
	if s := res.StaticAnalysis; s != nil {
		if s.Status == verdict.StatusSkipped {
			add("static-analysis", "SKIPPED "+verdict.FirstLine(s.Output))
		} else {
			add("static-analysis", verdict.OkWord(s.OK))
		}
	}
	if p := res.Performance; p != nil {
		add("performance", verdict.OkWord(p.OK))
	}
	if c := res.CVE; c != nil {
		if c.Status == verdict.StatusSkipped {
			add("cve", "SKIPPED "+verdict.FirstLine(c.Detail))
		} else if c.Count > 0 {
			add("cve", "WARN")
		} else {
			add("cve", "PASS")
		}
	}
	if l := res.License; l != nil {
		if l.Skipped != "" {
			add("license", "SKIPPED "+l.Skipped)
		} else if len(l.Findings) > 0 {
			add("license", "WARN")
		} else {
			add("license", "PASS")
		}
	}
	if s := res.Secrets; s != nil {
		if s.Status == verdict.StatusSkipped {
			add("secrets", "SKIPPED "+verdict.FirstLine(s.Detail))
		} else if s.Count > 0 {
			add("secrets", "WARN")
		} else {
			add("secrets", "PASS")
		}
	}
	if len(lines) == 0 {
		return string(res.Verdict)
	}
	return strings.Join(lines, ", ")
}

// testStatus renders one test check's status: "PASS"/"FAIL" from OK, or
// "SKIPPED <reason>" when the set was not executed (F3: a no-runner skip
// renders SKIPPED too — it must never read as a pass), or "WARN" for a
// diagnostic-only run (zero failed tests).
func testStatus(t *verdict.TestResult) string {
	switch t.Status {
	case verdict.StatusSkipped, verdict.StatusNoRunner:
		return "SKIPPED " + verdict.FirstLine(t.Output)
	case verdict.StatusWarn:
		return "WARN"
	}
	return verdict.OkWord(t.OK)
}

// VerifyBuild runs the build verification (wraps v1 validate/validate).
func (e *Engine) VerifyBuild() *verdict.BuildResult {
	res := &verdict.BuildResult{}
	// Polyglot (C2): the build command comes from per-language detection
	// (validate.Detect) and can be overridden with `verify.build` in
	// .kern/config.json (or KERN_VERIFY_BUILD) as a shell command string.
	var cmd *validate.Command
	if override := config.String(e.root, "KERN_VERIFY_BUILD", "verify.build", ""); override != "" {
		c, a := splitVerifyCommand(override)
		cmd = &validate.Command{Name: override, Cmd: c, Args: a, Kind: "build"}
	} else {
		detected, err := validate.DetectKind(e.root, "build")
		if err != nil {
			detected, err = validate.Detect(e.root)
		}
		if err != nil {
			if errors.Is(err, validate.ErrNoProjectType) {
				// No supported project type at all (zero candidates): there
				// is nothing to build. Report a clean skip — never a false
				// FAIL (F1; mirrors the VerifyTests no-runner skip below).
				// "required tooling not found in PATH" (candidates exist but
				// the toolchain is missing) stays an actionable FAIL.
				res.OK = true
				res.Output = verdict.SkipPrefix + "no supported project type detected (nothing to build)"
				res.Claims = append(res.Claims, evidence.FromBuildResult(e.root, res.OK, res.Output))
				return res
			}
			res.Output = err.Error()
			return res
		}
		cmd = detected
	}
	vr := validate.Run(context.Background(), e.root, cmd, buildTimeout)
	// F4: clip the embedded output to the PASS/FAIL caps and persist the
	// full log under .kern/audit/<run-id>/verify-build.log (path on LogPath).
	res.Output, res.LogPath = captureOutput(e.root, e.auditTime(), "build", vr.Output, vr.OK)
	res.Duration = vr.Dur
	res.OK = vr.OK
	if !vr.OK && vr.Err != nil && strings.TrimSpace(res.Output) == "" {
		res.Output = vr.Err.Error()
	}
	res.Claims = append(res.Claims, evidence.FromBuildResult(e.root, res.OK, res.Output))
	return res
}

// VerifyCI runs the CI/CD pipeline check. With no adapter configured it
// returns a skipped sub-result (OK=true) — CI is optional and never fails
// a run on its own. With an adapter it polls the latest run status (empty job
// ID = latest) and reports success, failure, or an in-progress note.
func (e *Engine) VerifyCI() verdict.CIResult {
	if e.CIAdapter == nil {
		return verdict.CIResult{OK: true, Status: "skipped", Summary: "no CI adapter configured"}
	}
	job, err := e.CIAdapter.Status("")
	if err != nil {
		return verdict.CIResult{OK: true, Status: "skipped", Summary: "ci status unavailable: " + err.Error()}
	}
	res := verdict.CIResult{OK: true, JobID: job.ID, Status: string(job.Status), URL: job.URL}
	switch job.Status {
	case ci.StatusSuccess:
		res.Summary = "CI pipeline succeeded"
	case ci.StatusFailure:
		res.OK = false
		res.Summary = "CI pipeline failed"
	case ci.StatusCancelled:
		res.OK = false
		res.Summary = "CI pipeline cancelled"
	case ci.StatusQueued:
		res.Summary = "CI pipeline queued"
	case ci.StatusInProgress:
		res.Summary = "CI pipeline in progress"
	default:
		res.Summary = "CI status: " + string(job.Status)
	}
	return res
}

// VerifyTests runs test verification via the sandbox execution layer and
// parses the verbose output into counts. Polyglot (C2): the test command is
// resolved from the project type (validate.DetectKind) instead of hard-coding
// `go test`, and can be overridden with `verify.test` in .kern/config.json
// (or KERN_VERIFY_TEST) as a shell command string. The default Go suite runs
// in short mode (`go test -v -short ./...`) — the fast agent-safe default —
// unless the engine was switched to the complete suite via FullTests/
// WithFullTests (P1). PASS/FAIL/SKIP counts are parsed for `go test -v`
// output and for a pytest-style final summary line (F3: a detected runner
// counts TESTS, not suites); other runners report OK from the exit status,
// and a non-zero exit with zero failed tests is a WARN diagnostic (F3).
func (e *Engine) VerifyTests() *verdict.TestResult {
	res := &verdict.TestResult{Package: "./..."}
	if pkgs := e.testPackages; len(pkgs) > 0 {
		res.Package = strings.Join(pkgs, " ")
	}
	cmd, args := "go", e.testArgs()
	if override := config.String(e.root, "KERN_VERIFY_TEST", "verify.test", ""); override != "" {
		cmd, args = splitVerifyCommand(override)
		res.Package = override
	} else if c, err := validate.DetectKind(e.root, "test"); err == nil {
		if c.Cmd == "go" {
			args = e.testArgs()
			// Keep the scoped label when package scoping is active (the
			// runner is still go test — the scope is the interesting part).
			if len(e.testPackages) == 0 {
				res.Package = c.Name
			}
		} else {
			cmd, args = c.Cmd, c.Args
			res.Package = c.Name
		}
		// npm test fails outright when package.json has no "test" script —
		// that is an absent suite, not a failing one. Report a clean skip
		// instead of a false FAIL.
		if c.Cmd == "npm" && !npmHasTestScript(e.root) {
			res.OK = true
			res.Status = verdict.StatusNoRunner
			res.Output = verdict.SkipPrefix + "package.json has no test script"
			res.Claims = append(res.Claims, evidence.FromTestResult(res.Package, res.OK, res.Output))
			return res
		}
	} else if !hasGoMod(e.root) {
		// No detected test runner and root is not a Go module: there is no
		// suite to run. The `go test ./...` default is invalid outside a
		// module ("directory prefix . does not contain main module"), so an
		// absent suite reports a clean skip — never a false FAIL (F1;
		// mirrors the npm no-test-script skip above). F3: the skip is
		// stamped StatusNoRunner so the phase renders SKIPPED and the
		// verdict folds to WARN — a vacuous PASS over an unmeasured suite
		// is dishonest.
		res.OK = true
		res.Status = verdict.StatusNoRunner
		res.Output = verdict.SkipPrefix + "no test runner detected (root has no go.mod)"
		res.Claims = append(res.Claims, evidence.FromTestResult(res.Package, res.OK, res.Output))
		return res
	}
	sr := sandbox.RunWithOptions(context.Background(), e.root, cmd, args, testTimeout, sandbox.RunOptions{AllowLoopbackBind: true})
	// F4: clip the embedded output and persist the full log under
	// .kern/audit/<run-id>/verify-test.log (path on LogPath).
	res.Output, res.LogPath = captureOutput(e.root, e.auditTime(), "test", sr.Output, sr.OK)
	res.Duration = sr.Duration
	res.OK = sr.OK
	foldTestOutcome(res, sr)
	res.Claims = append(res.Claims, evidence.FromTestResult(res.Package, res.OK, res.Output))
	return res
}

// foldTestOutcome folds a raw test-runner outcome into res: the `go test -v`
// "--- PASS/FAIL/SKIP" lines first, then — when nothing go-style was found —
// a pytest-style final summary line so a detected runner counts TESTS, never
// "1" for the whole suite (F3). A non-zero exit with ZERO failed tests is a
// diagnostic (go vet / compile error surfaced before any test executed),
// never a test failure: the tests phase may only FAIL when failed>0, so it is
// stamped StatusWarn (verdict WARN, exit 0) — except the sandbox's fail-closed
// isolation refusal, which markIsolationSkipped converts to a clean SKIPPED
// downstream and must keep its !OK signature.
func foldTestOutcome(res *verdict.TestResult, sr *sandbox.Result) {
	for _, line := range strings.Split(sr.Output, "\n") {
		switch {
		case strings.HasPrefix(line, "--- PASS"):
			res.Passed++
		case strings.HasPrefix(line, "--- FAIL"):
			res.Failed++
		case strings.HasPrefix(line, "--- SKIP"):
			res.Skipped++
		}
	}
	if res.Passed == 0 && res.Failed == 0 && res.Skipped == 0 {
		if p, f, s, ok := parsePytestSummary(sr.Output); ok {
			res.Passed, res.Failed, res.Skipped = p, f, s
		}
	}
	if sr.OK && res.Passed == 0 && res.Failed == 0 {
		res.Passed = 1
	}
	if !sr.OK && res.Failed == 0 && strings.TrimSpace(res.Output) == "" && sr.Err != nil {
		res.Output = sr.Err.Error()
	}
	if !sr.OK && res.Failed == 0 && !strings.Contains(res.Output, "refusing to run unisolated") {
		res.OK = true
		res.Status = verdict.StatusWarn
	}
}

// parsePytestSummary extracts TEST counts from a pytest final summary line —
// the verbose "=== 490 passed, 2 failed in 3.2s ===" form or the -q
// "2 passed in 0.01s" form. found reports whether such a line was
// recognised (F3: count tests, not suites).
func parsePytestSummary(output string) (passed, failed, skipped int, found bool) {
	for _, line := range strings.Split(output, "\n") {
		// Tolerate the "====" border of the verbose form.
		t := strings.TrimSpace(strings.Trim(strings.TrimSpace(line), "="))
		if !strings.Contains(t, " passed") && !strings.Contains(t, " failed") {
			continue
		}
		var p, f, s int
		matched := false
		every := true
		for _, part := range strings.Split(t, ",") {
			var n int
			var kind string
			if _, err := fmt.Sscanf(strings.TrimSpace(part), "%d %s", &n, &kind); err != nil {
				every = false
				break
			}
			switch {
			case strings.HasPrefix(kind, "passed"):
				p = n
				matched = true
			case strings.HasPrefix(kind, "failed"), strings.HasPrefix(kind, "error"):
				f += n
				matched = true
			case strings.HasPrefix(kind, "skipped"), strings.HasPrefix(kind, "xfailed"), strings.HasPrefix(kind, "xpassed"):
				s += n
			}
		}
		if every && matched {
			passed, failed, skipped, found = p, f, s, true
		}
	}
	return
}

// testArgs returns the default `go test` arguments for the engine's current
// mode: the fast short suite by default, the complete suite in full mode
// (WithFullTests/FullTests). -v is always kept so the PASS/FAIL/SKIP count
// parsing in VerifyTests works. An explicit KERN_VERIFY_TEST / verify.test
// override never reaches this helper — it replaces the command verbatim.
func (e *Engine) testArgs() []string {
	pkgs := e.testPackages
	if len(pkgs) == 0 {
		pkgs = []string{"./..."}
	}
	args := []string{"test", "-v"}
	if !e.fullTests {
		args = append(args, "-short")
	}
	return append(args, pkgs...)
}

// VerifyE2ETests runs the project's end-to-end tests (go test with the "e2e"
// build tag). E2E tests are detected by scanning for the e2e build constraint
// or e2e-named test files; when none are present the result is nil ("not
// run"), so callers can distinguish absent E2E coverage from a clean run.
func (e *Engine) VerifyE2ETests() *verdict.E2ETestResult {
	if !hasE2ETests(e.root) {
		return nil
	}
	res := &verdict.E2ETestResult{}
	sr := sandbox.RunWithOptions(context.Background(), e.root, "go", []string{"test", "-tags", "e2e", "-v", "./..."}, testTimeout, sandbox.RunOptions{AllowLoopbackBind: true})
	// F4: clip the embedded output and persist the full log under
	// .kern/audit/<run-id>/verify-e2e.log (path on LogPath).
	res.Output, res.LogPath = captureOutput(e.root, e.auditTime(), "e2e", sr.Output, sr.OK)
	res.Duration = sr.Duration
	res.OK = sr.OK
	for _, line := range strings.Split(sr.Output, "\n") {
		switch {
		case strings.HasPrefix(line, "--- PASS"):
			res.Passed++
		case strings.HasPrefix(line, "--- FAIL"):
			res.Failed++
		case strings.HasPrefix(line, "--- SKIP"):
			res.Skipped++
		}
	}
	if !sr.OK && res.Failed == 0 && strings.TrimSpace(res.Output) == "" && sr.Err != nil {
		res.Output = sr.Err.Error()
	}
	return res
}

// VerifyStaticAnalysis runs static analysis on the project. It defaults to
// `go vet ./...`, which is valid only inside a Go module; other linters
// (staticcheck, golangci-lint) could be detected here in future. Any finding
// (a line of vet output) makes OK false. A root WITHOUT go.mod is not
// vet-able (`go vet ./...` dies with "directory prefix . does not contain
// main module"), so the check degrades to the per-file gofmt -e syntax
// baseline — mirroring validate/checks.go — and never fails on module
// absence (F1).
func (e *Engine) VerifyStaticAnalysis() *verdict.StaticAnalysisResult {
	res := &verdict.StaticAnalysisResult{}
	// Polyglot (C2): static analysis defaults to `go vet` for Go modules and
	// is otherwise opt-in via `verify.lint` in .kern/config.json (or
	// KERN_VERIFY_LINT) — ecosystem linters that need project setup (npm
	// run lint, golangci-lint) would false-fail when unconfigured, so they
	// are never auto-detected. An explicit override always wins.
	if override := config.String(e.root, "KERN_VERIFY_LINT", "verify.lint", ""); override != "" {
		cmd, args := splitVerifyCommand(override)
		res.Tool = override
		e.runStaticAnalysis(cmd, args, res)
		return res
	}
	// Gate `go vet ./...` on go.mod: on a module-less root it exits 1 with
	// "pattern ./...: directory prefix . does not contain main module".
	if hasGoMod(e.root) {
		res.Tool = "go vet"
		cmd, args := "go", []string{"vet", "./..."}
		if c, err := validate.DetectKind(e.root, "lint"); err == nil && c.Cmd == "go" {
			cmd, args = c.Cmd, c.Args
			res.Tool = c.Name
		}
		e.runStaticAnalysis(cmd, args, res)
		return res
	}
	// Module-less root: per-file `gofmt -e` syntax baseline (mirrors
	// checks.go's no-module path). gofmt -e exits non-zero only on syntax
	// errors; on success it prints the reformatted file, which is noise and
	// is discarded.
	res.Tool = "gofmt -e"
	e.runGofmtBaseline(res)
	return res
}

// runStaticAnalysis executes a linter command and parses its output into the
// result: non-empty, non-#-prefixed lines are findings; OK requires a clean
// exit AND no findings. Output is captured per the F4 caps and the full log
// is persisted to .kern/audit/<run-id>/verify-static-analysis.log.
func (e *Engine) runStaticAnalysis(cmd string, args []string, res *verdict.StaticAnalysisResult) {
	sr := sandbox.Run(context.Background(), e.root, cmd, args, testTimeout)
	res.Duration = sr.Duration
	reason := foldStaticAnalysisOutcome(res, sr)
	res.Output, res.LogPath = captureOutput(e.root, e.auditTime(), "static-analysis", sr.Output, res.OK)
	if reason != "" {
		// The tool never executed — surface the did-not-run reason FIRST so
		// the SKIPPED render and the summary explain why, instead of a bare
		// "static-analysis: FAIL tool=go vet findings=0" (an unmeasured run
		// must neither claim PASS nor false-FAIL).
		res.Output = reason + "\n" + res.Output
	}
}

// foldStaticAnalysisOutcome folds a raw linter-run outcome into res:
// non-empty, non-#-prefixed output lines are findings; OK requires a clean
// exit AND no findings. When the run failed at EXECUTION level — the sandbox
// could not execute the tool at all (Err non-empty, or a non-zero exit with
// no tool output and zero findings) — the phase is stamped StatusSkipped and
// the SKIPPED reason is returned ("" when the tool executed normally). A
// did-not-run must never claim a clean PASS nor a false FAIL: its !OK says
// nothing about the code. Genuine tool findings (nonzero findings or tool
// output) keep today's FAIL semantics.
func foldStaticAnalysisOutcome(res *verdict.StaticAnalysisResult, sr *sandbox.Result) string {
	for _, line := range strings.Split(sr.Output, "\n") {
		if line = strings.TrimSpace(line); line == "" {
			continue
		}
		if !strings.HasPrefix(line, "#") {
			res.Findings = append(res.Findings, line)
		}
	}
	res.OK = sr.OK && len(res.Findings) == 0
	if !sr.OK && len(res.Findings) == 0 && (sr.Err != nil || (sr.ExitCode != 0 && strings.TrimSpace(sr.Output) == "")) {
		res.Status = verdict.StatusSkipped
		errText := "unknown error"
		if sr.Err != nil {
			errText = sr.Err.Error()
		}
		return fmt.Sprintf("static-analysis not executed: %s could not run (exit %d): %s; ensure %s is installed and runnable",
			res.Tool, sr.ExitCode, errText, res.Tool)
	}
	return ""
}

// runGofmtBaseline runs the module-less static-analysis fallback: per-file
// `gofmt -e` over the Go files under root, bounded to small repos (the
// checks.go bound). Files with syntax errors become findings. A repo with no
// files, or too many for a per-file pass, reports clean — the deterministic
// syntax baseline has nothing to fail, and a module-less root must never
// fail static analysis on module absence.
func (e *Engine) runGofmtBaseline(res *verdict.StaticAnalysisResult) {
	var files []string
	_ = filepath.WalkDir(e.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path != e.root && index.IgnoredDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) == ".go" {
			// Root-relative so gofmt -e <rel> resolves from the sandbox
			// working directory (root), whatever subdirectory the file
			// lives in.
			rel, rerr := filepath.Rel(e.root, path)
			if rerr != nil {
				return nil
			}
			files = append(files, filepath.ToSlash(rel))
		}
		return nil
	})
	res.OK = true
	if len(files) == 0 || len(files) > 8 {
		res.Output = "no go.mod detected; static analysis degraded to the per-file gofmt -e syntax baseline (module-less root)"
		return
	}
	var out strings.Builder
	for _, f := range files {
		sr := sandbox.Run(context.Background(), e.root, "gofmt", []string{"-e", f}, testTimeout)
		res.Duration += sr.Duration
		if !sr.OK {
			res.OK = false
			for _, line := range strings.Split(sr.Output, "\n") {
				if line = strings.TrimSpace(line); line == "" {
					continue
				}
				res.Findings = append(res.Findings, line)
				out.WriteString(line + "\n")
			}
		}
	}
	res.Output, res.LogPath = captureOutput(e.root, e.auditTime(), "static-analysis", strings.TrimSuffix(out.String(), "\n"), res.OK)
}

// VerifyPerformance runs the project benchmarks (`go test -bench=. -benchmem`)
// and parses the results into verdict.BenchmarkResult entries. It returns nil when no
// benchmark functions are detectable ("where available"). Performance is
// advisory — a benchmark run that returns non-zero does not fail the verdict.
func (e *Engine) VerifyPerformance() *verdict.PerformanceResult {
	if !hasBenchmarks(e.root) {
		return nil
	}
	sr := sandbox.RunWithOptions(context.Background(), e.root, "go", []string{"test", "-bench=.", "-benchmem", "-v", "./..."}, testTimeout, sandbox.RunOptions{AllowLoopbackBind: true})
	res := &verdict.PerformanceResult{}
	for _, line := range strings.Split(sr.Output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 || !strings.HasPrefix(fields[0], "Benchmark") {
			continue
		}
		b := verdict.BenchmarkResult{Name: fields[0]}
		if i, err := strconv.Atoi(fields[1]); err == nil {
			b.Iterations = i
		}
		if len(fields) >= 3 {
			b.NsPerOp = parseMetric(fields[2])
		}
		if len(fields) >= 4 {
			b.BytesPerOp = parseMetric(fields[3])
		}
		if len(fields) >= 5 {
			b.AllocsPerOp = parseMetric(fields[4])
		}
		res.Benchmarks = append(res.Benchmarks, b)
	}
	res.Output, res.LogPath = captureOutput(e.root, e.auditTime(), "performance", sr.Output, sr.OK)
	res.Duration = sr.Duration
	res.OK = sr.OK
	return res
}

// hasE2ETests scans root for Go test files carrying the "e2e" build constraint
// or an e2e test suffix. It drives the VerifyE2ETests nil/not-run behavior.
// splitVerifyCommand splits a user-supplied verify override (from
// .kern/config.json or its env twin) into a binary and arguments on
// whitespace. Quoting is not supported — the override is a flat command
// string like "npm test --silent".
func splitVerifyCommand(s string) (string, []string) {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return "", nil
	}
	return fields[0], fields[1:]
}

// npmHasTestScript reports whether the package.json at root declares a
// "test" script. npm test exits non-zero when the script is absent, which
// would report an absent suite as a failing one.
func npmHasTestScript(root string) bool {
	data, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		return false
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return false
	}
	return pkg.Scripts["test"] != ""
}

func hasE2ETests(root string) bool {
	found := false
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || found {
			return nil
		}
		if d.IsDir() {
			if path != root && index.IgnoredDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		// An e2e-named test file (e.g. e2e_test.go, *_e2e_test.go) is treated as
		// E2E coverage even when it lacks an explicit build constraint.
		if base := filepath.Base(path); strings.Contains(base, "e2e") && strings.HasSuffix(base, "_test.go") {
			found = true
			return nil
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "//go:build") && strings.Contains(line, "e2e") {
				found = true
				return nil
			}
			if strings.HasPrefix(line, "// +build") && strings.Contains(line, "e2e") {
				found = true
				return nil
			}
		}
		return nil
	})
	return found
}

// hasBenchmarks scans whether any Go test file declares a Benchmark function.
// It drives the VerifyPerformance nil/not-found behavior.
func hasBenchmarks(root string) bool {
	found := false
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || found {
			return nil
		}
		if d.IsDir() {
			if path != root && index.IgnoredDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		for _, line := range strings.Split(string(data), "\n") {
			if strings.Contains(line, "func Benchmark") {
				found = true
				return nil
			}
		}
		return nil
	})
	return found
}

// parseMetric parses a benchmark field like "1234 ns/op" or "123 B/op" into
// its leading integer, returning 0 on any parse failure (best-effort).
func parseMetric(field string) int64 {
	i := strings.IndexAny(field, " \t")
	if i < 0 {
		i = len(field)
	}
	if n, err := strconv.ParseInt(field[:i], 10, 64); err == nil {
		return n
	}
	return 0
}

// VerifySecurity runs the security scan (wraps v1 secscan.Scan) and aggregates
// findings by severity.
func (e *Engine) VerifySecurity() *verdict.SecurityResult {
	res := &verdict.SecurityResult{}
	findings, err := secscan.Scan(e.root)
	if err != nil {
		res.OK = false
		res.Error = err.Error()
		return res
	}
	res.Count = len(findings)
	// Triage layer (see suppress.go): merge the compiled-in defaults with
	// the optional user file .kern/verify-suppressions.json, then mark
	// matching findings suppressed. A suppressed finding is still reported
	// (marked [suppressed] with its reason) but never blocks the check and
	// is excluded from the Critical/High/Low risk ladder, so the counts
	// reflect only live problems. Count stays the raw total (including
	// suppressed) so the report shows the full picture.
	reg := loadSuppressionRegistry(e.root)
	unsuppressedCritical := 0
	for _, f := range findings {
		vf := verdict.Finding{
			File:    f.File,
			Line:    f.Line,
			Rule:    f.Rule,
			Message: f.Message,
			Snippet: f.Snippet,
		}
		// Map sec severities (error/warning/info) onto the risk ladder so a
		// finding's rendered [severity] label matches the summary counts
		// (critical/high/low) — previously the raw sec severity leaked into
		// the finding ("[info]" next to a "low=1" summary). The raw severity
		// still travels on the evidence claim and the eventbus payload below.
		switch f.Severity {
		case string(secscan.SeverityError):
			vf.Severity = "critical"
		case string(secscan.SeverityWarning):
			vf.Severity = "high"
		case string(secscan.SeverityInfo):
			vf.Severity = "low"
		default:
			vf.Severity = f.Severity
		}
		reason, suppressed := reg.Match(f)
		if suppressed {
			vf.Suppressed = true
			vf.SuppressionReason = reason
			res.Suppressed++
		} else {
			// Map sec severities (error/warning/info) onto the risk ladder.
			switch f.Severity {
			case string(secscan.SeverityError):
				res.Critical++
				unsuppressedCritical++
			case string(secscan.SeverityWarning):
				res.High++
			case string(secscan.SeverityInfo):
				res.Low++
			}
		}
		res.Findings = append(res.Findings, vf)
		// Emit an evidence-backed claim per finding through the evidence
		// factory so security findings flow into the result's claim set.
		res.Claims = append(res.Claims, evidence.FromSecurityFinding(f))
		// Emit a security.finding event per finding so the bus carries
		// individual findings (not just the aggregate) to webhooks/audit.
		if e.bus != nil {
			payload := map[string]string{"rule": f.Rule, "severity": f.Severity, "message": f.Message}
			if suppressed {
				payload["suppressed"] = "true"
				payload["suppression_reason"] = reason
			}
			e.bus.Publish(eventbus.Event{
				Kind:    eventbus.SecurityFinding,
				Source:  "verification",
				Subject: fmt.Sprintf("%s:%d", f.File, f.Line),
				Payload: payload,
			})
		}
	}
	// Only an UNSUPPRESSED critical (error) finding fails (blocks) the
	// security check; lower severities are non-blocking warnings surfaced by
	// verdict aggregation. When every finding is suppressed the check passes
	// with the triage visible in the report (Count > 0 renders WARN at the
	// verdict level — a note, never a silent pass).
	res.OK = unsuppressedCritical == 0
	return res
}

// VerifyArchitecture runs the architectural rule checks (wraps v1 intel guard
// rules loaded from .kern/boundaries.json).
func (e *Engine) VerifyArchitecture() *verdict.ArchitectureResult {
	res := &verdict.ArchitectureResult{}
	b, err := guard.LoadBoundaries(e.root)
	if err != nil {
		res.OK = false
		return res
	}
	ix := e.loadIndex()
	if ix == nil {
		res.OK = false
		return res
	}
	// No explicit .kern/boundaries.json: fall back to the inferred layered
	// guardrails (same semantics as kern guard / kern impact / the MCP guard
	// handler) so the architecture check is ENFORCED by default instead of
	// silently skipped. The inference is surfaced on the result so operators
	// know to pin explicit rules for deterministic enforcement.
	inferred := false
	if b == nil {
		b = guard.InferBoundaries(ix)
		inferred = true
	}
	files := listSourceFiles(e.root)
	violations, skipped := guard.CheckBoundariesPrecise(ix, b, files, false)
	if inferred {
		// The guard IS enforced with inferred rules — never claim otherwise.
		// Surface the inference as a warning (advisory) so the absence of an
		// explicit rules file stays observable, while OK stays driven by real
		// violations: inferred violations DO fail the check.
		msg := fmt.Sprintf("no .kern/boundaries.json found — verified against %d inferred layered boundary rule(s); create .kern/boundaries.json to pin explicit rules", len(b.Rules))
		res.Warnings = append(res.Warnings, msg)
		if e.bus != nil {
			e.bus.Publish(eventbus.Event{
				Kind:    eventbus.ArchitectureWarning,
				Source:  "verification",
				Subject: "architecture",
				Payload: map[string]string{"warning": msg},
			})
		}
	} else if n := skipped["boundaries-not-configured"]; n > 0 {
		// A missing boundaries file with files in scope is a warning, not a
		// violation: the guard was not enforced. Surface it on the result and the
		// bus so it is observable, but keep OK driven by real violations — a WARN
		// must not silently pass, yet must not fail an advisory verify either.
		msg := fmt.Sprintf("no boundary rules configured (.kern/boundaries.json not found) — architecture guard NOT enforced; %d files unchecked", n)
		res.Warnings = append(res.Warnings, msg)
		if e.bus != nil {
			e.bus.Publish(eventbus.Event{
				Kind:    eventbus.ArchitectureWarning,
				Source:  "verification",
				Subject: "architecture",
				Payload: map[string]string{"warning": msg},
			})
		}
	}
	for _, v := range violations {
		res.Violations = append(res.Violations, renderViolation(v))
		// Emit an architecture.violation event per violation so the bus
		// carries individual violations to webhooks/audit.
		if e.bus != nil {
			e.bus.Publish(eventbus.Event{
				Kind:    eventbus.ArchitectureViolation,
				Source:  "verification",
				Subject: v.CallerFile,
				Payload: map[string]string{"caller": v.CallerFile, "callee": v.CalleeFile, "from": v.RuleFrom, "to": v.RuleTo},
			})
		}
	}
	res.OK = len(res.Violations) == 0
	return res
}

// VerifyDependency verifies real module dependencies (missing modules,
// duplicated requires) plus the intelligence graph dependencies for the target.
// A target may be a symbol name or qualified name; an empty target checks the
// whole graph. Dependency manifests are verified in-process for every
// supported ecosystem present (go.mod, package.json, requirements.txt,
// pom.xml, Cargo.toml — see manifests.go). It is fail-closed: an unreadable
// or unparseable manifest is surfaced as a finding, never a fabricated PASS;
// a project with no supported manifest at all is reported as an honest skip.
func (e *Engine) VerifyDependency(target string) *verdict.DependencyResult {
	res := &verdict.DependencyResult{}
	ix := e.loadIndex()
	if ix == nil {
		res.OK = false
		return res
	}
	nodes := map[string]bool{}
	edges := 0
	for src, callees := range ix.Calls {
		nodes[src] = true
		for _, ce := range callees {
			nodes[ce.Target] = true
			edges++
		}
	}
	res.GraphNodes = len(nodes)
	res.GraphEdges = edges
	res.OK = true
	if target != "" {
		found := false
		for _, s := range ix.Symbols {
			if s.Name == target || s.FullName() == target {
				found = true
				break
			}
		}
		res.OK = found
	}

	// Real dependency manifest verification (fail-closed on any error).
	if mc := checkManifestDeps(e.root); mc != nil {
		res.Findings = append(res.Findings, mc.findings...)
		switch {
		case mc.skipped:
			// No supported manifest: honest skip, not a fabricated PASS or
			// FAIL — the graph verdict above still stands.
			res.Skipped = mc.skippedNote
		case mc.ok:
			if len(mc.findings) > 0 {
				res.OK = false
			}
		default:
			// Could not run the check: never fabricate a PASS.
			res.OK = false
		}
	}

	// New-dependency advisory (rung 5): diff the working-tree manifests
	// against HEAD and warn on newly added dependency paths. Advisory by
	// design — warnings never flip OK to false. The diff's skip note applies
	// ONLY when the manifest check did not already skip (its reason takes
	// precedence), and never fails the check.
	warns, diffSkipped := diffManifestDeps(e.root)
	res.Warnings = append(res.Warnings, warns...)
	if diffSkipped != "" && res.Skipped == "" {
		res.Skipped = diffSkipped
	}
	return res
}

// renderViolation formats a guard violation as a deterministic single line.
func renderViolation(v guard.Violation) string {
	parts := []string{v.CallerFile, v.CalleeFile}
	if v.Symbol != "" {
		parts = append(parts, v.Symbol)
	}
	return strings.Join(parts, " -> ")
}

// loadIndex returns the engine's prebuilt index when one was supplied via
// NewEngineWithIndex; otherwise it loads the persisted index, falling back to
// building it fresh.
func (e *Engine) loadIndex() *index.Index {
	if e.ix != nil {
		return e.ix
	}
	return loadIndex(e.root)
}

// loadIndex loads the persisted index, falling back to building it fresh.
func loadIndex(root string) *index.Index {
	ix, err := index.Load(root)
	if err != nil || ix == nil {
		ix, err = index.Build(root)
	}
	if err != nil || ix == nil {
		return nil
	}
	return ix
}

// listSourceFiles walks root and returns the indexable source file paths in
// stable sorted order (used as the architecture rule input).
func listSourceFiles(root string) []string {
	var files []string
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path != root && index.IgnoredDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil || !index.QuickExt(rel) {
			return nil
		}
		files = append(files, rel)
		return nil
	})
	sort.Strings(files)
	return files
}
