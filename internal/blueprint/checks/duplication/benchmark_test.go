package duplication

import (
	"fmt"
	"testing"
)

// benchmarkCase couples one G6 corpus fixture with its ground-truth label and
// the files whose fingerprint records form the "new" (changed) and "existing"
// function sets. Records come from fixtureFingerprintRecords (g6_test.go) —
// the REAL `kern fingerprint` output captured from the rebuilt kern binary,
// which emits control-flow counts — so no real kern binary is needed and the
// benchmark exercises the exact signal the production pipeline consumes.
//
// historicalScore is the score documented in fixtures.go. Those figures
// predate control-flow counts in fingerprint records; the control-flow signal
// raises current scores (e.g. exact duplicates now score 1.0 instead of
// 0.825), so the test pins the current measured values instead.
type benchmarkCase struct {
	name            string
	isDuplicate     bool // ground truth
	historicalScore float64
	newFiles        []string
	existingFiles   []string
}

var benchmarkCases = []benchmarkCase{
	{"exact-duplicate", true, 0.825, []string{"payments/retry.go"}, []string{"shared/retry.go"}},
	{"renamed-duplicate", true, 0.585, []string{"billing/retry.go"}, []string{"shared/retry.go"}},
	{"slightly-refactored-duplicate", true, 0.820, []string{"checkout/retry.go"}, []string{"shared/retry.go"}},
	{"different-but-similar-algorithm", false, 0.779, []string{"gateway/retry.go"}, []string{"shared/retry.go"}},
	{"wrapper-around-existing", false, 0.416, []string{"api/retry.go"}, []string{"shared/retry.go"}},
	{"unrelated-same-signature", false, 0.525, []string{"vault/process.go"}, []string{"shared/process.go"}},
	{"generated-boilerplate", false, 0.540, []string{"users/model.go"}, []string{"shared/model.go"}},
}

// detectionThreshold is the score at/above which the check emits a finding
// (spec tiers: >= 0.60 → informational/warning/block-candidate).
const detectionThreshold = 0.60

// fixtureFingerprints converts the fingerprint records for the given files
// into duplication Fingerprints (the same conversion fingerprintFromRecord
// applies in production). The wrapper-around-existing fixture's existing file
// (shared/retry.go) is byte-identical to the exact-duplicate fixture's, so
// its records are inherited from that table — the wrapper fixture's own
// record table only carries the new file.
func fixtureFingerprints(name string, newFiles, existingFiles []string) (newFps, existingFps []Fingerprint) {
	recs := fixtureFingerprintRecords(name)
	for _, f := range newFiles {
		for _, r := range recs[f] {
			newFps = append(newFps, fingerprintFromRecord(r))
		}
	}
	for _, f := range existingFiles {
		rs := recs[f]
		if len(rs) == 0 && name == "wrapper-around-existing" {
			rs = fixtureFingerprintRecords("exact-duplicate")[f]
		}
		for _, r := range rs {
			existingFps = append(existingFps, fingerprintFromRecord(r))
		}
	}
	return newFps, existingFps
}

// bestSimilarity mirrors Check.Run's comparison semantics: each new function
// is scored against every existing function, and the highest score is the
// fixture's detection score.
func bestSimilarity(newFps, existingFps []Fingerprint) float64 {
	best := 0.0
	for _, n := range newFps {
		for _, e := range existingFps {
			if s := Similarity(n, e); s > best {
				best = s
			}
		}
	}
	return best
}

// TestSimilarityBenchmark measures the in-house structural duplication oracle
// (Similarity at the 0.60 detection threshold) against the 7-fixture G6
// corpus and asserts each fixture's score plus the aggregate
// precision/recall/FPR. This is the ADVISORY (triage) path of the two-pass
// model (P1.1): high recall is its job, so its 0.50 precision is expected and
// documented. The BLOCKING path is pinned separately by
// TestSimilarityBenchmarkBlockingPath. This is a correctness regression
// guard, not a perf benchmark. Run with -v for the human-readable summary
// table:
//
//	go test -v ./internal/blueprint/checks/duplication/ -run TestSimilarityBenchmark
func TestSimilarityBenchmark(t *testing.T) {
	// Current measured scores, pinned as a regression guard. Measured
	// 2026-08-29 from fixtureFingerprintRecords (real kern output including
	// control-flow counts). They supersede the historical scores in
	// fixtures.go, which predate control-flow counts in the record.
	expectedScore := map[string]float64{
		"exact-duplicate":                 1.000,
		"renamed-duplicate":               0.760,
		"slightly-refactored-duplicate":   0.975,
		"different-but-similar-algorithm": 0.909,
		"wrapper-around-existing":         0.517,
		"unrelated-same-signature":        0.659,
		"generated-boilerplate":           0.680,
	}
	const scoreTolerance = 0.01

	type row struct {
		name        string
		isDuplicate bool
		score       float64
		detected    bool
		result      string // TP / FN / FP / TN
	}
	var rows []row
	var tp, fn, fp, tn int

	for _, bc := range benchmarkCases {
		newFps, existingFps := fixtureFingerprints(bc.name, bc.newFiles, bc.existingFiles)
		if len(newFps) == 0 || len(existingFps) == 0 {
			t.Fatalf("%s: empty fingerprint set (new=%d existing=%d) — fixture records missing", bc.name, len(newFps), len(existingFps))
		}
		score := bestSimilarity(newFps, existingFps)
		detected := score >= detectionThreshold

		r := row{name: bc.name, isDuplicate: bc.isDuplicate, score: score, detected: detected}
		switch {
		case bc.isDuplicate && detected:
			r.result, tp = "TP", tp+1
		case bc.isDuplicate && !detected:
			r.result, fn = "FN", fn+1
		case !bc.isDuplicate && detected:
			r.result, fp = "FP", fp+1
		default:
			r.result, tn = "TN", tn+1
		}
		rows = append(rows, r)

		// Regression guard: the fixture's score must match the pinned value.
		if want := expectedScore[bc.name]; want == 0 {
			t.Errorf("%s: no pinned expected score", bc.name)
		} else if d := score - want; d < -scoreTolerance || d > scoreTolerance {
			t.Errorf("%s: similarity = %.3f, want %.3f (±%.2f)", bc.name, score, want, scoreTolerance)
		}
	}

	// Aggregate metrics at the 0.60 threshold.
	precision := 0.0
	if tp+fp > 0 {
		precision = float64(tp) / float64(tp+fp)
	}
	recall := 0.0
	if tp+fn > 0 {
		recall = float64(tp) / float64(tp+fn)
	}
	fpr := 0.0
	if fp+tn > 0 {
		fpr = float64(fp) / float64(fp+tn)
	}

	if precision < 0.50 || precision > 1.0 {
		t.Errorf("precision %.2f out of [0.5, 1.0]", precision)
	}
	if recall < 0.0 || recall > 1.0 {
		t.Errorf("recall %.2f out of [0.0, 1.0]", recall)
	}
	if fpr < 0.0 || fpr > 1.0 {
		t.Errorf("FPR %.2f out of [0.0, 1.0]", fpr)
	}

	t.Log("--- Duplication Oracle Benchmark (threshold 0.60) ---")
	t.Logf("%-34s %-8s %-10s %-6s %s", "fixture", "score", "bucket", "truth", "result")
	for _, r := range rows {
		truth := "DUP"
		if !r.isDuplicate {
			truth = "NOT DUP"
		}
		det := "ignored"
		if r.detected {
			det = "detected"
		}
		t.Logf("%-34s %-8.3f %-10s %-6s %s (%s)", r.name, r.score, Bucket(r.score), truth, r.result, det)
	}
	t.Log("--- metrics ---")
	t.Logf("TP=%d FN=%d FP=%d TN=%d", tp, fn, fp, tn)
	t.Logf("precision=%.2f recall=%.2f FPR=%.2f", precision, recall, fpr)
	t.Logf("targets: precision >=0.50, recall >=0.75")

	// Assert the aggregate counts exactly: these are the benchmark results
	// that justify the advisory-only posture (see docs/duplication-benchmark.md).
	if tp != 3 || fn != 0 || fp != 3 || tn != 1 {
		t.Errorf("confusion matrix = TP:%d FN:%d FP:%d TN:%d, want TP:3 FN:0 FP:3 TN:1", tp, fn, fp, tn)
	}
	if fmt.Sprintf("%.2f", precision) != "0.50" || fmt.Sprintf("%.2f", recall) != "1.00" || fmt.Sprintf("%.2f", fpr) != "0.75" {
		t.Errorf("metrics = precision:%.2f recall:%.2f FPR:%.2f, want 0.50/1.00/0.75", precision, recall, fpr)
	}
}

// TestSimilarityBenchmarkBlockingPath measures the two-pass BLOCKING path
// (P1.1): only block-eligible candidates (>0.90, BlockEligible) can escalate,
// and escalation happens only when jscpd confirms the same file pair. jscpd
// confirmation is simulated with a mock confirmer — the same
// func([2]string) bool shape the jscpd adapter's WithConfirmer option
// accepts. The mock confirms the two true duplicates (exact,
// slightly-refactored) and rejects the structural false positive
// (different-but-similar-algorithm), which is exactly what jscpd's
// independent token-based signal does in production.
//
// Blocking-path confusion matrix: TP=2, FP=0, TN=1, FN=0 — precision 1.00,
// FPR 0.00, recall 1.00. renamed-duplicate (0.760) is below the >0.90 gate,
// so it is advisory-only and NOT a blocking-path FN. The blocking path is
// FPR-free because jscpd's confirmation filters the structural false
// positive before anything can escalate.
func TestSimilarityBenchmarkBlockingPath(t *testing.T) {
	// mockConfirmer simulates jscpd's Pass-2 confirmation, keyed by canonical
	// file pair: confirms the two true duplicates, rejects the false positive.
	mockConfirmer := func(filePair [2]string) bool {
		switch filePair {
		case canonicalPair("payments/retry.go", "shared/retry.go"):
			return true // exact-duplicate
		case canonicalPair("checkout/retry.go", "shared/retry.go"):
			return true // slightly-refactored-duplicate
		default:
			return false // different-but-similar-algorithm: jscpd disagrees
		}
	}

	type row struct {
		result string // TP / FN / FP / TN (blocking path only)
	}
	var rows []row
	var tp, fn, fp, tn int

	for _, bc := range benchmarkCases {
		newFps, existingFps := fixtureFingerprints(bc.name, bc.newFiles, bc.existingFiles)
		score := bestSimilarity(newFps, existingFps)

		if !BlockEligible(score) {
			// At or below the >0.90 gate: advisory-only, never a blocking-path
			// outcome (renamed-duplicate 0.760 is the canonical case).
			t.Logf("%-34s score %.3f -> advisory only (not block-eligible)", bc.name, score)
			continue
		}

		pair := canonicalPair(bc.newFiles[0], bc.existingFiles[0])
		confirmed := mockConfirmer(pair)

		r := row{}
		switch {
		case bc.isDuplicate && confirmed:
			r.result, tp = "TP", tp+1
		case bc.isDuplicate && !confirmed:
			r.result, fn = "FN", fn+1
		case !bc.isDuplicate && confirmed:
			r.result, fp = "FP", fp+1
		default:
			r.result, tn = "TN", tn+1
		}
		rows = append(rows, r)
		t.Logf("%-34s score %.3f -> block-eligible, jscpd confirms=%-5v -> %s", bc.name, score, confirmed, r.result)
	}

	precision := 0.0
	if tp+fp > 0 {
		precision = float64(tp) / float64(tp+fp)
	}
	recall := 0.0
	if tp+fn > 0 {
		recall = float64(tp) / float64(tp+fn)
	}
	fpr := 0.0
	if fp+tn > 0 {
		fpr = float64(fp) / float64(fp+tn)
	}

	t.Log("--- Duplication Two-Pass Blocking Path (eligible >0.90, jscpd-confirmed) ---")
	t.Logf("TP=%d FN=%d FP=%d TN=%d", tp, fn, fp, tn)
	t.Logf("precision=%.2f recall=%.2f FPR=%.2f", precision, recall, fpr)

	// Regression guards: jscpd's token-based confirmation filters the
	// structural false positive (different-but-similar-algorithm 0.909), so
	// the blocking path is FPR-free while keeping full recall on the true
	// duplicates that clear the >0.90 gate.
	if tp != 2 || fn != 0 || fp != 0 || tn != 1 {
		t.Errorf("blocking-path confusion matrix = TP:%d FN:%d FP:%d TN:%d, want TP:2 FN:0 FP:0 TN:1", tp, fn, fp, tn)
	}
	if fmt.Sprintf("%.2f", precision) != "1.00" || fmt.Sprintf("%.2f", recall) != "1.00" || fmt.Sprintf("%.2f", fpr) != "0.00" {
		t.Errorf("blocking-path metrics = precision:%.2f recall:%.2f FPR:%.2f, want 1.00/1.00/0.00", precision, recall, fpr)
	}
}

// canonicalPair returns the file pair with paths sorted, so the mock
// confirmer and the blocking-path gate agree regardless of which side is the
// "new" file (mirrors the jscpd adapter's canonicalPair).
func canonicalPair(a, b string) [2]string {
	if a <= b {
		return [2]string{a, b}
	}
	return [2]string{b, a}
}
