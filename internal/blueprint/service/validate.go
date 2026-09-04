// Package service implements the one canonical validation pipeline (spec Rule 1).
//
// Every adapter (CLI, MCP, git hook, watcher, CI) eventually calls
// BlueprintService.Validate. There is no other validation path.
package service

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"time"

	"github.com/JayveerPrajapati/kern/internal/blueprint/audit"
	"github.com/JayveerPrajapati/kern/internal/blueprint/domain"
	"github.com/JayveerPrajapati/kern/internal/blueprint/resilience"
)

// Check is the seam every validator implements. A check examines a ChangeRequest
// and returns a CheckResult. Checks must be deterministic given the same input
// and kern index state (spec Rule 4).
//
// Checks MUST NOT mutate the ChangeRequest or the repository. They MAY invoke
// the kern binary (read-only subcommands) and the filesystem (read-only).
type Check interface {
	// Name is the stable check identifier used in CheckResult.Name and policy
	// routing. Must be unique across all registered checks.
	Name() string

	// Run executes the check. A nil error means the check ran to completion and
	// its CheckResult is authoritative. A non-nil error means the check could
	// not run (tool failure, missing dependency) and produces StatusError.
	Run(ctx context.Context, req domain.ChangeRequest) (domain.CheckResult, error)
}

// optionalChecks is the catalog of opt-in checks a pipeline may legitimately
// omit (e.g. resilience behind --resilience, because fault injection is
// slow). Keyed by the check's stable Name(); the value is the user-facing
// short name recorded in checks_skipped and the terminal summary. P2-2: a
// check that did not run must be explicit, visible audit state — never a
// silent omission.
var optionalChecks = map[string]string{
	resilience.CheckName: "resilience",
}

// BlueprintService is the canonical validation orchestrator (spec Section 8).
// There is exactly one validation pipeline; this is it.
type BlueprintService struct {
	checks      []Check
	policy      PolicyEvaluator
	config      Config
	now         func() time.Time
	corrID      func() string
	metrics     MetricsRecorder
	metricsPath string
	audit       *audit.Writer
}

// PolicyEvaluator maps a CheckResult to a final Status per the loaded policy
// (spec Section 7). The evaluator is what makes a deterministic WARN-rule not
// escalate to BLOCK, and a BLOCK-rule not downgrade to WARN.
type PolicyEvaluator interface {
	// Evaluate takes a raw CheckResult and returns the enforced Status plus the
	// findings with severities adjusted per policy. It MUST be monotonic: if any
	// finding has SeverityBlock, the returned Status MUST be StatusBlock.
	Evaluate(result domain.CheckResult) (domain.Status, []domain.Finding)
}

// Config is the resolved Blueprint configuration (spec Section 7).
type Config struct {
	Mode        string // "enforce" | "warn" | "off"
	Enforcement map[domain.Category]domain.Enforcement
	// SourceRules holds per-source enforcement overrides (spec P0-3), keyed by
	// change source then category. It mirrors policy.Policy.SourceRules for
	// consumers that only receive the service config.
	SourceRules    map[domain.Source]map[domain.Category]domain.Enforcement
	TimeoutSec     int
	MaxOutputBytes int
	// StagedLatencyBudgetMs is the P2-3 performance budget gate: when > 0 and
	// the validation wall time exceeds it, the result gains a WARN-only
	// performance:latency-budget finding (visible everywhere, never blocking).
	// 0 disables the gate.
	StagedLatencyBudgetMs int
	// KernVersion is the kern binary version stamped onto every finding by the
	// service (P2-4, service-owned provenance). It is set via WithKernVersion
	// from a best-effort client.Version() probe by the caller. An empty string
	// skips stamping, so a failed probe never affects validation. Check-owned
	// fields (rule_version, confidence, scope, index_freshness) are never
	// overwritten.
	KernVersion string
}

// Option configures a BlueprintService.
type Option func(*BlueprintService)

// WithPolicy sets the policy evaluator. Required for correct enforcement.
func WithPolicy(p PolicyEvaluator) Option {
	return func(s *BlueprintService) { s.policy = p }
}

// WithConfig sets the resolved configuration.
func WithConfig(c Config) Option {
	return func(s *BlueprintService) { s.config = c }
}

// WithClock injects a clock (for deterministic tests).
func WithClock(fn func() time.Time) Option {
	return func(s *BlueprintService) { s.now = fn }
}

// MetricsRecorder is the local-observability seam. metrics.Metrics satisfies
// it; defining the interface here (instead of importing the metrics package)
// keeps the dependency direction one-way: metrics may import service in its
// tests, service never imports metrics.
type MetricsRecorder interface {
	RecordValidation(status string, duration time.Duration)
	RecordCheckLatency(checkName string, duration time.Duration)
	Save(path string) error
}

// WithMetrics attaches the local metrics recorder (nil-safe: when absent,
// validation runs record nothing). The recorder is saved to path after each
// validation so `blueprint metrics` reflects real runs.
func WithMetrics(m MetricsRecorder, path string) Option {
	return func(s *BlueprintService) { s.metrics = m; s.metricsPath = path }
}

// WithAudit attaches the audit-trail writer (nil-safe: when absent, validation
// runs record nothing). Writes are best-effort: a failed audit write must
// never fail a validation (same philosophy as metrics).
func WithAudit(w *audit.Writer) Option {
	return func(s *BlueprintService) { s.audit = w }
}

// WithKernVersion sets the kern binary version the service stamps onto every
// finding (P2-4, service-owned provenance). Callers obtain it from a
// best-effort client.Version() probe. An empty value skips stamping, so a
// failed probe never affects validation. Check-owned fields (rule_version,
// confidence, scope, index_freshness) are never overwritten.
func WithKernVersion(v string) Option {
	return func(s *BlueprintService) { s.config.KernVersion = v }
}

// New constructs a BlueprintService with the given checks (in execution order)
// and options. Checks may be nil; the service will return StatusSkip for an
// empty change set.
func New(checks []Check, opts ...Option) *BlueprintService {
	s := &BlueprintService{
		checks: checks,
		now:    time.Now,
		corrID: func() string { return fmt.Sprintf("bp-%d", time.Now().UnixNano()) },
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Validate runs the canonical pipeline (spec Section 8) and returns the
// aggregated ValidationResult.
//
// Aggregation is monotonic for blocking findings (spec Section 8 hard requirement):
// if any check produces a BLOCK, the final result is BLOCK regardless of other
// checks' statuses. This invariant is enforced by Aggregate.
func (s *BlueprintService) Validate(ctx context.Context, req domain.ChangeRequest) domain.ValidationResult {
	start := s.now()
	corrID := s.corrID()

	// Empty change => documented NOOP contract (G1: "empty change => PASS/NOOP").
	// The NOOP early return is intentionally audit-free: no-op validations
	// write no audit record (P1-1).
	if len(req.Files) == 0 {
		return domain.ValidationResult{
			Status:        domain.StatusPass,
			ExitCode:      0,
			Checks:        nil,
			DurationMs:    s.sinceMs(start),
			CorrelationID: corrID,
		}
	}

	results := make([]domain.CheckResult, 0, len(s.checks))
	timeout := s.config.timeoutDuration()

	for _, chk := range s.checks {
		cr := s.runCheck(ctx, chk, req, timeout)
		results = append(results, cr)
	}
	// P2-2: record which opt-in checks were NOT exercised in this pipeline
	// (e.g. resilience without --resilience) so the skip is visible audit
	// state, never a silent omission.
	skipped := s.computeSkippedChecks()

	status, exitCode, findings, summary := s.Aggregate(results)
	durationMs := s.sinceMs(start)
	s.recordMetrics(status, results, durationMs)

	// P2-3 latency budget (performance gate): when a budget is configured and
	// the validation wall time exceeds it, append a WARN-only performance
	// finding. It is appended AFTER aggregation and policy evaluation, so it
	// never participates in the monotonic BLOCK aggregation and never passes
	// through the policy engine. It can at most bump a PASS to WARN — BLOCK,
	// ERROR and SKIP statuses (and their exit codes) are untouched, so the
	// latency gate never blocks on its own (WARN stays exit 0).
	if budget := s.config.StagedLatencyBudgetMs; budget > 0 && durationMs > int64(budget) {
		findings = append(findings, domain.Finding{
			RuleID:      "performance:latency-budget",
			Severity:    domain.SeverityWarn,
			Category:    domain.CategoryPerformance,
			Message:     fmt.Sprintf("validation took %dms (budget %dms)", durationMs, budget),
			Explanation: "Staged validation exceeded the configured latency budget; the sec cache and batched git diff should keep typical changes well under the budget. Check per-check durations in the artifact.",
			// P2-4: the latency finding is constructed here (service), so the
			// service owns its rule_version/confidence/scope stamping. Its
			// kern_version is stamped by the service-wide loop below.
			RuleVersion: "1",
			Confidence:  1.0,
			Scope:       "repo",
			Evidence: []domain.Evidence{{
				Kind:        "latency",
				Description: fmt.Sprintf("measured %dms budget %dms", durationMs, budget),
			}},
		})
		summary.Total++
		summary.Warnings++
		if status == domain.StatusPass {
			status = domain.StatusWarn
		}
	}

	// P1.2: validate the retrieval provenance schema version on consumption
	// (mirrors the KernContractVersion pattern in adapters/kern). Provenance
	// is audit metadata, not a safety gate: a version skew emits a WARN
	// finding and never blocks. Like the latency gate, it is appended AFTER
	// aggregation and policy evaluation, so it can at most bump PASS to WARN.
	if p := req.ContextProvenance; p != nil && p.SchemaVersion != domain.ContextProvenanceSchemaVersion {
		findings = append(findings, domain.Finding{
			RuleID:      "provenance:schema-version",
			Severity:    domain.SeverityWarn,
			Category:    domain.CategoryPolicy,
			Message:     fmt.Sprintf("context provenance schema_version %d does not match expected %d", p.SchemaVersion, domain.ContextProvenanceSchemaVersion),
			Explanation: "The retrieval provenance attached to this change came from a kern version speaking a different contract version. Provenance is audit metadata, so the change still validates; the WARN makes the skew visible in the audit trail. Upgrade or pin kern (KERN_BINARY) to the matching version.",
			RuleVersion: "1",
			Confidence:  1.0,
			Scope:       "repo",
		})
		if status == domain.StatusPass {
			status = domain.StatusWarn
		}
	}

	// P2-4: stamp the kern binary version onto every finding (service-owned
	// provenance). This runs after the latency block so the latency finding is
	// stamped too, and before writeAudit so the audit trail carries it.
	// Best-effort: an empty KernVersion is skipped. Check-owned fields
	// (rule_version, confidence, scope, index_freshness) are NOT touched.
	if kv := s.config.KernVersion; kv != "" {
		for i := range findings {
			findings[i].KernVersion = kv
		}
	}
	s.writeAudit(corrID, req, status, exitCode, findings, summary, durationMs, skipped)

	return domain.ValidationResult{
		Status:        status,
		ExitCode:      exitCode,
		Findings:      findings,
		Summary:       summary,
		Checks:        results,
		DurationMs:    durationMs,
		CorrelationID: corrID,
		ChecksSkipped: skipped,
	}
}

// recordMetrics accumulates validation counts and per-check latencies into
// the attached local metrics recorder and persists it. Best-effort: a broken
// metrics file must never fail a validation.
func (s *BlueprintService) recordMetrics(status domain.Status, results []domain.CheckResult, durationMs int64) {
	if s.metrics == nil {
		return
	}
	s.metrics.RecordValidation(string(status), time.Duration(durationMs)*time.Millisecond)
	for _, cr := range results {
		s.metrics.RecordCheckLatency(cr.Name, time.Duration(cr.Duration)*time.Millisecond)
	}
	if s.metricsPath != "" {
		_ = s.metrics.Save(s.metricsPath)
	}
}

// writeAudit appends one self-hashed audit record for a completed validation
// (P1-1). Best-effort: a failed audit write must never fail a validation —
// same philosophy as metrics. Records carry findings META only
// (rule_id/severity/category/file/line); messages, evidence, and snippets are
// never written (redaction invariant).
func (s *BlueprintService) writeAudit(corrID string, req domain.ChangeRequest, status domain.Status, exitCode int, findings []domain.Finding, summary domain.Summary, durationMs int64, skipped []string) {
	if s.audit == nil {
		return
	}
	rec := audit.Record{
		CorrelationID:     corrID,
		Timestamp:         s.now(),
		Source:            req.Source,
		AgentID:           req.AgentID,
		Operation:         req.Operation,
		RepoRoot:          req.RepositoryRoot,
		Status:            status,
		ExitCode:          exitCode,
		ContextProvenance: req.ContextProvenance, // P1.2: link decision to its context authorization
		Summary: audit.SummaryMeta{
			Total:    summary.Total,
			Errors:   summary.Errors,
			Warnings: summary.Warnings,
			Blocks:   summary.Blocks,
			Skipped:  summary.Skipped,
		},
		DurationMs:    durationMs,
		ChecksSkipped: skipped, // P2-2: opt-in checks that did not run stay visible in the audit trail
	}
	for _, f := range findings {
		rec.Findings = append(rec.Findings, audit.FindingMeta{
			RuleID:     f.RuleID,
			Severity:   f.Severity,
			Category:   f.Category,
			File:       f.File,
			Line:       f.Line,
			Suppressed: f.Suppressed,
			Owner:      f.Owner,
		})
	}
	_ = s.audit.Write(rec) // best-effort: never fail validation on audit write failure
}

// computeSkippedChecks returns the user-facing names of opt-in checks that
// were NOT registered in this pipeline (P2-2). Absence of an optional check —
// e.g. resilience behind --resilience — is recorded explicitly so the
// not-run state is visible in the validation result and the audit trail,
// never a silent omission. Returns nil when every optional check ran.
func (s *BlueprintService) computeSkippedChecks() []string {
	registered := make(map[string]bool, len(s.checks))
	for _, chk := range s.checks {
		registered[chk.Name()] = true
	}
	var skipped []string
	for checkName, display := range optionalChecks {
		if !registered[checkName] {
			skipped = append(skipped, display)
		}
	}
	if len(skipped) == 0 {
		return nil
	}
	// Deterministic order so repeated runs marshal identical JSON.
	sort.Strings(skipped)
	return skipped
}

// runCheck executes one check with timeout and error normalization.
func (s *BlueprintService) runCheck(ctx context.Context, chk Check, req domain.ChangeRequest, timeout time.Duration) domain.CheckResult {
	checkStart := s.now()
	cctx := ctx
	if timeout > 0 {
		var cancel context.CancelFunc
		cctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	cr, err := chk.Run(cctx, req)
	cr.Duration = s.sinceMs(checkStart)
	if cr.Name == "" {
		cr.Name = chk.Name()
	}
	// Stamp the change source onto the result so policy evaluation can resolve
	// per-source overrides (spec P0-3).
	cr.Source = req.Source

	if err != nil {
		cr.Status = domain.StatusError
		cr.Error = err.Error()
		// Preserve any findings the check produced before erroring.
		if cr.Findings == nil {
			cr.Findings = []domain.Finding{}
		}
		return cr
	}

	// Apply policy evaluation if configured.
	if s.policy != nil {
		enforcedStatus, enforcedFindings := s.policy.Evaluate(cr)
		cr.Status = enforcedStatus
		cr.Findings = enforcedFindings
	}

	return cr
}

// Aggregate combines per-check results into a final Status, exit code, and
// flattened findings list. This is the monotonic aggregation (spec Section 8).
//
// Precedence: ERROR > BLOCK > WARN > PASS > SKIP.
// A single BLOCK anywhere forces final BLOCK. A single ERROR forces final ERROR
// (tool failure is not a silent pass). WARNs never upgrade to BLOCK.
func (s *BlueprintService) Aggregate(results []domain.CheckResult) (status domain.Status, exitCode int, findings []domain.Finding, summary domain.Summary) {
	hasError := false
	hasBlock := false
	hasWarn := false
	hasPass := false
	skipped := 0

	findings = []domain.Finding{}
	for _, cr := range results {
		// A check is skipped when it says so explicitly (Skipped flag) or
		// when its status IS StatusSkip — both must be counted and must not
		// affect the aggregated status.
		if cr.Skipped || cr.Status == domain.StatusSkip {
			skipped++
			continue
		}
		switch cr.Status {
		case domain.StatusError:
			hasError = true
		case domain.StatusBlock:
			hasBlock = true
		case domain.StatusWarn:
			hasWarn = true
		case domain.StatusPass:
			hasPass = true
		}
		findings = append(findings, cr.Findings...)
	}

	switch {
	case hasError:
		status = domain.StatusError
		exitCode = 2
	case hasBlock:
		status = domain.StatusBlock
		exitCode = 1
	case hasWarn:
		status = domain.StatusWarn
		exitCode = 0 // WARN is not a violation exit
	case hasPass:
		status = domain.StatusPass
		exitCode = 0
	default:
		// All checks skipped.
		status = domain.StatusSkip
		exitCode = 0
	}

	// Truthful counts: Errors counts only SeverityError findings, Blocks
	// counts SeverityBlock findings, Warnings counts SeverityWarn findings.
	// (Previously Errors folded blocks in and Blocks was a monotonic 0/1 flag,
	// which misled: 3 block findings showed as errors=3, blocks=1.)
	summary = domain.Summary{
		Total:   len(findings),
		Skipped: skipped,
	}
	for _, f := range findings {
		switch f.Severity {
		case domain.SeverityError:
			summary.Errors++
		case domain.SeverityWarn:
			summary.Warnings++
		case domain.SeverityBlock:
			summary.Blocks++
		}
	}

	return status, exitCode, findings, summary
}

func (s *BlueprintService) sinceMs(start time.Time) int64 {
	return s.now().Sub(start).Milliseconds()
}

func (c Config) timeoutDuration() time.Duration {
	for _, key := range []string{"BLUEPRINT_TIMEOUT", "BLUEPRINT_CI_TIMEOUT"} {
		if val := os.Getenv(key); val != "" {
			if d, err := time.ParseDuration(val); err == nil && d > 0 {
				return d
			}
			if secs, err := strconv.Atoi(val); err == nil && secs > 0 {
				return time.Duration(secs) * time.Second
			}
		}
	}
	if c.TimeoutSec <= 0 {
		return 300 * time.Second
	}
	return time.Duration(c.TimeoutSec) * time.Second
}
