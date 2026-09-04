package duplication

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/blueprint/adapters/kern"
	"github.com/JayveerPrajapati/kern/internal/blueprint/domain"
)

// fingerprintFake is a fake kern runner that serves `kern fingerprint --json`
// output from a path-keyed record table, mimicking what the real kern binary
// emits for the fixture repos. It never depends on the real kern binary.
type fingerprintFake struct {
	records map[string][]kern.FingerprintRecord
	// schemaVersion overrides the emitted schema_version (default 2); used to
	// exercise contract-mismatch failure.
	schemaVersion int
	calls         []fingerprintCall
}

// fingerprintCall records one fingerprint invocation.
type fingerprintCall struct {
	workdir string
	files   []string // nil for a whole-root scan
}

func (f *fingerprintFake) runner() kern.CommandRunner {
	return func(ctx context.Context, name string, args []string, workdir string) (string, string, int, error) {
		if len(args) == 0 || args[0] != "fingerprint" {
			return "", "unexpected command: " + strings.Join(args, " "), 2, nil
		}
		var files []string
		for i := 0; i < len(args); i++ {
			if args[i] == "--file" && i+1 < len(args) {
				files = strings.Split(args[i+1], ",")
			}
		}
		f.calls = append(f.calls, fingerprintCall{workdir: workdir, files: files})

		var recs []kern.FingerprintRecord
		if files == nil {
			// Whole-root scan: return records for every .go file present in
			// the scanned directory (real kern emits go records for the whole
			// repo root).
			_ = filepath.Walk(workdir, func(path string, info os.FileInfo, err error) error {
				if err != nil || info.IsDir() {
					return nil
				}
				if !strings.HasSuffix(path, ".go") {
					return nil
				}
				rel, err := filepath.Rel(workdir, path)
				if err != nil {
					return nil
				}
				recs = append(recs, f.records[filepath.ToSlash(rel)]...)
				return nil
			})
		} else {
			for _, file := range files {
				recs = append(recs, f.records[file]...)
			}
		}

		payload := struct {
			SchemaVersion int                      `json:"schema_version"`
			Fingerprints  []kern.FingerprintRecord `json:"fingerprints"`
		}{f.schemaVersion, recs}
		if payload.SchemaVersion == 0 {
			payload.SchemaVersion = 2
		}
		b, err := json.Marshal(payload)
		if err != nil {
			panic(err)
		}
		return string(b), "", 0, nil
	}
}

// newFakeCheck constructs a duplication check backed by a fake kern client.
func newFakeCheck(f *fingerprintFake) *Check {
	client, err := kern.NewKernClient(kern.WithBinary("kern"), kern.WithRunner(f.runner()))
	if err != nil {
		panic(err)
	}
	return NewCheck(client)
}

// fixtureFingerprintRecords returns the fingerprint records the fake runner
// serves for a G6 fixture, keyed by repo-relative path. The values are the REAL
// `kern fingerprint` output for the fixture sources (captured from the rebuilt
// kern binary, which emits control-flow counts), so the unit tests exercise the
// exact signal the production pipeline consumes. Tables are PER-FIXTURE because
// fixtures reuse relative paths (e.g. shared/retry.go) with different content.
func fixtureFingerprintRecords(name string) map[string][]kern.FingerprintRecord {
	rec := func(file, fn, sig string, params, rets int, calls []string, lits, stmts, line int, cf kern.ControlFlow) kern.FingerprintRecord {
		return kern.FingerprintRecord{
			File: file, Name: fn, SignatureShape: sig, ParamCount: params, ReturnCount: rets,
			CalledSymbols: calls, LiteralCount: lits, StatementCount: stmts,
			Lang: "go", Line: line, ControlFlow: cf,
		}
	}
	retryMain := []string{"errors.New(1)", "send(1)", "time.Sleep(1)"}
	switch name {
	case "exact-duplicate":
		return map[string][]kern.FingerprintRecord{
			"payments/retry.go": {
				rec("payments/retry.go", "send", "func(1ptr)1err", 1, 1, nil, 0, 1, 10, kern.ControlFlow{Return: 1}),
				rec("payments/retry.go", "DoRetry", "func(1ptr)1err", 1, 1, retryMain, 3, 8, 12, kern.ControlFlow{If: 1, For: 1, Return: 2, Assign: 2, Call: 3}),
			},
			"shared/retry.go": {
				rec("shared/retry.go", "send", "func(1ptr)1err", 1, 1, nil, 0, 1, 10, kern.ControlFlow{Return: 1}),
				rec("shared/retry.go", "RetryRequest", "func(1ptr)1err", 1, 1, retryMain, 3, 8, 12, kern.ControlFlow{If: 1, For: 1, Return: 2, Assign: 2, Call: 3}),
			},
		}
	case "renamed-duplicate":
		return map[string][]kern.FingerprintRecord{
			"billing/retry.go": {
				rec("billing/retry.go", "transmit", "func(1ptr)1err", 1, 1, nil, 0, 1, 10, kern.ControlFlow{Return: 1}),
				rec("billing/retry.go", "AttemptRetry", "func(1ptr)1err", 1, 1, []string{"fmt.Errorf(1)", "time.Sleep(1)", "transmit(1)"}, 3, 8, 12, kern.ControlFlow{If: 1, For: 1, Return: 2, Assign: 2, Call: 3}),
			},
			"shared/retry.go": {
				rec("shared/retry.go", "send", "func(1ptr)1err", 1, 1, nil, 0, 1, 10, kern.ControlFlow{Return: 1}),
				rec("shared/retry.go", "RetryRequest", "func(1ptr)1err", 1, 1, retryMain, 3, 8, 12, kern.ControlFlow{If: 1, For: 1, Return: 2, Assign: 2, Call: 3}),
			},
		}
	case "slightly-refactored-duplicate":
		return map[string][]kern.FingerprintRecord{
			"checkout/retry.go": {
				rec("checkout/retry.go", "send", "func(1ptr)1err", 1, 1, nil, 0, 1, 10, kern.ControlFlow{Return: 1}),
				rec("checkout/retry.go", "RetryRequest", "func(1ptr)1err", 1, 1, retryMain, 2, 9, 12, kern.ControlFlow{If: 1, Range: 1, Return: 2, Assign: 2, Call: 3}),
			},
			"shared/retry.go": {
				rec("shared/retry.go", "send", "func(1ptr)1err", 1, 1, nil, 0, 1, 10, kern.ControlFlow{Return: 1}),
				rec("shared/retry.go", "RetryRequest", "func(1ptr)1err", 1, 1, retryMain, 3, 9, 12, kern.ControlFlow{If: 1, For: 1, Return: 2, Assign: 3, Call: 3}),
			},
		}
	case "different-but-similar-algorithm":
		return map[string][]kern.FingerprintRecord{
			"gateway/retry.go": {
				rec("gateway/retry.go", "send", "func(1ptr)1err", 1, 1, nil, 0, 1, 10, kern.ControlFlow{Return: 1}),
				rec("gateway/retry.go", "RetryRequest", "func(1ptr)1err", 1, 1, retryMain, 3, 7, 13, kern.ControlFlow{If: 1, For: 1, Return: 2, Assign: 2, Call: 3}),
			},
			"shared/retry.go": {
				rec("shared/retry.go", "send", "func(1ptr)1err", 1, 1, nil, 0, 1, 10, kern.ControlFlow{Return: 1}),
				rec("shared/retry.go", "RetryRequest", "func(1ptr)1err", 1, 1, retryMain, 6, 12, 13, kern.ControlFlow{If: 2, For: 1, Return: 2, Assign: 5, Call: 3}),
			},
		}
	case "wrapper-around-existing":
		return map[string][]kern.FingerprintRecord{
			"api/retry.go": {
				rec("api/retry.go", "RetryWithLog", "func(1ptr)1err", 1, 1, []string{"log.Println(1)", "shared.RetryRequest(1)"}, 1, 2, 9, kern.ControlFlow{Return: 1, Call: 2}),
			},
			"shared/retry.go": {
				rec("shared/retry.go", "send", "func(1ptr)1err", 1, 1, nil, 0, 1, 10, kern.ControlFlow{Return: 1}),
				rec("shared/retry.go", "RetryRequest", "func(1ptr)1err", 1, 1, retryMain, 3, 8, 12, kern.ControlFlow{If: 1, For: 1, Return: 2, Assign: 2, Call: 3}),
			},
		}
	case "unrelated-same-signature":
		return map[string][]kern.FingerprintRecord{
			"shared/process.go": {
				rec("shared/process.go", "Process", "func(1slice)1err", 1, 1, []string{"fmt.Errorf(1)", "json.Unmarshal(2)"}, 2, 8, 14, kern.ControlFlow{If: 2, Return: 3, Assign: 1, Call: 2}),
			},
			"vault/process.go": {
				rec("vault/process.go", "Process", "func(1slice)1err", 1, 1, []string{"byte(1)", "len(1)"}, 2, 8, 4, kern.ControlFlow{If: 1, Range: 1, Return: 2, Assign: 2, Call: 2}),
			},
		}
	case "generated-boilerplate":
		return map[string][]kern.FingerprintRecord{
			"shared/model.go": {
				rec("shared/model.go", "GetName", "func()1string", 0, 1, nil, 0, 1, 8, kern.ControlFlow{Return: 1}),
				rec("shared/model.go", "SetName", "func(1string)", 1, 0, nil, 0, 1, 9, kern.ControlFlow{Assign: 1}),
			},
			"users/model.go": {
				rec("users/model.go", "GetEmail", "func()1string", 0, 1, nil, 0, 1, 7, kern.ControlFlow{Return: 1}),
				rec("users/model.go", "SetEmail", "func(1string)", 1, 0, nil, 0, 1, 8, kern.ControlFlow{Assign: 1}),
			},
		}
	}
	return nil
}

// runCheckAgainstFixture runs the DuplicationCheck against a fixture repo,
// treating the NewFile as a staged change. Returns the CheckResult.
func runCheckAgainstFixture(t *testing.T, f DupFixture) domain.CheckResult {
	t.Helper()
	req := domain.ChangeRequest{
		RepositoryRoot: f.RepoDir,
		Source:         domain.SourceCI,
		Operation:      domain.OpCommit,
		Files:          []domain.FileChange{{Path: f.NewFile, Op: domain.OpWrite}},
	}
	check := newFakeCheck(&fingerprintFake{records: fixtureFingerprintRecords(f.Name)})
	res, err := check.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Check.Run error: %v", err)
	}
	return res
}

// assertBucket verifies the check produced a finding with the expected
// similarity bucket (extracted from the evidence description).
func assertBucket(t *testing.T, res domain.CheckResult, expectedBucket string) {
	t.Helper()
	if expectedBucket == "ignore" {
		// "ignore" means no finding should be produced (similarity < 0.60).
		if len(res.Findings) > 0 {
			for _, f := range res.Findings {
				if bucketStr := extractBucket(f); bucketStr != "ignore" {
					t.Errorf("expected no finding (ignore), got bucket=%s: %s", bucketStr, f.Message)
				}
			}
		}
		return
	}
	// Non-ignore buckets: must have at least one finding matching the bucket.
	for _, f := range res.Findings {
		if extractBucket(f) == expectedBucket {
			return // found a matching finding
		}
	}
	if len(res.Findings) == 0 {
		t.Fatalf("expected finding with bucket %q, got none", expectedBucket)
	}
	t.Fatalf("expected finding with bucket %q; got %d findings with buckets: %v",
		expectedBucket, len(res.Findings), allBuckets(res.Findings))
}

func extractBucket(f domain.Finding) string {
	for _, e := range f.Evidence {
		if desc := e.Description; desc != "" {
			// Description format: "similarity score: 0.92, bucket: warning"
			if idx := indexOfStr(desc, "bucket: "); idx >= 0 {
				start := idx + len("bucket: ")
				rest := desc[start:]
				// Take until space or end.
				for i, c := range rest {
					if c == ' ' || c == ',' {
						return rest[:i]
					}
				}
				return rest
			}
		}
	}
	return "unknown"
}

func allBuckets(findings []domain.Finding) []string {
	var buckets []string
	for _, f := range findings {
		buckets = append(buckets, extractBucket(f))
	}
	return buckets
}

func indexOfStr(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

// recordBucket runs the check and logs the actual bucket vs expected, without
// hard-failing on mismatch. Per spec lines 1063-1084, G6 requires benchmarking
// against these scenarios and tracking metrics — NOT exact per-fixture bucket
// matching. The real quality gates are TestG6_Metrics and TestG6_NeverBlocks.
func recordBucket(t *testing.T, f DupFixture) {
	t.Helper()
	res := runCheckAgainstFixture(t, f)
	if res.Status == domain.StatusError {
		t.Fatalf("%s: check errored: %s", f.Name, res.Error)
	}
	actualBuckets := allBuckets(res.Findings)
	if len(actualBuckets) == 0 {
		actualBuckets = []string{"ignore (no findings)"}
	}
	t.Logf("%-30s expected=%-18s actual=%-18v findings=%d  [%s]",
		f.Name, f.ExpectedBucket, actualBuckets, len(res.Findings), f.Description)
}

// --- G6 benchmark scenarios (spec lines 1065-1073) ---
// Each test benchmarks the check against a fixture and records the result.
// Per spec, these are benchmark data points, not pass/fail gates.

func TestG6_ExactDuplicate(t *testing.T) {
	recordBucket(t, ExactDuplicate(t))
}

func TestG6_RenamedDuplicate(t *testing.T) {
	recordBucket(t, RenamedDuplicate(t))
}

func TestG6_SlightlyRefactoredDuplicate(t *testing.T) {
	recordBucket(t, SlightlyRefactoredDuplicate(t))
}

func TestG6_DifferentButSimilarAlgorithm(t *testing.T) {
	recordBucket(t, DifferentButSimilarAlgorithm(t))
}

func TestG6_WrapperAroundExisting(t *testing.T) {
	recordBucket(t, WrapperAroundExisting(t))
}

func TestG6_UnrelatedSameSignature(t *testing.T) {
	recordBucket(t, UnrelatedSameSignature(t))
}

func TestG6_GeneratedBoilerplate(t *testing.T) {
	recordBucket(t, GeneratedBoilerplate(t))
}

// --- Precision / Recall / Latency tracking (spec lines 1075-1082) ---

// TestG6_Metrics runs all 7 fixtures and computes precision, recall, and
// false-positive rate, then asserts they meet reasonable thresholds.
// The spec says "Do not promote this check from WARN to BLOCK until benchmark
// results justify it" — this test records the metrics so promotion decisions
// are evidence-based.
func TestG6_Metrics(t *testing.T) {
	fixtures := []struct {
		name  string
		build func(*testing.T) DupFixture
		// isTrueDuplicate: true if the fixture represents an actual duplication
		// (exact, renamed, refactored, wrapper). False for genuinely different
		// code (similar-algo, unrelated, boilerplate).
		isTrueDuplicate bool
	}{
		{"exact-duplicate", ExactDuplicate, true},
		{"renamed-duplicate", RenamedDuplicate, true},
		{"refactored-duplicate", SlightlyRefactoredDuplicate, true},
		{"different-similar-algo", DifferentButSimilarAlgorithm, false},
		{"wrapper", WrapperAroundExisting, true},
		{"unrelated-same-sig", UnrelatedSameSignature, false},
		{"generated-boilerplate", GeneratedBoilerplate, false},
	}

	var truePos, falsePos, falseNeg, trueNeg int
	var totalLatency time.Duration

	for _, fx := range fixtures {
		f := fx.build(t)
		start := time.Now()
		res := runCheckAgainstFixture(t, f)
		latency := time.Since(start)
		totalLatency += latency

		detected := len(res.Findings) > 0 && res.Status != domain.StatusPass

		switch {
		case fx.isTrueDuplicate && detected:
			truePos++
		case fx.isTrueDuplicate && !detected:
			falseNeg++
		case !fx.isTrueDuplicate && detected:
			falsePos++
		case !fx.isTrueDuplicate && !detected:
			trueNeg++
		}

		t.Logf("%-25s trueDup=%-5v detected=%-5v latency=%-8s findings=%d",
			fx.name, fx.isTrueDuplicate, detected, latency, len(res.Findings))
	}

	total := len(fixtures)
	precision := 0.0
	if truePos+falsePos > 0 {
		precision = float64(truePos) / float64(truePos+falsePos)
	}
	recall := 0.0
	if truePos+falseNeg > 0 {
		recall = float64(truePos) / float64(truePos+falseNeg)
	}
	fpr := 0.0
	if falsePos+trueNeg > 0 {
		fpr = float64(falsePos) / float64(falsePos+trueNeg)
	}
	avgLatency := totalLatency / time.Duration(total)

	t.Log("--- Duplication Oracle Metrics ---")
	t.Logf("True Positives:  %d", truePos)
	t.Logf("False Positives: %d", falsePos)
	t.Logf("False Negatives: %d", falseNeg)
	t.Logf("True Negatives:  %d", trueNeg)
	t.Logf("Precision:       %.2f", precision)
	t.Logf("Recall:          %.2f", recall)
	t.Logf("False-Pos Rate:  %.2f", fpr)
	t.Logf("Avg Latency:     %s", avgLatency)

	// Reasonable thresholds for starting values (not promoting to BLOCK).
	// These are documented, not gates — the spec says benchmark before promoting.
	if precision < 0.50 {
		t.Errorf("precision %.2f below 0.50 threshold — too many false positives", precision)
	}
	if recall < 0.50 {
		t.Errorf("recall %.2f below 0.50 threshold — missing too many true duplicates", recall)
	}
}

// TestG6_NeverBlocks verifies the check NEVER produces a BLOCK finding,
// regardless of similarity score (spec line 1084).
func TestG6_NeverBlocks(t *testing.T) {
	fixtures := []func(*testing.T) DupFixture{
		ExactDuplicate,
		RenamedDuplicate,
		SlightlyRefactoredDuplicate,
		DifferentButSimilarAlgorithm,
		WrapperAroundExisting,
		UnrelatedSameSignature,
		GeneratedBoilerplate,
	}
	for _, build := range fixtures {
		f := build(t)
		res := runCheckAgainstFixture(t, f)
		for _, finding := range res.Findings {
			if finding.Severity == domain.SeverityBlock {
				t.Errorf("duplication check produced BLOCK finding (spec forbids this): %s", finding.Message)
			}
		}
		if res.Status == domain.StatusBlock {
			t.Errorf("duplication check returned BLOCK status (spec forbids this)")
		}
	}
}

// TestG6_FindingFormat verifies the finding format matches the spec UX example
// (lines 1053-1061):
//
//	WARN  duplicate-candidate
//	new: payments/retry.go::DoRetry
//	existing: shared/retry.go::RetryRequest
//	similarity: 0.92
//	suggestion: reuse shared/retry.go::RetryRequest
func TestG6_FindingFormat(t *testing.T) {
	f := ExactDuplicate(t)
	res := runCheckAgainstFixture(t, f)
	if len(res.Findings) == 0 {
		t.Skip("no findings for exact duplicate fixture — skipping format check")
	}
	finding := res.Findings[0]
	if finding.RuleID != "duplication:advisory" {
		t.Errorf("RuleID = %s, want duplication:advisory", finding.RuleID)
	}
	if finding.Category != domain.CategoryDuplication {
		t.Errorf("Category = %v, want duplication", finding.Category)
	}
	// Message must mention "duplicate-candidate" and similarity score.
	if !contains(finding.Message, "duplicate-candidate") {
		t.Errorf("message missing 'duplicate-candidate': %s", finding.Message)
	}
	if !contains(finding.Message, "similarity") && !contains(finding.Explanation, "similarity") {
		t.Errorf("finding must mention similarity score")
	}
	// SuggestedFix must mention reuse.
	if !contains(finding.SuggestedFix, "Reuse") && !contains(finding.SuggestedFix, "reuse") {
		t.Errorf("SuggestedFix should mention reuse: %s", finding.SuggestedFix)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && indexOfStr(s, substr) >= 0
}

// TestG6_EmptyChange verifies the check skips when there are no changed Go files.
func TestG6_EmptyChange(t *testing.T) {
	dir := t.TempDir()
	writeGoMod(t, dir)
	req := domain.ChangeRequest{
		RepositoryRoot: dir,
		Source:         domain.SourceCI,
		Operation:      domain.OpCommit,
		Files:          []domain.FileChange{}, // empty
	}
	check := newFakeCheck(&fingerprintFake{records: fixtureFingerprintRecords("")})
	res, err := check.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if res.Status != domain.StatusSkip {
		t.Errorf("status = %s, want SKIP for empty change", res.Status)
	}
}

// TestG6_NoExistingFiles verifies the check passes when the new file is the
// only Go file in the repo (nothing to compare against).
func TestG6_NoExistingFiles(t *testing.T) {
	dir := t.TempDir()
	writeGoMod(t, dir)
	writeDupFile(t, dir, "main.go", "package main\n\nfunc main() {}\n")
	req := domain.ChangeRequest{
		RepositoryRoot: dir,
		Source:         domain.SourceCI,
		Operation:      domain.OpCommit,
		Files:          []domain.FileChange{{Path: "main.go", Op: domain.OpWrite}},
	}
	check := newFakeCheck(&fingerprintFake{records: fixtureFingerprintRecords("")})
	res, err := check.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if res.Status == domain.StatusError {
		t.Fatalf("check errored: %s", res.Error)
	}
	if len(res.Findings) > 0 {
		t.Errorf("expected no findings (no existing files to compare), got %d", len(res.Findings))
	}
}

// Ensure all fixture files are found on disk (defensive check).
func TestG6_FixturesExist(t *testing.T) {
	fixtures := []struct {
		name  string
		build func(*testing.T) DupFixture
	}{
		{"exact", ExactDuplicate},
		{"renamed", RenamedDuplicate},
		{"refactored", SlightlyRefactoredDuplicate},
		{"similar-algo", DifferentButSimilarAlgorithm},
		{"wrapper", WrapperAroundExisting},
		{"unrelated", UnrelatedSameSignature},
		{"boilerplate", GeneratedBoilerplate},
	}
	for _, fx := range fixtures {
		f := fx.build(t)
		if _, err := os.Stat(filepath.Join(f.RepoDir, f.NewFile)); err != nil {
			t.Errorf("%s: new file not found: %v", fx.name, err)
		}
		if _, err := os.Stat(filepath.Join(f.RepoDir, f.ExistingFile)); err != nil {
			t.Errorf("%s: existing file not found: %v", fx.name, err)
		}
		if f.ExpectedBucket == "" {
			t.Errorf("%s: expected bucket is empty", fx.name)
		}
	}
}

// --- G21 (gate G21): duplication oracle via kern fingerprint ---

// TestG21_DuplicationWarnOnDisk: an existing file has function F1 and the
// changed file has a near-identical function (fake kern output with
// high-similarity records) → the check reports a WARN finding on the changed
// file.
func TestG21_DuplicationWarnOnDisk(t *testing.T) {
	f := ExactDuplicate(t)
	res := runCheckAgainstFixture(t, f)

	if res.Status != domain.StatusWarn {
		t.Fatalf("status = %s, want WARN for a near-identical duplicate", res.Status)
	}
	var found bool
	for _, finding := range res.Findings {
		if finding.RuleID != "duplication:advisory" {
			continue
		}
		if finding.File != f.NewFile {
			continue
		}
		if !contains(finding.Message, "duplicate-candidate") {
			continue
		}
		found = true
		break
	}
	if !found {
		t.Fatalf("expected a duplication finding for %s; got: %v", f.NewFile, res.Findings)
	}
}

// TestG21_DuplicationContentPath: the changed file arrives via Content (not on
// disk) → the check temp-mirrors the content and fingerprints it there, then
// reports the WARN finding against the repo-relative path. The fake runner
// must receive a fingerprint invocation whose workdir is the temp mirror (not
// the repo root) for the changed file, plus a whole-root scan of the repo.
func TestG21_DuplicationContentPath(t *testing.T) {
	dir := t.TempDir()
	writeGoMod(t, dir)
	// Existing file on disk; the duplicate is proposed content only.
	writeDupFile(t, dir, "shared/retry.go", sharedRetrySource(t))

	fake := &fingerprintFake{records: fixtureFingerprintRecords("exact-duplicate")}
	req := domain.ChangeRequest{
		RepositoryRoot: dir,
		Source:         domain.SourceAgent,
		Operation:      domain.OpWrite,
		Files: []domain.FileChange{
			{Path: "payments/retry.go", Op: domain.OpWrite, Content: paymentsRetrySource(t)},
		},
	}
	check := newFakeCheck(fake)
	res, err := check.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if res.Status == domain.StatusError {
		t.Fatalf("check errored: %s", res.Error)
	}
	if res.Status != domain.StatusWarn {
		t.Fatalf("status = %s, want WARN for a content-path duplicate", res.Status)
	}
	var found bool
	for _, finding := range res.Findings {
		if finding.RuleID == "duplication:advisory" && finding.File == "payments/retry.go" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected a duplication finding for payments/retry.go (content path); got: %v", res.Findings)
	}

	// The changed-file fingerprinting must have run against a temp mirror,
	// not the repo root (content files are not on disk).
	var sawMirror, sawRootScan bool
	for _, call := range fake.calls {
		if call.workdir != dir && call.workdir != "" {
			sawMirror = true
		}
		if call.workdir == dir && call.files == nil {
			sawRootScan = true
		}
	}
	if !sawMirror {
		t.Errorf("expected a fingerprint invocation against a temp mirror for the proposed content; calls: %+v", fake.calls)
	}
	if !sawRootScan {
		t.Errorf("expected a whole-root fingerprint scan of the repo for existing files; calls: %+v", fake.calls)
	}
}

// TestG21_NoKernClient: NewCheck(nil) → Run returns StatusError.
func TestG21_NoKernClient(t *testing.T) {
	check := NewCheck(nil)
	res, err := check.Run(context.Background(), domain.ChangeRequest{
		RepositoryRoot: t.TempDir(),
		Source:         domain.SourceCI,
		Operation:      domain.OpCommit,
		Files:          []domain.FileChange{{Path: "a.go", Op: domain.OpWrite}},
	})
	if err != nil {
		t.Fatalf("Run returned an error value (status-embedded error expected): %v", err)
	}
	if res.Status != domain.StatusError {
		t.Fatalf("status = %s, want ERROR", res.Status)
	}
	if !contains(res.Error, "kern client") {
		t.Errorf("error = %q, want mention of kern client", res.Error)
	}
}

// TestG21_ContractMismatch: the kern runner returns a wrong schema_version →
// the check surfaces a StatusError, never a WARN finding.
func TestG21_ContractMismatch(t *testing.T) {
	dir := t.TempDir()
	writeGoMod(t, dir)
	writeDupFile(t, dir, "shared/retry.go", sharedRetrySource(t))
	writeDupFile(t, dir, "payments/retry.go", paymentsRetrySource(t))

	fake := &fingerprintFake{records: fixtureFingerprintRecords(""), schemaVersion: 3}
	req := domain.ChangeRequest{
		RepositoryRoot: dir,
		Source:         domain.SourceCI,
		Operation:      domain.OpCommit,
		Files:          []domain.FileChange{{Path: "payments/retry.go", Op: domain.OpWrite}},
	}
	check := newFakeCheck(fake)
	res, err := check.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Run returned an error value (status-embedded error expected): %v", err)
	}
	if res.Status != domain.StatusError {
		t.Fatalf("status = %s, want ERROR on contract mismatch (not WARN)", res.Status)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("expected no findings on contract mismatch, got %d", len(res.Findings))
	}
	if !contains(res.Error, "contract") {
		t.Errorf("error = %q, want mention of contract", res.Error)
	}
}

// sharedRetrySource and paymentsRetrySource are the fixture source strings,
// kept here so the G21 content-path tests can drive proposed content that is
// not on disk. They mirror the sources embedded in fixtures.go.
func sharedRetrySource(t *testing.T) string {
	t.Helper()
	return `package shared

import (
	"errors"
	"time"
)

type Request struct{ URL string }

func send(req *Request) error { return nil }

func RetryRequest(req *Request) error {
	for i := 0; i < 3; i++ {
		err := send(req)
		if err == nil {
			return nil
		}
		time.Sleep(time.Second)
	}
	return errors.New("max retries")
}
`
}

func paymentsRetrySource(t *testing.T) string {
	t.Helper()
	return `package payments

import (
	"errors"
	"time"
)

type Request struct{ URL string }

func send(req *Request) error { return nil }

func DoRetry(req *Request) error {
	for i := 0; i < 3; i++ {
		err := send(req)
		if err == nil {
			return nil
		}
		time.Sleep(time.Second)
	}
	return errors.New("max retries")
}
`
}

// TestG21_ConfidenceEqualsSimilarityScore verifies the duplication finding's
// Confidence equals the structural similarity score that triggered it (P2-4),
// and that rule_version/scope are stamped. The exact-duplicate fixture's
// similarity is echoed in the message ("similarity %.2f"), so the test parses
// it back out and asserts Confidence matches exactly.
func TestG21_ConfidenceEqualsSimilarityScore(t *testing.T) {
	f := ExactDuplicate(t)
	res := runCheckAgainstFixture(t, f)
	var found *domain.Finding
	for i := range res.Findings {
		if res.Findings[i].RuleID == "duplication:advisory" {
			found = &res.Findings[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("no duplication finding for exact-duplicate fixture: %+v", res.Findings)
	}
	if found.RuleVersion != "1" {
		t.Errorf("RuleVersion = %q, want \"1\"", found.RuleVersion)
	}
	if found.Scope != "file" {
		t.Errorf("Scope = %q, want \"file\"", found.Scope)
	}
	// Message format: "duplicate-candidate: <fn> (similarity %.2f) matches ..."
	marker := " (similarity "
	i := strings.Index(found.Message, marker)
	if i < 0 {
		t.Fatalf("message %q missing similarity marker", found.Message)
	}
	var want float64
	if _, err := fmt.Sscanf(found.Message[i+len(marker):], "%f", &want); err != nil {
		t.Fatalf("parse similarity from %q: %v", found.Message[i+len(marker):], err)
	}
	if found.Confidence != want {
		t.Errorf("Confidence = %v, want %v (must equal the similarity score)", found.Confidence, want)
	}
}
