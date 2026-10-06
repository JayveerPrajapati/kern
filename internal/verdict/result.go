// Package verdict owns the unified verification result contract: the typed
// VerificationResult and its sub-results, the Verdict math, and the
// evidence/claim adapters consumed by the governance layer and by
// internal/verification (which produces the results).
package verdict

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
	// VerdictSkipped means at least one executed check was explicitly skipped
	// (e.g. tests could not run because network isolation is unavailable). A
	// skipped check counts as NEITHER passing nor failing in the verdict math,
	// so the run is not a pass: an operator cannot claim full verification when
	// a check did not run.
	VerdictSkipped Verdict = "SKIPPED"
)

// StatusSkipped is the explicit per-check status stamped on a sub-verification
// result when the check was NOT executed (e.g. a test set skipped because
// network isolation is unavailable on this platform). It is distinct from OK
// and FAIL: a skipped check is excluded from the verdict math and must never
// render as a pass.
const StatusSkipped = "SKIPPED"

// StatusWarn marks a test phase that exited non-zero with ZERO failed
// tests — a diagnostic (a go vet / policy finding or compile error
// surfaced by go test before any test executed). Such a run is a WARNING,
// never a test failure: the tests phase may only FAIL when failed>0 (F3).
const StatusWarn = "WARN"

// StatusNoRunner marks a test phase that could not run because no test
// runner was detected. It renders as SKIPPED in the phase lines but folds
// to a WARN verdict — a vacuous PASS over an unmeasured suite is
// dishonest (F3).
const StatusNoRunner = "NO-RUNNER"

// SkipPrefix marks a sub-result Output whose check was skipped rather than
// failed. The prefix is the skip marker; the text that follows it is the
// reason. Shared across engine.go (setter) and the renderers (summarizeChecks,
// cmd_review.go) so the marker cannot drift between producer and consumers.
const SkipPrefix = "skipped: "

// VerificationResult is the unified verification output.
type VerificationResult struct {
	Version      string `json:"version,omitempty"`
	TaskID       string
	Target       string // symbol/file/service being verified
	Build        *BuildResult
	UnitTests    *TestResult
	Integration  *TestResult
	Security     *SecurityResult
	Architecture *ArchitectureResult
	Dependency   *DependencyResult
	// Reuse holds reuse (duplication) advisories for newly added functions
	// that structurally mirror existing ones; nil when not requested. See
	// ReuseResult: advisory only, never fails, Skipped is informational.
	Reuse *ReuseResult
	// E2ETests is nil when E2E tests were not requested or none were detected,
	// distinguishing "not run" from "ran and passed".
	E2ETests *E2ETestResult
	// StaticAnalysis holds static analysis output (go vet, staticcheck,
	// golangci-lint); nil when not requested.
	StaticAnalysis *StaticAnalysisResult
	// Performance holds optional benchmark results; nil when not requested or
	// none exist.
	Performance *PerformanceResult
	// CVE holds the govulncheck vulnerability check; nil when not requested.
	CVE *CVEResult
	// License holds the deterministic license classifier output; nil when not
	// requested.
	License *LicenseResult
	// Secrets holds the committed-secret history scan; nil when not requested.
	Secrets *SecretsResult
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
	// LogPath is the root-relative path of the FULL build output log under
	// .kern/audit/<run-id>/ ("" when nothing was captured). Output above is
	// the clipped tail; this points at the complete log (F4).
	LogPath string
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
	// LogPath is the root-relative path of the FULL test output log under
	// .kern/audit/<run-id>/ ("" when nothing was captured). Output above is
	// the clipped tail; this points at the complete log (F4).
	LogPath string
	// Claims are the evidence-backed claims for this test result (e.g. from
	// evidence.FromTestResult).
	Claims []domain.Claim
	// Status is an explicit per-check status label. "" means the OK bool
	// decides (OK/FAIL). StatusSkipped means the test set was NOT executed
	// (e.g. network isolation unavailable) — it is neither a pass nor a fail
	// and must not be counted as either in the verdict math.
	Status string
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
	// Suppressed counts findings covered by a triage suppression (built-in
	// defaults or .kern/verify-suppressions.json). Suppressed findings are
	// excluded from the Critical/High/Low ladder above — those counts
	// reflect only live problems — but stay listed in Findings with their
	// reason, and Count remains the raw total including suppressed, so the
	// report shows the full picture.
	Suppressed int
	OK         bool
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
	// Suppressed marks a finding covered by a triage suppression (built-in
	// defaults or .kern/verify-suppressions.json). Suppressed findings stay
	// listed here — rendered with SuppressionReason so the triage is
	// auditable — but they never block the security check and are excluded
	// from the Critical/High/Low risk ladder.
	Suppressed        bool
	SuppressionReason string
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

// DependencyResult verifies real dependency manifests (missing modules,
// unpinned or duplicate dependencies) across supported ecosystems, in addition
// to summarizing the intelligence graph.
type DependencyResult struct {
	GraphNodes int
	GraphEdges int
	OK         bool
	// Findings lists concrete dependency anomalies (e.g. "missing module
	// example.com/x required by main.go", "unpinned dependency foo"). Empty =
	// the project's declared dependencies are consistent.
	Findings []string
	// Warnings surfaces advisory findings that are not anomalies (e.g. a
	// dependency newly added in the working tree vs HEAD). A warning does not
	// flip OK to false.
	Warnings []string
	// Skipped is non-empty when the project has no supported dependency
	// manifest at all (nothing to verify) — an honest skip, distinct from a
	// PASS or a FAIL.
	Skipped string
}

// ReuseResult aggregates reuse advisories for newly added Go functions that
// are structurally near-identical to an existing function (the "reuse"
// verification). Advisory by design (spec line 1084 of the duplication
// scanner: it never blocks): findings never flip the overall verdict to FAIL.
// Skipped is INFORMATIONAL ONLY — a clean tree is the normal state and must
// NOT downgrade a PASS to VerdictSkipped (deliberately unlike the compliance
// checks, where a skipped check counts as neither passing nor failing).
type ReuseResult struct {
	Findings []string
	Skipped  string
	OK       bool
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
	// LogPath is the root-relative path of the FULL e2e output log under
	// .kern/audit/<run-id>/ ("" when nothing was captured). Output above is
	// the clipped tail; this points at the complete log (F4).
	LogPath string
	// Status is an explicit per-check status label; StatusSkipped means the
	// E2E set was NOT executed (e.g. network isolation unavailable) and must
	// not count as passing or failing in the verdict math.
	Status string
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
	// LogPath is the root-relative path of the FULL static-analysis output
	// log under .kern/audit/<run-id>/ ("" when nothing was captured). Output
	// above is the clipped tail; this points at the complete log (F4).
	LogPath string
	// Status is an explicit per-check status label. "" means the OK bool
	// decides (OK/FAIL). StatusSkipped means the tool could NOT be executed
	// (e.g. a sandbox execution failure or a missing binary) — the phase is
	// neither a pass nor a fail and is excluded from the verdict math.
	Status string
}

// PerformanceResult holds benchmark results. Optional — only populated
// when benchmark data is available.
type PerformanceResult struct {
	OK         bool
	Benchmarks []BenchmarkResult
	Output     string
	Duration   time.Duration
	// LogPath is the root-relative path of the FULL benchmark output log
	// under .kern/audit/<run-id>/ ("" when nothing was captured). Output
	// above is the clipped tail; this points at the complete log (F4).
	LogPath string
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

// CVEResult holds the govulncheck vulnerability scan. Vulnerabilities are
// advisory: OK stays true when findings exist (they never fail the verdict)
// and the check is SKIPPED when the binary is absent or could not run.
type CVEResult struct {
	OK       bool
	Status   string // StatusSkipped when govulncheck could not run
	Findings []CVEFinding
	Count    int
	// Detail is the SKIPPED reason (install hint, stderr tail) or empty.
	Detail string
}

// CVEFinding is one OSV-style vulnerability reported by govulncheck.
type CVEFinding struct {
	ID         string // e.g. GO-2023-1234 / CVE-2023-1234
	Module     string // affected Go module path
	Summary    string // description, truncated to ~200 chars
	Introduced string // first affected version ("" = earliest)
	Fixed      string // first fixed version ("" = none known)
}

// LicenseResult holds the deterministic license classification of the
// project's modules (go.mod + vendor/modules.txt). Unknown and copyleft
// licenses are WARN-level findings; the check itself never fails. Skipped is
// non-empty when the project has neither go.mod nor vendor/.
type LicenseResult struct {
	OK       bool
	Skipped  string // "no vendor/ or go.mod found" when nothing to scan
	Modules  []LicenseEntry
	Findings []string // "unknown license: <mod>" / "copyleft: <mod> (<lic>)"
}

// LicenseEntry maps one module to its classified license ("unknown" when no
// local LICENSE text matched a high-precision signature).
type LicenseEntry struct {
	Module  string
	License string
}

// SecretsResult holds the committed-secret history scan. Findings are
// advisory (WARN); the check itself never fails. Status is StatusSkipped when
// the root is not a git repository.
type SecretsResult struct {
	OK       bool
	Status   string // StatusSkipped when git log could not run
	Findings []SecretFinding
	Count    int
	// Detail reports the scan cap ("scan capped at 200000 lines") or a clean
	// confirmation.
	Detail string
}

// SecretFinding is one committed secret: commit hash (short), file path,
// line, pattern kind, and a MASKED snippet (first 4 chars + "…" + last 4) —
// the raw secret is never exposed.
type SecretFinding struct {
	Commit  string
	File    string
	Line    int
	Kind    string // aws-access-key, github-pat, slack-token, private-key, ...
	Snippet string
}

// RenderCompact renders a VerificationResult as a short verdict plus one line
// per executed sub-check. Used where a FAIL verdict must still surface the
// typed verdict and per-check status instead of a bare error.
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
		if v.UnitTests.Status == StatusSkipped || v.UnitTests.Status == StatusNoRunner {
			b.WriteString("tests: SKIPPED " + FirstLine(v.UnitTests.Output) + "\n")
		} else if v.UnitTests.Status == StatusWarn {
			// F3: a diagnostic-only run (non-zero exit, zero failed tests)
			// is a warning, never a test failure. The status line carries
			// the diagnostic so the reason is not lost.
			detail := fmt.Sprintf("passed=%d failed=%d skipped=%d (%s)", v.UnitTests.Passed, v.UnitTests.Failed, v.UnitTests.Skipped, v.UnitTests.Duration)
			reason := testFailureReason(v.UnitTests.Output)
			if reason == "" {
				reason = FirstLine(v.UnitTests.Output)
			}
			line("tests", "WARN (diagnostic: "+reason+")", detail)
		} else {
			detail := fmt.Sprintf("passed=%d failed=%d skipped=%d (%s)", v.UnitTests.Passed, v.UnitTests.Failed, v.UnitTests.Skipped, v.UnitTests.Duration)
			// D1: a FAIL verdict with zero failed tests is contradictory — the
			// suite could not build (go test surfaces vet/compile errors before
			// any test executes, so the PASS/FAIL counters stay 0/0). Fold the
			// actual reason into the status line instead of a bare
			// "tests: FAIL passed=0 failed=0".
			if !v.UnitTests.OK && v.UnitTests.Failed == 0 {
				if reason := testFailureReason(v.UnitTests.Output); reason != "" {
					line("tests", "FAILED (diagnostic: "+reason+")", detail)
				} else {
					line("tests", okStatus(v.UnitTests.OK), detail)
				}
			} else {
				line("tests", okStatus(v.UnitTests.OK), detail)
			}
			if !v.UnitTests.OK && strings.TrimSpace(v.UnitTests.Output) != "" {
				b.WriteString(boundedFailureOutput(v.UnitTests.Output) + "\n")
			}
			// F13: the full output lives in the audit log — point at it
			// instead of dumping it.
			if !v.UnitTests.OK && v.UnitTests.LogPath != "" {
				b.WriteString("full log: " + v.UnitTests.LogPath + "\n")
			}
		}
	}
	if v.Integration != nil {
		if v.Integration.Status == StatusSkipped {
			b.WriteString("integration: SKIPPED " + FirstLine(v.Integration.Output) + "\n")
		} else {
			detail := fmt.Sprintf("passed=%d failed=%d skipped=%d (%s)", v.Integration.Passed, v.Integration.Failed, v.Integration.Skipped, v.Integration.Duration)
			// D1 (mirrors the UnitTests branch above): zero failed tests with a
			// FAIL verdict means the suite could not build — surface the reason.
			if !v.Integration.OK && v.Integration.Failed == 0 {
				if reason := testFailureReason(v.Integration.Output); reason != "" {
					line("integration", "FAILED (diagnostic: "+reason+")", detail)
				} else {
					line("integration", okStatus(v.Integration.OK), detail)
				}
			} else {
				line("integration", okStatus(v.Integration.OK), detail)
			}
			if !v.Integration.OK && strings.TrimSpace(v.Integration.Output) != "" {
				b.WriteString(boundedFailureOutput(v.Integration.Output) + "\n")
			}
			// F13: same full-log pointer as the unit-test branch.
			if !v.Integration.OK && v.Integration.LogPath != "" {
				b.WriteString("full log: " + v.Integration.LogPath + "\n")
			}
		}
	}
	if v.Security != nil {
		detail := fmt.Sprintf("findings=%d critical=%d high=%d low=%d", v.Security.Count, v.Security.Critical, v.Security.High, v.Security.Low)
		if v.Security.Suppressed > 0 {
			detail += fmt.Sprintf(" (%d suppressed)", v.Security.Suppressed)
		}
		if v.Security.Error != "" {
			detail += " error=" + v.Security.Error
		}
		status := okStatus(v.Security.OK)
		if v.Security.OK && v.Security.Critical+v.Security.High+v.Security.Medium+v.Security.Low > 0 {
			// Live findings are warnings, never a clean OK (F7).
			status = "WARN"
		}
		line("security", status, detail)
		for i, fd := range v.Security.Findings {
			if i >= 10 {
				b.WriteString(fmt.Sprintf("  ... and %d more findings\n", len(v.Security.Findings)-10))
				break
			}
			if fd.Suppressed {
				b.WriteString(fmt.Sprintf("  - [suppressed] %s:%d [%s] %s: %s — %s\n", fd.File, fd.Line, fd.Severity, fd.Rule, fd.Message, fd.SuppressionReason))
			} else {
				b.WriteString(fmt.Sprintf("  - %s:%d [%s] %s: %s\n", fd.File, fd.Line, fd.Severity, fd.Rule, fd.Message))
			}
		}
	}
	if v.Architecture != nil {
		line("architecture", okStatus(v.Architecture.OK), fmt.Sprintf("violations=%d warnings=%d", len(v.Architecture.Violations), len(v.Architecture.Warnings)))
	}
	if v.Dependency != nil {
		if v.Dependency.Skipped != "" {
			b.WriteString("dependency: SKIPPED " + v.Dependency.Skipped + "\n")
		} else {
			line("dependency", okStatus(v.Dependency.OK), fmt.Sprintf("nodes=%d edges=%d findings=%d", v.Dependency.GraphNodes, v.Dependency.GraphEdges, len(v.Dependency.Findings)))
			if !v.Dependency.OK {
				for i, fd := range v.Dependency.Findings {
					if i >= 10 {
						b.WriteString(fmt.Sprintf("  ... and %d more findings\n", len(v.Dependency.Findings)-10))
						break
					}
					b.WriteString(fmt.Sprintf("  - %s\n", fd))
				}
			}
		}
	}
	if v.Reuse != nil {
		if v.Reuse.Skipped != "" {
			b.WriteString("reuse: SKIPPED " + v.Reuse.Skipped + "\n")
		} else if len(v.Reuse.Findings) > 0 {
			// Reuse findings are warnings, never a clean OK (F7-style): the
			// check ran and flagged structurally duplicated new functions.
			line("reuse", "WARN", fmt.Sprintf("findings=%d", len(v.Reuse.Findings)))
		} else {
			line("reuse", okStatus(v.Reuse.OK), fmt.Sprintf("findings=%d", len(v.Reuse.Findings)))
		}
		for i, fd := range v.Reuse.Findings {
			if i >= 10 {
				b.WriteString(fmt.Sprintf("  ... and %d more reuse findings\n", len(v.Reuse.Findings)-10))
				break
			}
			b.WriteString(fmt.Sprintf("  - %s\n", fd))
		}
	}
	if v.StaticAnalysis != nil {
		if v.StaticAnalysis.Status == StatusSkipped {
			// Did-not-run (e.g. the sandbox could not execute the tool): a
			// skip, never a clean OK nor a false FAIL — the first output
			// line carries the explicit reason (mirrors the tests SKIPPED
			// render above).
			b.WriteString("static-analysis: SKIPPED " + FirstLine(v.StaticAnalysis.Output) + "\n")
		} else {
			line("static-analysis", okStatus(v.StaticAnalysis.OK), fmt.Sprintf("tool=%s findings=%d", v.StaticAnalysis.Tool, len(v.StaticAnalysis.Findings)))
		}
	}
	if v.CVE != nil {
		if v.CVE.Status == StatusSkipped {
			b.WriteString("cve: SKIPPED " + FirstLine(v.CVE.Detail) + "\n")
		} else if v.CVE.Count > 0 {
			// Vulnerabilities are findings, never a clean OK (F7).
			line("cve", "WARN", fmt.Sprintf("vulnerabilities=%d", v.CVE.Count))
		} else {
			line("cve", okStatus(v.CVE.OK), fmt.Sprintf("vulnerabilities=%d", v.CVE.Count))
		}
		for i, fd := range v.CVE.Findings {
			if i >= 10 {
				b.WriteString(fmt.Sprintf("  ... and %d more vulnerabilities\n", len(v.CVE.Findings)-10))
				break
			}
			b.WriteString(fmt.Sprintf("  - %s %s: %s\n", fd.ID, fd.Module, fd.Summary))
		}
	}
	if v.License != nil {
		if v.License.Skipped != "" {
			b.WriteString("license: SKIPPED " + v.License.Skipped + "\n")
		} else if len(v.License.Findings) > 0 {
			// Unknown/copyleft findings are warnings, never a clean OK (F7).
			line("license", "WARN", fmt.Sprintf("modules=%d findings=%d", len(v.License.Modules), len(v.License.Findings)))
		} else {
			line("license", okStatus(v.License.OK), fmt.Sprintf("modules=%d", len(v.License.Modules)))
		}
		for _, m := range v.License.Modules {
			b.WriteString(fmt.Sprintf("  - %s: %s\n", m.Module, m.License))
		}
		for i, fd := range v.License.Findings {
			if i >= 10 {
				b.WriteString(fmt.Sprintf("  ... and %d more license findings\n", len(v.License.Findings)-10))
				break
			}
			b.WriteString(fmt.Sprintf("  ! %s\n", fd))
		}
	}
	if v.Secrets != nil {
		if v.Secrets.Status == StatusSkipped {
			b.WriteString("secrets: SKIPPED " + FirstLine(v.Secrets.Detail) + "\n")
		} else if v.Secrets.Count > 0 {
			// Findings are findings, never a clean OK (F7): a scan that
			// found 63 secrets must read "WARN", not "OK findings=63".
			line("secrets", "WARN", fmt.Sprintf("findings=%d", v.Secrets.Count))
		} else {
			line("secrets", okStatus(v.Secrets.OK), fmt.Sprintf("findings=%d", v.Secrets.Count))
		}
		if v.Secrets.Detail != "" {
			b.WriteString("  " + v.Secrets.Detail + "\n")
		}
		for i, fd := range v.Secrets.Findings {
			if i >= 10 {
				b.WriteString(fmt.Sprintf("  ... and %d more findings\n", len(v.Secrets.Findings)-10))
				break
			}
			b.WriteString(fmt.Sprintf("  - %s %s:%d [%s] %s\n", fd.Commit, fd.File, fd.Line, fd.Kind, fd.Snippet))
		}
	}
	if v.E2ETests != nil {
		if v.E2ETests.Status == StatusSkipped {
			b.WriteString("e2e: SKIPPED " + FirstLine(v.E2ETests.Output) + "\n")
		} else {
			line("e2e", okStatus(v.E2ETests.OK), fmt.Sprintf("passed=%d failed=%d skipped=%d", v.E2ETests.Passed, v.E2ETests.Failed, v.E2ETests.Skipped))
			if !v.E2ETests.OK && strings.TrimSpace(v.E2ETests.Output) != "" {
				b.WriteString(strings.TrimSpace(v.E2ETests.Output) + "\n")
			}
		}
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// FirstLine returns the first non-empty, trimmed line of s — the concise
// reason carried by a SKIPPED check's output.
func FirstLine(s string) string {
	for _, ln := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(ln); t != "" {
			return t
		}
	}
	return strings.TrimSpace(s)
}

// testFailureReason extracts the first meaningful diagnostic line from a
// failed test run's output. `go test` surfaces compile/vet errors as bare
// diagnostics before any test executes, so the PASS/FAIL counters stay 0/0 —
// the actual reason lives in the output, not the counters (D1). Noise lines
// (package headers, run summaries, PASS/FAIL markers) are skipped.
func testFailureReason(output string) string {
	for _, ln := range strings.Split(output, "\n") {
		t := strings.TrimSpace(ln)
		if t == "" ||
			strings.HasPrefix(t, "#") || // package header (e.g. "# module/path")
			strings.HasPrefix(t, "FAIL") ||
			strings.HasPrefix(t, "ok ") ||
			strings.HasPrefix(t, "--- ") ||
			strings.HasPrefix(t, "=== ") ||
			strings.HasPrefix(t, "PASS") ||
			!isDiagnosticLine(t) {
			continue
		}
		return t
	}
	return ""
}

func okStatus(ok bool) string {
	return map[bool]string{true: "OK", false: "FAIL"}[ok]
}
