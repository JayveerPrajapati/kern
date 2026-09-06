// Package verification is the unified Verification Engine. It produces a single
// VerificationResult from build, unit-test, security, architecture and
// dependency sources by wrapping the existing v1 packages (internal/verify,
// internal/sandbox, internal/sec, internal/validate, internal/intel), which it
// imports but never modifies. Output is a typed, deterministic, evidence-backed
// contract for the governance layer.
package verification

import (
	"fmt"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// Verdict is the overall outcome of a verification run.
type Verdict string

const (
	// VerdictPass means every executed verification passed cleanly.
	VerdictPass Verdict = "PASS"
	// VerdictFail means at least one executed verification reported failure.
	VerdictFail Verdict = "FAIL"
	// VerdictWarn means verification passed but surfaced warnings (e.g.
	// security findings).
	VerdictWarn Verdict = "WARN"
	// VerdictPassWithWarning indicates the check passed but with non-blocking warnings.
	VerdictPassWithWarning Verdict = "PASS_WITH_WARNING"
	// VerdictBlocked indicates the check could not run / was gated (e.g. approval, missing prereq).
	VerdictBlocked Verdict = "BLOCKED"
	// VerdictNotRun indicates the check was not executed.
	VerdictNotRun Verdict = "NOT_RUN"
)

// VerificationResult is the unified verification output.
type VerificationResult struct {
	TaskID       string
	Target       string // symbol/file/service being verified
	Build        *BuildResult
	UnitTests    *TestResult
	Integration  *TestResult
	Security     *SecurityResult
	Architecture *ArchitectureResult
	Dependency   *DependencyResult
	// E2ETests is nil when E2E tests were not requested or none were detected,
	// distinguishing "not run" from "ran and passed".
	E2ETests *E2ETestResult
	// StaticAnalysis holds static analysis output (go vet, staticcheck,
	// golangci-lint); nil when not requested.
	StaticAnalysis *StaticAnalysisResult
	// Performance holds optional benchmark results; nil when not requested or
	// none exist.
	Performance *PerformanceResult
	// CI holds the CI/CD pipeline sub-result; skipped (OK=true) when no
	// adapter is configured.
	CI       CIResult
	Evidence []domain.Evidence
	// Claims are the evidence-backed domain.Claims aggregated by the
	// individual verifications (security findings, test/build outcomes).
	Claims      []domain.Claim
	Verdict     Verdict
	Summary     string
	GeneratedAt time.Time
}

// BuildResult captures the outcome of a build verification.
type BuildResult struct {
	OK       bool
	Output   string
	Duration time.Duration
	// Claims are the evidence-backed claims for this build result (e.g. from
	// evidence.FromBuildResult).
	Claims []domain.Claim
}

// TestResult captures the outcome of a unit or integration test verification.
type TestResult struct {
	Package  string
	Passed   int
	Failed   int
	Skipped  int
	Duration time.Duration
	OK       bool
	Output   string
	// Claims are the evidence-backed claims for this test result (e.g. from
	// evidence.FromTestResult).
	Claims []domain.Claim
}

// SecurityResult aggregates security scan findings. The internal/sec scanner
// reports error/warning/info severities, mapped to Critical/High/Low (no
// Medium). A non-zero Critical count fails the check; lower severities are
// non-blocking warnings.
type SecurityResult struct {
	Findings []Finding
	Count    int
	Critical int
	High     int
	Medium   int
	Low      int
	OK       bool
	// Error is non-empty when the scan itself failed (e.g. the root directory
	// could not be read), as opposed to findings being found.
	Error string
	// Claims are the evidence-backed claims emitted for each finding (via
	// evidence.FromSecurityFinding).
	Claims []domain.Claim
}

// Finding is one security issue reported at a concrete file:line. It mirrors
// sec.Finding but is defined here so the verification package owns its result
// contract (no v1 type leaks into the domain-facing API).
type Finding struct {
	File     string
	Line     int
	Rule     string
	Severity string
	Message  string
	Snippet  string
}

// ArchitectureResult aggregates architectural rule violations.
type ArchitectureResult struct {
	Violations []string // rule violations found
	// Warnings surfaces gaps that are not violations but must not be silent
	// either (e.g. no .kern/boundaries.json was found, so the guard was not
	// enforced). A warning does not flip OK to false.
	Warnings []string
	OK       bool
}

// DependencyResult verifies real module dependencies (missing modules, version
// duplication) in addition to summarizing the intelligence graph.
type DependencyResult struct {
	GraphNodes int
	GraphEdges int
	OK         bool
	// Findings lists concrete dependency anomalies (e.g. "missing module
	// example.com/x required by main.go"). Empty = the module's dependencies
	// are consistent.
	Findings []string
}

// E2ETestResult holds end-to-end test results. E2E tests are distinguished
// from unit tests by test tags (e.g. //go:build e2e) or a separate test
// command. Populated only when E2E tests are requested.
type E2ETestResult struct {
	OK       bool
	Passed   int
	Failed   int
	Skipped  int
	Output   string
	Duration time.Duration
}

// StaticAnalysisResult holds static analysis output (go vet, staticcheck,
// golangci-lint). The tool runs whichever linter is available; OK is false
// when any finding is reported.
type StaticAnalysisResult struct {
	OK       bool
	Tool     string   // "go vet", "staticcheck", "golangci-lint"
	Findings []string // finding messages
	Output   string
	Duration time.Duration
}

// PerformanceResult holds benchmark results. Optional — only populated
// when benchmark data is available.
type PerformanceResult struct {
	OK         bool
	Benchmarks []BenchmarkResult
	Output     string
	Duration   time.Duration
}

// BenchmarkResult is a single benchmark measurement.
type BenchmarkResult struct {
	Name        string
	Iterations  int
	NsPerOp     int64
	BytesPerOp  int64
	AllocsPerOp int64
}

// CIResult is the CI/CD pipeline verification sub-result. When a CIAdapter
// is configured, Verify("ci") triggers a pipeline run (or checks the latest
// run) and reports its status. When no adapter is configured, the sub-result
// is skipped (OK=true, skipped note) — CI is optional.
type CIResult struct {
	OK      bool   `json:"ok"`
	JobID   string `json:"job_id,omitempty"`
	Status  string `json:"status,omitempty"` // "success", "failure", "in_progress", "queued", "skipped"
	URL     string `json:"url,omitempty"`
	Summary string `json:"summary,omitempty"`
}

// RenderCompact renders a VerificationResult as a short verdict plus one line
// per executed sub-check. Used where a FAIL verdict must still surface the
// typed verdict and per-check status instead of a bare error (report A11).
func RenderCompact(v VerificationResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "verdict: %s\n", v.Verdict)
	if v.Summary != "" {
		fmt.Fprintf(&b, "summary: %s\n", v.Summary)
	}
	line := func(name, status, detail string) {
		if status == "" {
			status = map[bool]string{true: "OK", false: "FAIL"}[detail == ""]
		}
		if detail != "" {
			b.WriteString(name + ": " + status + " " + detail + "\n")
		} else {
			b.WriteString(name + ": " + status + "\n")
		}
	}
	if v.Build != nil {
		line("build", okStatus(v.Build.OK), fmt.Sprintf("(%s)", v.Build.Duration))
	}
	if v.UnitTests != nil {
		line("tests", okStatus(v.UnitTests.OK), fmt.Sprintf("passed=%d failed=%d skipped=%d (%s)", v.UnitTests.Passed, v.UnitTests.Failed, v.UnitTests.Skipped, v.UnitTests.Duration))
	}
	if v.Integration != nil {
		line("integration", okStatus(v.Integration.OK), fmt.Sprintf("passed=%d failed=%d skipped=%d (%s)", v.Integration.Passed, v.Integration.Failed, v.Integration.Skipped, v.Integration.Duration))
	}
	if v.Security != nil {
		detail := fmt.Sprintf("findings=%d critical=%d high=%d low=%d", v.Security.Count, v.Security.Critical, v.Security.High, v.Security.Low)
		if v.Security.Error != "" {
			detail += " error=" + v.Security.Error
		}
		line("security", okStatus(v.Security.OK), detail)
	}
	if v.Architecture != nil {
		line("architecture", okStatus(v.Architecture.OK), fmt.Sprintf("violations=%d warnings=%d", len(v.Architecture.Violations), len(v.Architecture.Warnings)))
	}
	if v.Dependency != nil {
		line("dependency", okStatus(v.Dependency.OK), fmt.Sprintf("nodes=%d edges=%d findings=%d", v.Dependency.GraphNodes, v.Dependency.GraphEdges, len(v.Dependency.Findings)))
	}
	if v.StaticAnalysis != nil {
		line("static-analysis", okStatus(v.StaticAnalysis.OK), fmt.Sprintf("tool=%s findings=%d", v.StaticAnalysis.Tool, len(v.StaticAnalysis.Findings)))
	}
	if v.E2ETests != nil {
		line("e2e", okStatus(v.E2ETests.OK), fmt.Sprintf("passed=%d failed=%d skipped=%d", v.E2ETests.Passed, v.E2ETests.Failed, v.E2ETests.Skipped))
	}
	return strings.TrimSuffix(b.String(), "\n")
}

func okStatus(ok bool) string {
	return map[bool]string{true: "OK", false: "FAIL"}[ok]
}
