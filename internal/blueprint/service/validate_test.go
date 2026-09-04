package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/blueprint/audit"
	"github.com/JayveerPrajapati/kern/internal/blueprint/domain"
	"github.com/JayveerPrajapati/kern/internal/blueprint/metrics"
)

// fakeCheck is a test double implementing Check with a predetermined result.
type fakeCheck struct {
	name   string
	result domain.CheckResult
	err    error
}

func (f *fakeCheck) Name() string { return f.name }
func (f *fakeCheck) Run(ctx context.Context, req domain.ChangeRequest) (domain.CheckResult, error) {
	return f.result, f.err
}

// req is a minimal valid ChangeRequest with one file.
func req() domain.ChangeRequest {
	return domain.ChangeRequest{
		RepositoryRoot: "/tmp/repo",
		Source:         domain.SourceAgent,
		Operation:      domain.OpCommit,
		Files:          []domain.FileChange{{Path: "main.go", Op: domain.OpEdit}},
	}
}

func blockFinding(file string, line int) domain.Finding {
	return domain.Finding{
		RuleID:   "test:block",
		Severity: domain.SeverityBlock,
		Category: domain.CategoryArchitecture,
		File:     file,
		Line:     line,
		Message:  "block finding",
	}
}

func warnFinding(file string, line int) domain.Finding {
	return domain.Finding{
		RuleID:   "test:warn",
		Severity: domain.SeverityWarn,
		Category: domain.CategoryTests,
		File:     file,
		Line:     line,
		Message:  "warn finding",
	}
}

func noPolicy() PolicyEvaluator { return nil }

// G1 test 1: valid change => PASS
func TestG1_ValidChange_PASS(t *testing.T) {
	checks := []Check{
		&fakeCheck{name: "architecture:guard", result: domain.CheckResult{Name: "architecture:guard", Status: domain.StatusPass, Findings: nil}},
	}
	svc := New(checks, WithClock(func() time.Time { return time.Unix(1000, 0) }), WithPolicy(noPolicy()))
	r := svc.Validate(context.Background(), req())
	if r.Status != domain.StatusPass {
		t.Fatalf("status = %s, want PASS", r.Status)
	}
	if r.ExitCode != 0 {
		t.Fatalf("exitCode = %d, want 0", r.ExitCode)
	}
}

// G1 test 2: architecture violation => BLOCK
func TestG1_ArchitectureViolation_BLOCK(t *testing.T) {
	checks := []Check{
		&fakeCheck{name: "architecture:guard", result: domain.CheckResult{
			Name:     "architecture:guard",
			Status:   domain.StatusBlock,
			Findings: []domain.Finding{blockFinding("web/web.go", 10)},
		}},
	}
	svc := New(checks, WithPolicy(noPolicy()))
	r := svc.Validate(context.Background(), req())
	if r.Status != domain.StatusBlock {
		t.Fatalf("status = %s, want BLOCK", r.Status)
	}
	if r.ExitCode != 1 {
		t.Fatalf("exitCode = %d, want 1", r.ExitCode)
	}
}

// G1 test 3: empty change => PASS/NOOP
func TestG1_EmptyChange_PASS(t *testing.T) {
	checks := []Check{
		&fakeCheck{name: "architecture:guard", result: domain.CheckResult{Name: "architecture:guard", Status: domain.StatusPass}},
	}
	svc := New(checks, WithPolicy(noPolicy()))
	emptyReq := domain.ChangeRequest{RepositoryRoot: "/tmp/repo", Source: domain.SourceHuman, Operation: domain.OpCommit, Files: nil}
	r := svc.Validate(context.Background(), emptyReq)
	if r.Status != domain.StatusPass {
		t.Fatalf("status = %s, want PASS for empty change", r.Status)
	}
	if len(r.Checks) != 0 {
		t.Fatalf("expected 0 checks run for empty change, got %d", len(r.Checks))
	}
}

// G1 test 4: multiple findings => all findings preserved
func TestG1_MultipleFindings_AllPreserved(t *testing.T) {
	findings := []domain.Finding{
		blockFinding("a.go", 1),
		warnFinding("b.go", 2),
		warnFinding("c.go", 3),
	}
	checks := []Check{
		&fakeCheck{name: "architecture:guard", result: domain.CheckResult{Name: "architecture:guard", Status: domain.StatusBlock, Findings: findings[:1]}},
		&fakeCheck{name: "tests:run", result: domain.CheckResult{Name: "tests:run", Status: domain.StatusWarn, Findings: findings[1:]}},
	}
	svc := New(checks, WithPolicy(noPolicy()))
	r := svc.Validate(context.Background(), req())
	if len(r.Findings) != 3 {
		t.Fatalf("expected 3 findings preserved, got %d: %+v", len(r.Findings), r.Findings)
	}
	if r.Summary.Total != 3 {
		t.Fatalf("summary total = %d, want 3", r.Summary.Total)
	}
}

// G1 test 5: one BLOCK + many WARNs => BLOCK (monotonic aggregation)
func TestG1_BlockPlusWarns_BLOCK(t *testing.T) {
	checks := []Check{
		&fakeCheck{name: "tests:run", result: domain.CheckResult{
			Name:     "tests:run",
			Status:   domain.StatusWarn,
			Findings: []domain.Finding{warnFinding("t1.go", 1), warnFinding("t2.go", 2), warnFinding("t3.go", 3)},
		}},
		&fakeCheck{name: "architecture:guard", result: domain.CheckResult{
			Name:     "architecture:guard",
			Status:   domain.StatusBlock,
			Findings: []domain.Finding{blockFinding("web.go", 10)},
		}},
		&fakeCheck{name: "tests:run2", result: domain.CheckResult{
			Name:     "tests:run2",
			Status:   domain.StatusWarn,
			Findings: []domain.Finding{warnFinding("t4.go", 4)},
		}},
	}
	svc := New(checks, WithPolicy(noPolicy()))
	r := svc.Validate(context.Background(), req())
	if r.Status != domain.StatusBlock {
		t.Fatalf("status = %s, want BLOCK (monotonic: one BLOCK must dominate)", r.Status)
	}
	if r.ExitCode != 1 {
		t.Fatalf("exitCode = %d, want 1", r.ExitCode)
	}
	if r.Summary.Blocks != 1 {
		t.Fatalf("summary.Blocks = %d, want 1 (the single block finding, counted)", r.Summary.Blocks)
	}
}

// G1 test 6: check error => ERROR status, exit 2
func TestG1_CheckError_ERROR(t *testing.T) {
	checks := []Check{
		&fakeCheck{name: "architecture:guard", err: errFake("subprocess crashed")},
	}
	svc := New(checks, WithPolicy(noPolicy()))
	r := svc.Validate(context.Background(), req())
	if r.Status != domain.StatusError {
		t.Fatalf("status = %s, want ERROR", r.Status)
	}
	if r.ExitCode != 2 {
		t.Fatalf("exitCode = %d, want 2", r.ExitCode)
	}
	if r.Checks[0].Error == "" {
		t.Fatal("check error message not preserved")
	}
}

// G1 test 7: ERROR dominates BLOCK (precedence: ERROR > BLOCK)
func TestG1_ErrorDominatesBlock(t *testing.T) {
	checks := []Check{
		&fakeCheck{name: "architecture:guard", result: domain.CheckResult{
			Name:     "architecture:guard",
			Status:   domain.StatusBlock,
			Findings: []domain.Finding{blockFinding("a.go", 1)},
		}},
		&fakeCheck{name: "tests:run", err: errFake("tool failed")},
	}
	svc := New(checks, WithPolicy(noPolicy()))
	r := svc.Validate(context.Background(), req())
	if r.Status != domain.StatusError {
		t.Fatalf("status = %s, want ERROR (ERROR must dominate BLOCK)", r.Status)
	}
	if r.ExitCode != 2 {
		t.Fatalf("exitCode = %d, want 2", r.ExitCode)
	}
}

// G1 test 8: JSON output stable and parseable
func TestG1_JSONStableParseable(t *testing.T) {
	checks := []Check{
		&fakeCheck{name: "architecture:guard", result: domain.CheckResult{
			Name:     "architecture:guard",
			Status:   domain.StatusBlock,
			Findings: []domain.Finding{blockFinding("web.go", 10)},
		}},
		&fakeCheck{name: "tests:run", result: domain.CheckResult{
			Name:     "tests:run",
			Status:   domain.StatusWarn,
			Findings: []domain.Finding{warnFinding("t.go", 1)},
		}},
	}
	svc := New(checks, WithPolicy(noPolicy()),
		WithClock(func() time.Time { return time.Unix(5000, 0) }),
	)
	// Inject deterministic correlation ID.
	svc.corrID = func() string { return "test-corr-1" }

	r1 := svc.Validate(context.Background(), req())
	r2 := svc.Validate(context.Background(), req())

	b1, _ := json.Marshal(r1)
	b2, _ := json.Marshal(r2)
	if string(b1) != string(b2) {
		t.Fatalf("JSON not stable: run1 != run2\n%s\n%s", b1, b2)
	}

	// Must be parseable back into a ValidationResult.
	var parsed domain.ValidationResult
	if err := json.Unmarshal(b1, &parsed); err != nil {
		t.Fatalf("JSON not parseable: %v", err)
	}
	if parsed.Status != domain.StatusBlock {
		t.Fatalf("parsed status = %s, want BLOCK", parsed.Status)
	}
	if parsed.CorrelationID != "test-corr-1" {
		t.Fatalf("parsed correlationID = %s, want test-corr-1", parsed.CorrelationID)
	}
}

// G1 test 9: all checks skipped => SKIP status
func TestG1_AllSkipped_SKIP(t *testing.T) {
	checks := []Check{
		&fakeCheck{name: "architecture:guard", result: domain.CheckResult{Name: "architecture:guard", Status: domain.StatusSkip, Skipped: true}},
		&fakeCheck{name: "tests:run", result: domain.CheckResult{Name: "tests:run", Status: domain.StatusSkip, Skipped: true}},
	}
	svc := New(checks, WithPolicy(noPolicy()))
	r := svc.Validate(context.Background(), req())
	if r.Status != domain.StatusSkip {
		t.Fatalf("status = %s, want SKIP", r.Status)
	}
	if r.ExitCode != 0 {
		t.Fatalf("exitCode = %d, want 0", r.ExitCode)
	}
	if r.Summary.Skipped != 2 {
		t.Fatalf("summary.Skipped = %d, want 2", r.Summary.Skipped)
	}
}

// --- Aggregation unit tests (subset of the above, explicit) ---

func TestAggregate_BlockMonotonic(t *testing.T) {
	svc := New(nil, WithPolicy(noPolicy()))
	results := []domain.CheckResult{
		{Name: "a", Status: domain.StatusPass},
		{Name: "b", Status: domain.StatusWarn, Findings: []domain.Finding{warnFinding("x", 1)}},
		{Name: "c", Status: domain.StatusBlock, Findings: []domain.Finding{blockFinding("y", 2)}},
		{Name: "d", Status: domain.StatusPass},
	}
	status, exitCode, findings, summary := svc.Aggregate(results)
	if status != domain.StatusBlock {
		t.Fatalf("status = %s, want BLOCK", status)
	}
	if exitCode != 1 {
		t.Fatalf("exitCode = %d, want 1", exitCode)
	}
	if len(findings) != 2 {
		t.Fatalf("findings = %d, want 2", len(findings))
	}
	if summary.Blocks != 1 {
		t.Fatalf("summary.Blocks = %d, want 1 (the single block finding, counted)", summary.Blocks)
	}
}

func TestAggregate_ErrorDominatesBlock(t *testing.T) {
	svc := New(nil, WithPolicy(noPolicy()))
	results := []domain.CheckResult{
		{Name: "a", Status: domain.StatusBlock, Findings: []domain.Finding{blockFinding("y", 2)}},
		{Name: "b", Status: domain.StatusError, Error: "boom"},
	}
	status, exitCode, _, _ := svc.Aggregate(results)
	if status != domain.StatusError {
		t.Fatalf("status = %s, want ERROR", status)
	}
	if exitCode != 2 {
		t.Fatalf("exitCode = %d, want 2", exitCode)
	}
}

func TestAggregate_WarnDoesNotBlock(t *testing.T) {
	svc := New(nil, WithPolicy(noPolicy()))
	results := []domain.CheckResult{
		{Name: "a", Status: domain.StatusWarn, Findings: []domain.Finding{warnFinding("x", 1)}},
		{Name: "b", Status: domain.StatusPass},
	}
	status, exitCode, _, _ := svc.Aggregate(results)
	if status != domain.StatusWarn {
		t.Fatalf("status = %s, want WARN", status)
	}
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0 (WARN is not a violation exit)", exitCode)
	}
}

// errFake is a simple error type for test doubles.
type errFake string

func (e errFake) Error() string { return string(e) }

// TestValidateRecordsMetrics: WithMetrics wires the local recorder into the
// pipeline — after a real validation, counts and per-check latencies are
// persisted to the metrics file.
func TestValidateRecordsMetrics(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".blueprint", "metrics.json")
	m, err := metrics.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	svc := New([]Check{
		&fakeCheck{name: "a:one", result: domain.CheckResult{Name: "a:one", Status: domain.StatusPass, Duration: 5}},
		&fakeCheck{name: "b:two", result: domain.CheckResult{Name: "b:two", Status: domain.StatusBlock, Duration: 7, Findings: []domain.Finding{{RuleID: "x", Severity: domain.SeverityBlock}}}},
	}, WithMetrics(m, path))
	res := svc.Validate(context.Background(), domain.ChangeRequest{
		RepositoryRoot: dir,
		Files:          []domain.FileChange{{Path: "a.go", Op: domain.OpWrite}},
	})
	if res.Status != domain.StatusBlock {
		t.Fatalf("Status = %q, want BLOCK", res.Status)
	}
	loaded, err := metrics.Load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if loaded.ValidationCount != 1 || loaded.BlockedCount != 1 {
		t.Errorf("counts = %+v, want validation=1 blocked=1", loaded)
	}
	// runCheck overwrites Duration with the real measured latency, so only
	// the presence of a recorded entry per check is asserted.
	for _, name := range []string{"a:one", "b:two"} {
		got := loaded.PerCheckLatencies[name]
		if len(got) != 1 || got[0] < 0 {
			t.Errorf("per-check latency for %s = %v, want 1 non-negative entry", name, got)
		}
	}
}

// TestValidateWithoutMetricsIsNoop: default service (no recorder) must not
// crash and must not write any metrics file.
func TestValidateWithoutMetricsIsNoop(t *testing.T) {
	dir := t.TempDir()
	svc := New([]Check{
		&fakeCheck{name: "a:one", result: domain.CheckResult{Name: "a:one", Status: domain.StatusPass}},
	})
	res := svc.Validate(context.Background(), domain.ChangeRequest{
		RepositoryRoot: dir,
		Files:          []domain.FileChange{{Path: "a.go", Op: domain.OpWrite}},
	})
	if res.Status != domain.StatusPass {
		t.Fatalf("Status = %q, want PASS", res.Status)
	}
	if _, err := os.Stat(filepath.Join(dir, ".blueprint", "metrics.json")); !os.IsNotExist(err) {
		t.Errorf("metrics file should not exist without a recorder, stat err = %v", err)
	}
}

// TestAggregate_BlocksCountedNotFlag: summary.Blocks must COUNT block-severity
// findings (3 block findings => Blocks == 3), and block findings must NOT fold
// into summary.Errors.
func TestAggregate_BlocksCountedNotFlag(t *testing.T) {
	svc := New(nil, WithPolicy(noPolicy()))
	results := []domain.CheckResult{
		{Name: "a", Status: domain.StatusBlock, Findings: []domain.Finding{
			blockFinding("y", 1), blockFinding("y", 2), blockFinding("y", 3),
		}},
	}
	status, _, findings, summary := svc.Aggregate(results)
	if status != domain.StatusBlock {
		t.Fatalf("status = %s, want BLOCK", status)
	}
	if len(findings) != 3 {
		t.Fatalf("findings = %d, want 3", len(findings))
	}
	if summary.Blocks != 3 {
		t.Errorf("summary.Blocks = %d, want 3 (counted, not a 0/1 flag)", summary.Blocks)
	}
	if summary.Errors != 0 {
		t.Errorf("summary.Errors = %d, want 0 (block findings must not fold into errors)", summary.Errors)
	}
	if summary.Total != 3 {
		t.Errorf("summary.Total = %d, want 3", summary.Total)
	}
}

// TestAggregate_ErrorsCountOnlySeverityError: summary.Errors counts ONLY
// SeverityError findings; block findings are counted under Blocks.
func TestAggregate_ErrorsCountOnlySeverityError(t *testing.T) {
	svc := New(nil, WithPolicy(noPolicy()))
	results := []domain.CheckResult{
		{Name: "a", Status: domain.StatusBlock, Findings: []domain.Finding{
			blockFinding("y", 1),
			{RuleID: "test:error", Severity: domain.SeverityError, Category: domain.CategoryTests, File: "z", Line: 2, Message: "tool error"},
		}},
	}
	_, _, _, summary := svc.Aggregate(results)
	if summary.Errors != 1 {
		t.Errorf("summary.Errors = %d, want 1 (only the SeverityError finding)", summary.Errors)
	}
	if summary.Blocks != 1 {
		t.Errorf("summary.Blocks = %d, want 1", summary.Blocks)
	}
}

// TestAggregate_StatusSkipWithoutFlagCounted: a check whose status IS
// StatusSkip but which does not set the Skipped flag must still be counted in
// summary.Skipped and must not affect the aggregated status.
func TestAggregate_StatusSkipWithoutFlagCounted(t *testing.T) {
	svc := New(nil, WithPolicy(noPolicy()))
	results := []domain.CheckResult{
		{Name: "a", Status: domain.StatusSkip}, // no Skipped flag set
		{Name: "b", Status: domain.StatusPass},
	}
	status, exitCode, findings, summary := svc.Aggregate(results)
	if status != domain.StatusPass {
		t.Fatalf("status = %s, want PASS (skip must not affect status)", status)
	}
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0", exitCode)
	}
	if len(findings) != 0 {
		t.Fatalf("findings = %d, want 0", len(findings))
	}
	if summary.Skipped != 1 {
		t.Errorf("summary.Skipped = %d, want 1 (StatusSkip without flag is still skipped)", summary.Skipped)
	}
	if summary.Total != 0 {
		t.Errorf("summary.Total = %d, want 0", summary.Total)
	}
}

// recordingEvaluator is a PolicyEvaluator test double that records the
// CheckResult it receives, so tests can assert what the service passed to
// policy evaluation.
type recordingEvaluator struct {
	got      domain.CheckResult
	status   domain.Status
	findings []domain.Finding
}

func (r *recordingEvaluator) Evaluate(result domain.CheckResult) (domain.Status, []domain.Finding) {
	r.got = result
	return r.status, r.findings
}

// TestG16_ServiceSetsCheckResultSource: runCheck must stamp req.Source onto
// the CheckResult before policy evaluation so per-source overrides (spec P0-3)
// can be resolved by the evaluator.
func TestG16_ServiceSetsCheckResultSource(t *testing.T) {
	rec := &recordingEvaluator{status: domain.StatusPass}
	checks := []Check{
		&fakeCheck{name: "architecture:guard", result: domain.CheckResult{
			Name:     "architecture:guard",
			Status:   domain.StatusWarn,
			Findings: []domain.Finding{warnFinding("a.go", 1)},
		}},
	}
	svc := New(checks, WithClock(func() time.Time { return time.Unix(1000, 0) }), WithPolicy(rec))

	r := req()
	r.Source = domain.SourceDepBot
	result := svc.Validate(context.Background(), r)

	if len(result.Checks) != 1 {
		t.Fatalf("result.Checks = %d, want 1", len(result.Checks))
	}
	if rec.got.Source != domain.SourceDepBot {
		t.Errorf("Evaluate received CheckResult.Source = %q, want %q", rec.got.Source, domain.SourceDepBot)
	}
	if result.Checks[0].Source != domain.SourceDepBot {
		t.Errorf("aggregated CheckResult.Source = %q, want %q", result.Checks[0].Source, domain.SourceDepBot)
	}
}

// --- P1-1 audit trail (gate G19) ---

// readAuditLines reads the audit JSONL file into one string per line.
func readAuditLines(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read audit %s: %v", path, err)
	}
	var lines []string
	for _, l := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		if l != "" {
			lines = append(lines, l)
		}
	}
	return lines
}

// TestG19_ValidationWritesAudit: a real validation with WithAudit writes
// exactly one JSONL record carrying status/exit-code/source/correlation-id and
// findings META only (rule/severity/file) — never message/evidence/snippet.
func TestG19_ValidationWritesAudit(t *testing.T) {
	dir := t.TempDir()
	auditPath := filepath.Join(dir, ".blueprint", "audit", "audit.jsonl")
	aw := audit.NewWriter(auditPath)

	checks := []Check{
		&fakeCheck{name: "architecture:guard", result: domain.CheckResult{
			Name:     "architecture:guard",
			Status:   domain.StatusBlock,
			Findings: []domain.Finding{blockFinding("web/web.go", 10)},
		}},
		&fakeCheck{name: "tests:run", result: domain.CheckResult{Name: "tests:run", Status: domain.StatusPass}},
	}
	svc := New(checks, WithPolicy(noPolicy()), WithAudit(aw))
	svc.corrID = func() string { return "g19-corr-1" }

	r := svc.Validate(context.Background(), req())
	if r.Status != domain.StatusBlock {
		t.Fatalf("status = %s, want BLOCK", r.Status)
	}

	lines := readAuditLines(t, auditPath)
	if len(lines) != 1 {
		t.Fatalf("audit has %d lines, want exactly 1", len(lines))
	}

	// The record must be parseable and match the validation result.
	var rec audit.Record
	if err := json.Unmarshal([]byte(lines[0]), &rec); err != nil {
		t.Fatalf("audit line is not valid JSON: %v", err)
	}
	if rec.Status != r.Status {
		t.Errorf("audit status = %s, want %s", rec.Status, r.Status)
	}
	if rec.ExitCode != r.ExitCode {
		t.Errorf("audit exit_code = %d, want %d", rec.ExitCode, r.ExitCode)
	}
	if rec.Source != req().Source {
		t.Errorf("audit source = %s, want %s", rec.Source, req().Source)
	}
	if rec.CorrelationID != r.CorrelationID {
		t.Errorf("audit correlation_id = %q, want %q", rec.CorrelationID, r.CorrelationID)
	}
	if rec.RepoRoot != req().RepositoryRoot {
		t.Errorf("audit repo_root = %q, want %q", rec.RepoRoot, req().RepositoryRoot)
	}
	if rec.Operation != req().Operation {
		t.Errorf("audit operation = %s, want %s", rec.Operation, req().Operation)
	}
	if rec.Summary.Total != 1 || rec.Summary.Blocks != 1 {
		t.Errorf("audit summary = %+v, want Total=1 Blocks=1", rec.Summary)
	}
	if len(rec.Findings) != 1 {
		t.Fatalf("audit findings = %d, want 1", len(rec.Findings))
	}
	fm := rec.Findings[0]
	if fm.RuleID != "test:block" || fm.Severity != domain.SeverityBlock || fm.File != "web/web.go" || fm.Line != 10 {
		t.Errorf("audit finding meta = %+v, want test:block/block/web/web.go:10", fm)
	}

	// Redaction invariant: the raw JSONL must not leak message/evidence/snippet
	// keys nor the finding's message text.
	raw := lines[0]
	for _, banned := range []string{`"message"`, `"evidence"`, `"snippet"`, `"explanation"`, `"suggested_fix"`} {
		if strings.Contains(raw, banned) {
			t.Errorf("audit record leaks key %s — redaction invariant violated: %s", banned, raw)
		}
	}
	if strings.Contains(raw, "block finding") {
		t.Errorf("audit record leaks finding message text: %s", raw)
	}
}

// TestP22_ResilienceNotRunIsExplicit: a validation WITHOUT the resilience
// check records it explicitly as skipped — in the result JSON (checks_skipped)
// and in the audit record — instead of silently omitting it (P2-2). When the
// resilience check IS registered, checks_skipped is empty/omitted from both.
func TestP22_ResilienceNotRunIsExplicit(t *testing.T) {
	dir := t.TempDir()
	auditPath := filepath.Join(dir, ".blueprint", "audit", "audit.jsonl")
	aw := audit.NewWriter(auditPath)

	svc := New([]Check{
		&fakeCheck{name: "architecture:guard", result: domain.CheckResult{Name: "architecture:guard", Status: domain.StatusPass}},
	}, WithPolicy(noPolicy()), WithAudit(aw))
	svc.corrID = func() string { return "p22-corr-1" }

	r := svc.Validate(context.Background(), req())

	// Validation result carries the explicit not-run state.
	if len(r.ChecksSkipped) != 1 || r.ChecksSkipped[0] != "resilience" {
		t.Fatalf("ChecksSkipped = %v, want [resilience]", r.ChecksSkipped)
	}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	if !strings.Contains(string(b), `"checks_skipped":["resilience"]`) {
		t.Errorf("result JSON missing checks_skipped: %s", b)
	}

	// Audit record carries the same explicit not-run state.
	lines := readAuditLines(t, auditPath)
	if len(lines) != 1 {
		t.Fatalf("audit has %d lines, want exactly 1", len(lines))
	}
	var rec audit.Record
	if err := json.Unmarshal([]byte(lines[0]), &rec); err != nil {
		t.Fatalf("audit line is not valid JSON: %v", err)
	}
	if len(rec.ChecksSkipped) != 1 || rec.ChecksSkipped[0] != "resilience" {
		t.Errorf("audit ChecksSkipped = %v, want [resilience]", rec.ChecksSkipped)
	}
	if !strings.Contains(lines[0], `"checks_skipped":["resilience"]`) {
		t.Errorf("audit JSON lacks explicit not-run state: %s", lines[0])
	}

	// When resilience IS registered, checks_skipped is omitted from both.
	dir2 := t.TempDir()
	auditPath2 := filepath.Join(dir2, ".blueprint", "audit", "audit.jsonl")
	aw2 := audit.NewWriter(auditPath2)
	svc2 := New([]Check{
		&fakeCheck{name: "architecture:guard", result: domain.CheckResult{Name: "architecture:guard", Status: domain.StatusPass}},
		&fakeCheck{name: "resilience:scenarios", result: domain.CheckResult{Name: "resilience:scenarios", Status: domain.StatusPass}},
	}, WithPolicy(noPolicy()), WithAudit(aw2))
	r2 := svc2.Validate(context.Background(), req())
	if len(r2.ChecksSkipped) != 0 {
		t.Errorf("ChecksSkipped with resilience registered = %v, want empty", r2.ChecksSkipped)
	}
	b2, _ := json.Marshal(r2)
	if strings.Contains(string(b2), "checks_skipped") {
		t.Errorf("result JSON should omit checks_skipped when resilience ran: %s", b2)
	}
	lines2 := readAuditLines(t, auditPath2)
	if strings.Contains(lines2[0], "checks_skipped") {
		t.Errorf("audit JSON should omit checks_skipped when resilience ran: %s", lines2[0])
	}
}

// TestG19_AuditFailureDoesNotFailValidation: when the audit file cannot be
// written, Validate still returns its normal status and exit code with no
// error (best-effort contract, same philosophy as metrics).
func TestG19_AuditFailureDoesNotFailValidation(t *testing.T) {
	dir := t.TempDir()
	// Make the parent of the audit path an existing FILE: MkdirAll fails, so
	// every Write fails — but validation must still succeed. Robust on macOS.
	blocker := filepath.Join(dir, "blocked")
	if err := os.WriteFile(blocker, []byte("not a dir"), 0o644); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	auditPath := filepath.Join(blocker, "audit.jsonl")

	checks := []Check{
		&fakeCheck{name: "architecture:guard", result: domain.CheckResult{
			Name:     "architecture:guard",
			Status:   domain.StatusBlock,
			Findings: []domain.Finding{blockFinding("a.go", 1)},
		}},
	}
	svc := New(checks, WithPolicy(noPolicy()), WithAudit(audit.NewWriter(auditPath)))

	r := svc.Validate(context.Background(), req())
	if r.Status != domain.StatusBlock {
		t.Fatalf("status = %s, want BLOCK despite audit failure", r.Status)
	}
	if r.ExitCode != 1 {
		t.Fatalf("exit_code = %d, want 1 despite audit failure", r.ExitCode)
	}
}

// TestG19_EmptyChangeNoAudit: an empty change is a documented NOOP and must
// write NO audit record.
func TestG19_EmptyChangeNoAudit(t *testing.T) {
	dir := t.TempDir()
	auditPath := filepath.Join(dir, "audit.jsonl")
	svc := New(nil, WithAudit(audit.NewWriter(auditPath)))

	emptyReq := domain.ChangeRequest{RepositoryRoot: "/tmp/repo", Source: domain.SourceHuman, Operation: domain.OpCommit, Files: nil}
	r := svc.Validate(context.Background(), emptyReq)
	if r.Status != domain.StatusPass {
		t.Fatalf("status = %s, want PASS for empty change", r.Status)
	}
	if _, err := os.Stat(auditPath); !os.IsNotExist(err) {
		t.Fatalf("audit file exists for a no-op validation (stat err = %v) — no record expected", err)
	}
}

// --- P2-3 (G24): latency budget gate ---

// newAdvancingClock builds a clock whose each successive call is one step
// later than the previous, so a validation's measured wall time grows
// deterministically across calls.
func newAdvancingClock(step time.Duration) func() time.Time {
	calls := 0
	return func() time.Time {
		calls++
		return time.Unix(1000, int64(calls)*step.Nanoseconds())
	}
}

func TestG24_LatencyBudgetExceededAddsWarnFinding(t *testing.T) {
	clock := newAdvancingClock(time.Millisecond)
	checks := []Check{
		&fakeCheck{name: "architecture:guard", result: domain.CheckResult{Name: "architecture:guard", Status: domain.StatusPass}},
	}
	svc := New(checks,
		WithConfig(Config{StagedLatencyBudgetMs: 1}),
		WithClock(clock),
	)
	r := svc.Validate(context.Background(), req())
	if r.Status != domain.StatusWarn {
		t.Fatalf("status = %s, want WARN (bumped from PASS)", r.Status)
	}
	if r.ExitCode != 0 {
		t.Fatalf("exitCode = %d, want 0 (WARN never blocks)", r.ExitCode)
	}
	var found *domain.Finding
	for i := range r.Findings {
		if r.Findings[i].RuleID == "performance:latency-budget" {
			found = &r.Findings[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("no performance:latency-budget finding in %+v", r.Findings)
	}
	if found.Severity != domain.SeverityWarn || found.Category != domain.CategoryPerformance {
		t.Errorf("finding = %+v, want WARN severity + performance category", found)
	}
	if r.Summary.Warnings != 1 || r.Summary.Total != 1 {
		t.Errorf("summary = %+v, want warnings=1 total=1", r.Summary)
	}
}

func TestG24_LatencyBudgetDisabledNoFinding(t *testing.T) {
	clock := newAdvancingClock(time.Millisecond)
	checks := []Check{
		&fakeCheck{name: "architecture:guard", result: domain.CheckResult{Name: "architecture:guard", Status: domain.StatusPass}},
	}
	// StagedLatencyBudgetMs zero (default) => gate disabled.
	svc := New(checks, WithConfig(Config{}), WithClock(clock))
	r := svc.Validate(context.Background(), req())
	if r.Status != domain.StatusPass {
		t.Fatalf("status = %s, want PASS (budget disabled)", r.Status)
	}
	for _, f := range r.Findings {
		if f.RuleID == "performance:latency-budget" {
			t.Fatalf("unexpected latency finding with budget disabled: %+v", f)
		}
	}
}

func TestG24_LatencyBudgetDoesNotDowngradeBlock(t *testing.T) {
	clock := newAdvancingClock(time.Millisecond)
	checks := []Check{
		&fakeCheck{name: "architecture:guard", result: domain.CheckResult{
			Name:     "architecture:guard",
			Status:   domain.StatusBlock,
			Findings: []domain.Finding{blockFinding("web/web.go", 10)},
		}},
	}
	svc := New(checks,
		WithConfig(Config{StagedLatencyBudgetMs: 1}),
		WithClock(clock),
	)
	r := svc.Validate(context.Background(), req())
	if r.Status != domain.StatusBlock {
		t.Fatalf("status = %s, want BLOCK (latency bump must never downgrade BLOCK)", r.Status)
	}
	if r.ExitCode != 1 {
		t.Fatalf("exitCode = %d, want 1", r.ExitCode)
	}
	hasLatency := false
	for _, f := range r.Findings {
		if f.RuleID == "performance:latency-budget" {
			hasLatency = true
		}
	}
	if !hasLatency {
		t.Fatalf("missing latency finding alongside BLOCK findings: %+v", r.Findings)
	}
}

// --- P2-4 (G25): kern version provenance stamping ---

// TestG25_WithKernVersionStampsAllFindings verifies WithKernVersion stamps
// every finding — including the latency-budget finding appended after
// aggregation — with kern_version, and that the latency finding carries its
// check-owned rule_version/confidence/scope.
func TestG25_WithKernVersionStampsAllFindings(t *testing.T) {
	clock := newAdvancingClock(time.Millisecond)
	checks := []Check{
		&fakeCheck{name: "architecture:guard", result: domain.CheckResult{
			Name:     "architecture:guard",
			Status:   domain.StatusBlock,
			Findings: []domain.Finding{blockFinding("web/web.go", 10)},
		}},
	}
	svc := New(checks,
		WithConfig(Config{StagedLatencyBudgetMs: 1}),
		WithKernVersion("v0.9.0"),
		WithClock(clock),
	)
	r := svc.Validate(context.Background(), req())
	if len(r.Findings) < 2 {
		t.Fatalf("Findings = %d, want >= 2 (block finding + latency finding)", len(r.Findings))
	}
	hasLatency := false
	for i := range r.Findings {
		if r.Findings[i].RuleID == "performance:latency-budget" {
			hasLatency = true
			// The latency finding's check-owned fields are stamped at
			// construction; KernVersion comes from the service-wide loop.
			if r.Findings[i].RuleVersion != "1" || r.Findings[i].Confidence != 1.0 || r.Findings[i].Scope != "repo" {
				t.Errorf("latency finding check-owned fields wrong: %+v", r.Findings[i])
			}
		}
		if r.Findings[i].KernVersion != "v0.9.0" {
			t.Errorf("finding %q KernVersion = %q, want %q", r.Findings[i].RuleID, r.Findings[i].KernVersion, "v0.9.0")
		}
	}
	if !hasLatency {
		t.Fatalf("missing latency finding: %+v", r.Findings)
	}
}

// TestG25_EmptyKernVersionSkipsStamping verifies an empty KernVersion leaves
// findings unstamped: a failed version probe must never affect validation.
func TestG25_EmptyKernVersionSkipsStamping(t *testing.T) {
	checks := []Check{
		&fakeCheck{name: "architecture:guard", result: domain.CheckResult{
			Name:     "architecture:guard",
			Status:   domain.StatusBlock,
			Findings: []domain.Finding{blockFinding("web/web.go", 10)},
		}},
	}
	svc := New(checks, WithKernVersion(""))
	r := svc.Validate(context.Background(), req())
	if len(r.Findings) != 1 {
		t.Fatalf("Findings = %d, want 1", len(r.Findings))
	}
	if r.Findings[0].KernVersion != "" {
		t.Errorf("KernVersion = %q, want empty (stamping skipped)", r.Findings[0].KernVersion)
	}
}

// TestG25_StampingLeavesCheckOwnedFieldsUntouched verifies the service-level
// kern version stamping never overwrites check-owned provenance fields
// (rule_version, confidence, scope, index_freshness).
func TestG25_StampingLeavesCheckOwnedFieldsUntouched(t *testing.T) {
	f := blockFinding("web/web.go", 10)
	f.RuleVersion = "1"
	f.Confidence = 0.5
	f.Scope = "file"
	f.IndexFreshness = "fresh"
	checks := []Check{
		&fakeCheck{name: "architecture:guard", result: domain.CheckResult{
			Name:     "architecture:guard",
			Status:   domain.StatusBlock,
			Findings: []domain.Finding{f},
		}},
	}
	svc := New(checks, WithKernVersion("v0.9.0"))
	r := svc.Validate(context.Background(), req())
	if len(r.Findings) != 1 {
		t.Fatalf("Findings = %d, want 1", len(r.Findings))
	}
	got := r.Findings[0]
	if got.RuleVersion != "1" || got.Confidence != 0.5 || got.Scope != "file" || got.IndexFreshness != "fresh" {
		t.Errorf("check-owned fields overwritten: %+v", got)
	}
	if got.KernVersion != "v0.9.0" {
		t.Errorf("KernVersion = %q, want v0.9.0", got.KernVersion)
	}
}
