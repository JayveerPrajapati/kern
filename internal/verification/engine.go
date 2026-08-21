package verification

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/ci"
	"github.com/JayveerPrajapati/kern/internal/eventbus"
	"github.com/JayveerPrajapati/kern/internal/evidence"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
	"github.com/JayveerPrajapati/kern/internal/metrics"
	"github.com/JayveerPrajapati/kern/internal/sandbox"
	"github.com/JayveerPrajapati/kern/internal/sec"
	"github.com/JayveerPrajapati/kern/internal/validate"
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
// into a single VerificationResult.
type Engine struct {
	root      string
	ix        *index.Index  // optional prebuilt index; nil = derive per verification
	bus       *eventbus.Bus // optional event publisher; nil = no-op
	CIAdapter ci.CIAdapter  // optional CI/CD adapter; nil = no CI checking
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
func (e *Engine) publish(kind eventbus.Kind, res *VerificationResult) {
	if e.bus == nil {
		return
	}
	payload := map[string]string{"verdict": string(res.Verdict)}
	if res.Target != "" {
		payload["target"] = res.Target
	}
	e.bus.Publish(eventbus.Event{Kind: kind, Source: "verification", Subject: res.Target, Payload: payload})
}

// Verify runs all requested verification types and returns the unified result.
// Supported types (substring match): "build", "test", "security",
// "architecture", "dependency", "e2e", "static-analysis", "performance". An
// empty verifications list runs them all. Ordering of the aggregated result is
// fixed and deterministic.
func (e *Engine) Verify(types []string) VerificationResult {
	start := time.Now()
	defer func() { metrics.Default().RecordVerification(time.Since(start)) }()

	now := time.Now()
	res := VerificationResult{GeneratedAt: now}
	e.publish(eventbus.VerificationStarted, &res)

	run := map[string]bool{}
	if len(types) == 0 {
		defaults := []string{"build", "test", "security", "architecture", "dependency", "e2e", "static-analysis", "performance"}
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
			case strings.Contains(t, "build"):
				run["build"] = true
			case strings.Contains(t, "test"), strings.Contains(t, "unit"), strings.Contains(t, "integration"):
				run["test"] = true
			case strings.Contains(t, "security"), strings.Contains(t, "sec"):
				run["security"] = true
			case strings.Contains(t, "archi"):
				run["architecture"] = true
			case strings.Contains(t, "depend"), strings.Contains(t, "dep"):
				run["dependency"] = true
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

	var parts []string
	if run["build"] {
		res.Build = e.VerifyBuild()
		parts = append(parts, "build")
	}
	if run["test"] {
		res.UnitTests = e.VerifyTests()
		parts = append(parts, "test")
	}
	if run["security"] {
		res.Security = e.VerifySecurity()
		parts = append(parts, "security")
	}
	if run["architecture"] {
		res.Architecture = e.VerifyArchitecture()
		parts = append(parts, "architecture")
	}
	if run["dependency"] {
		res.Dependency = e.VerifyDependency("")
		parts = append(parts, "dependency")
	}
	if run["e2e"] {
		res.E2ETests = e.VerifyE2ETests()
		if res.E2ETests != nil {
			parts = append(parts, "e2e")
		}
	}
	if run["static-analysis"] {
		res.StaticAnalysis = e.VerifyStaticAnalysis()
		if res.StaticAnalysis != nil {
			parts = append(parts, "static-analysis")
		}
	}
	if run["performance"] {
		res.Performance = e.VerifyPerformance()
		if res.Performance != nil {
			parts = append(parts, "performance")
		}
	}
	if run["ci"] {
		res.CI = e.VerifyCI()
		parts = append(parts, "ci")
	}

	res.Evidence = evidenceOf(&res)
	// Aggregate the evidence-backed claims emitted by each sub-verification
	// (security findings, test results, build results) into the unified result.
	res.Claims = append(res.Claims, res.Security.claims()...)
	res.Claims = append(res.Claims, res.UnitTests.claims()...)
	res.Claims = append(res.Claims, res.Build.claims()...)
	res.Verdict = verdictOf(&res)
	// verdictOf has no knowledge of the CI sub-result, so fold it in here. A
	// non-empty Status means CI was actually evaluated (not requested, or
	// skipped for lack of an adapter); only a reported failure fails the run.
	if res.CI.Status != "" && !res.CI.OK {
		res.Verdict = VerdictFail
	}
	res.Summary = strings.Join(parts, ", ") + ": " + string(res.Verdict)
	if res.Verdict == VerdictFail {
		e.publish(eventbus.VerificationFailed, &res)
	} else {
		e.publish(eventbus.VerificationCompleted, &res)
	}
	return res
}

// VerifyBuild runs the build verification (wraps v1 validate/validate).
func (e *Engine) VerifyBuild() *BuildResult {
	res := &BuildResult{}
	cmd, err := validate.Detect(e.root)
	if err != nil {
		res.Output = err.Error()
		return res
	}
	vr := validate.Run(context.Background(), e.root, cmd, buildTimeout)
	res.Output = vr.Output
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
func (e *Engine) VerifyCI() CIResult {
	if e.CIAdapter == nil {
		return CIResult{OK: true, Status: "skipped", Summary: "no CI adapter configured"}
	}
	job, err := e.CIAdapter.Status("")
	if err != nil {
		return CIResult{OK: true, Status: "skipped", Summary: "ci status unavailable: " + err.Error()}
	}
	res := CIResult{OK: true, JobID: job.ID, Status: string(job.Status), URL: job.URL}
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

// VerifyTests runs test verification via the sandbox execution layer
// (`go test ./...`) and parses the verbose output into counts.
func (e *Engine) VerifyTests() *TestResult {
	res := &TestResult{Package: "./..."}
	sr := sandbox.Run(context.Background(), e.root, "go", []string{"test", "-v", "./..."}, testTimeout)
	res.Output = sr.Output
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
	res.Claims = append(res.Claims, evidence.FromTestResult(res.Package, res.OK, res.Output))
	return res
}

// VerifyE2ETests runs the project's end-to-end tests (go test with the "e2e"
// build tag). E2E tests are detected by scanning for the e2e build constraint
// or e2e-named test files; when none are present the result is nil ("not
// run"), so callers can distinguish absent E2E coverage from a clean run.
func (e *Engine) VerifyE2ETests() *E2ETestResult {
	if !hasE2ETests(e.root) {
		return nil
	}
	res := &E2ETestResult{}
	sr := sandbox.Run(context.Background(), e.root, "go", []string{"test", "-tags", "e2e", "-v", "./..."}, testTimeout)
	res.Output = sr.Output
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
// `go vet ./...`, which is always available for Go modules; other linters
// (staticcheck, golangci-lint) could be detected here in future. Any finding
// (a line of vet output) makes OK false.
func (e *Engine) VerifyStaticAnalysis() *StaticAnalysisResult {
	res := &StaticAnalysisResult{Tool: "go vet"}
	sr := sandbox.Run(context.Background(), e.root, "go", []string{"vet", "./..."}, testTimeout)
	res.Output = sr.Output
	res.Duration = sr.Duration
	for _, line := range strings.Split(sr.Output, "\n") {
		if line = strings.TrimSpace(line); line == "" {
			continue
		}
		if !strings.HasPrefix(line, "#") {
			res.Findings = append(res.Findings, line)
		}
	}
	res.OK = sr.OK && len(res.Findings) == 0
	return res
}

// VerifyPerformance runs the project benchmarks (`go test -bench=. -benchmem`)
// and parses the results into BenchmarkResult entries. It returns nil when no
// benchmark functions are detectable ("where available"). Performance is
// advisory — a benchmark run that returns non-zero does not fail the verdict.
func (e *Engine) VerifyPerformance() *PerformanceResult {
	if !hasBenchmarks(e.root) {
		return nil
	}
	sr := sandbox.Run(context.Background(), e.root, "go", []string{"test", "-bench=.", "-benchmem", "-v", "./..."}, testTimeout)
	res := &PerformanceResult{}
	for _, line := range strings.Split(sr.Output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 || !strings.HasPrefix(fields[0], "Benchmark") {
			continue
		}
		b := BenchmarkResult{Name: fields[0]}
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
	res.Output = sr.Output
	res.Duration = sr.Duration
	res.OK = sr.OK
	return res
}

// hasE2ETests scans root for Go test files carrying the "e2e" build constraint
// or an e2e test suffix. It drives the VerifyE2ETests nil/not-run behavior.
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

// VerifySecurity runs the security scan (wraps v1 sec.Scan) and aggregates
// findings by severity.
func (e *Engine) VerifySecurity() *SecurityResult {
	res := &SecurityResult{}
	findings, err := sec.Scan(e.root)
	if err != nil {
		res.OK = false
		res.Error = err.Error()
		return res
	}
	res.Count = len(findings)
	for _, f := range findings {
		res.Findings = append(res.Findings, Finding{
			File:     f.File,
			Line:     f.Line,
			Rule:     f.Rule,
			Severity: f.Severity,
			Message:  f.Message,
			Snippet:  f.Snippet,
		})
		// Emit an evidence-backed claim per finding through the evidence
		// factory so security findings flow into the result's claim set.
		res.Claims = append(res.Claims, evidence.FromSecurityFinding(f))
		// Map sec severities (error/warning/info) onto the risk ladder.
		switch f.Severity {
		case string(sec.SeverityError):
			res.Critical++
		case string(sec.SeverityWarning):
			res.High++
		case string(sec.SeverityInfo):
			res.Low++
		}
		// Emit a security.finding event per finding so the bus carries
		// individual findings (not just the aggregate) to webhooks/audit.
		if e.bus != nil {
			e.bus.Publish(eventbus.Event{
				Kind:    eventbus.SecurityFinding,
				Source:  "verification",
				Subject: fmt.Sprintf("%s:%d", f.File, f.Line),
				Payload: map[string]string{"rule": f.Rule, "severity": f.Severity, "message": f.Message},
			})
		}
	}
	// A critical finding fails (blocks) the security check; lower severities
	// are non-blocking warnings surfaced by verdict aggregation.
	res.OK = res.Critical == 0
	return res
}

// VerifyArchitecture runs the architectural rule checks (wraps v1 intel guard
// rules loaded from .kern/boundaries.json).
func (e *Engine) VerifyArchitecture() *ArchitectureResult {
	res := &ArchitectureResult{}
	b, err := intel.LoadBoundaries(e.root)
	if err != nil {
		res.OK = false
		return res
	}
	ix := e.loadIndex()
	if ix == nil {
		res.OK = false
		return res
	}
	files := listSourceFiles(e.root)
	violations := intel.CheckBoundaries(ix, b, files)
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
// whole graph. For Go modules the check is done in-process (see deps.go); it is
// fail-closed — an error running the check is surfaced as a finding, never a
// fabricated PASS.
func (e *Engine) VerifyDependency(target string) *DependencyResult {
	res := &DependencyResult{}
	ix := e.loadIndex()
	if ix == nil {
		res.OK = false
		return res
	}
	nodes := map[string]bool{}
	edges := 0
	for src, callees := range ix.Calls {
		nodes[src] = true
		for _, c := range callees {
			nodes[c] = true
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

	// Real module dependency verification (fail-closed on any error).
	if md := checkModuleDeps(e.root); md != nil {
		res.Findings = append(res.Findings, md.findings...)
		if md.ok {
			if len(md.findings) > 0 {
				res.OK = false
			}
		} else {
			// Could not run the check: never fabricate a PASS.
			res.OK = false
		}
	}
	return res
}

// renderViolation formats a guard violation as a deterministic single line.
func renderViolation(v intel.Violation) string {
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
