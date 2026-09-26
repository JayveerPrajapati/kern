package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/calibrate"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/incident"
	"github.com/JayveerPrajapati/kern/internal/learning"
	"github.com/JayveerPrajapati/kern/internal/testfixture"
	"github.com/JayveerPrajapati/kern/internal/whatif"
)

// calibrationClaimMemories returns the typed-claim calibration constraint
// memories currently stored: MemoryConstraint entries in the reserved
// "calibration:impact:" scope carrying the INFERENCE claim type.
func calibrationClaimMemories(t *testing.T, p *Platform) []domain.Memory {
	t.Helper()
	mems, err := p.Memory().List(domain.MemoryConstraint)
	if err != nil {
		t.Fatalf("memory List: %v", err)
	}
	var out []domain.Memory
	for _, m := range mems {
		if m.ClaimType == domain.ClaimInference && strings.HasPrefix(m.Scope, "calibration:impact:") {
			out = append(out, m)
		}
	}
	return out
}

// readPredictionLog returns the raw predictions log lines for root.
func readPredictionLog(t *testing.T, root string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, ".kern", "predictions.jsonl"))
	if err != nil {
		t.Fatalf("predictions log not written: %v", err)
	}
	var lines []string
	for _, ln := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if ln != "" {
			lines = append(lines, ln)
		}
	}
	return lines
}

// TestWhatIfRecordsPrediction proves the what-if choke point appends an
// impact-kind prediction to .kern/predictions.jsonl.
func TestWhatIfRecordsPrediction(t *testing.T) {
	if testing.Short() {
		t.Skip("index build; skipped with -short")
	}
	root := testfixture.Repo(t)
	p, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := NewTaskService(p, nil)
	if _, _, err := ts.WhatIf(whatif.RemoveSymbol, "NewServer", ""); err != nil {
		t.Fatalf("WhatIf: %v", err)
	}
	lines := readPredictionLog(t, root)
	if len(lines) != 1 {
		t.Fatalf("want 1 prediction line, got %d: %v", len(lines), lines)
	}
	if !strings.Contains(lines[0], `"kind":"impact"`) {
		t.Errorf("prediction kind = %s", lines[0])
	}
	if !strings.Contains(lines[0], `"subsystem":"web"`) {
		t.Errorf("prediction subsystem = %s (NewServer lives in web/)", lines[0])
	}
	if !strings.Contains(lines[0], `web/handler.go`) && !strings.Contains(lines[0], `"predicted_files"`) {
		t.Errorf("prediction missing predicted files: %s", lines[0])
	}
}

// TestImpactRecordsPrediction proves the impact choke point appends an
// impact-kind prediction carrying the file set the impact path computed.
func TestImpactRecordsPrediction(t *testing.T) {
	if testing.Short() {
		t.Skip("index build; skipped with -short")
	}
	root := testfixture.Repo(t)
	p, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := NewTaskService(p, nil)
	if _, _, _, err := ts.Impact("NewServer"); err != nil {
		t.Fatalf("Impact: %v", err)
	}
	lines := readPredictionLog(t, root)
	if len(lines) != 1 {
		t.Fatalf("want 1 prediction line, got %d: %v", len(lines), lines)
	}
	if !strings.Contains(lines[0], `"kind":"impact"`) {
		t.Errorf("prediction kind = %s", lines[0])
	}
	if !strings.Contains(lines[0], `"subsystem":"web"`) || !strings.Contains(lines[0], `"predicted_files"`) {
		t.Errorf("prediction incomplete: %s", lines[0])
	}
}

// TestImpactRenderAppendsConfidenceLine seeds a matched outcome for the
// target's subsystem and proves the rendered impact text (the shared CLI+MCP
// render path) ends with the confidence line, while the JSON report shape is
// untouched.
func TestImpactRenderAppendsConfidenceLine(t *testing.T) {
	if testing.Short() {
		t.Skip("index build; skipped with -short")
	}
	root := testfixture.Repo(t)
	p, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := NewTaskService(p, nil)
	if _, _, text1, err := ts.Impact("NewServer"); err != nil {
		t.Fatalf("Impact: %v", err)
	} else if !strings.HasSuffix(strings.TrimSpace(text1), "confidence: web insufficient data") {
		t.Errorf("fresh render must end with insufficient-data line; got:\n%s", text1)
	}
	// Seed an observed outcome AFTER the recorded prediction (file hit).
	future := time.Now().Add(24 * time.Hour)
	if _, err := calibrate.MatchOutcomes(root, []calibrate.OutcomeSource{
		{ID: "inc-seed", Kind: "impact", Subsystem: "web", Files: []string{"web/handler.go"}, TS: future},
	}); err != nil {
		t.Fatalf("MatchOutcomes: %v", err)
	}
	// Re-run: the render still shows the seeded match (the render path only
	// reads the persisted model — matching happens on CalibrationHealth).
	_, rep, text2, err := ts.Impact("NewServer")
	if err != nil {
		t.Fatalf("Impact: %v", err)
	}
	if !strings.HasSuffix(strings.TrimSpace(text2), "confidence: web 100.0% (1 samples)") {
		t.Errorf("render must end with the confidence line; got:\n%s", text2)
	}
	// JSON shape unchanged: report target and risk still present.
	if rep.Target != "NewServer" || rep.Risk == "" {
		t.Errorf("ImpactReport shape changed: %+v", rep)
	}
}

// TestCalibrationHealthMatchesIncidents proves the app accessor gathers
// incidents from the incident store, matches them, and renders the health
// summary; an empty store reports "no outcome data" gracefully.
func TestCalibrationHealthMatchesIncidents(t *testing.T) {
	if testing.Short() {
		t.Skip("index build; skipped with -short")
	}
	root := testfixture.Repo(t)
	p, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := NewTaskService(p, nil)
	if _, _, _, err := ts.Impact("NewServer"); err != nil {
		t.Fatalf("Impact: %v", err)
	}
	// Empty incident store → graceful no-outcome-data note.
	health, err := ts.CalibrationHealth()
	if err != nil {
		t.Fatalf("CalibrationHealth: %v", err)
	}
	if !strings.Contains(health, "no outcome data") || !strings.Contains(health, "1 prediction(s)") {
		t.Errorf("empty-store health = %q", health)
	}
	// Save an incident whose root cause touches a predicted file.
	store := incident.NewStore(root)
	_, err = store.Save(&domain.Incident{
		ID:        "inc-1",
		CreatedAt: time.Now().Add(24 * time.Hour),
		RootCause: &domain.RootCause{Files: []string{"web/handler.go"}},
	})
	if err != nil {
		t.Fatalf("save incident: %v", err)
	}
	health, err = ts.CalibrationHealth()
	if err != nil {
		t.Fatalf("CalibrationHealth: %v", err)
	}
	if !strings.Contains(health, "calibration: 1 prediction(s) recorded, 1 matched outcome(s)") {
		t.Errorf("health after incident = %q", health)
	}
	if !strings.Contains(health, "web") || !strings.Contains(health, "100.0%") {
		t.Errorf("health table missing web row: %q", health)
	}
}

// TestVerifyConfidenceLineAccessor proves the aggregate line surfaces through
// the app accessor used by the CLI and MCP verify render paths.
func TestVerifyConfidenceLineAccessor(t *testing.T) {
	root := t.TempDir()
	if got := VerifyConfidenceLine(root); got != "confidence: insufficient data" {
		t.Errorf("empty root = %q", got)
	}
	t0 := time.Now().Add(-time.Hour)
	for i := 0; i < 5; i++ {
		_ = calibrate.RecordPrediction(root, calibrate.Prediction{
			Kind: "impact", Change: "c", Target: "t", Subsystem: "web",
			PredictedFiles: []string{"web/x.go"}, TS: t0.Add(time.Duration(i) * time.Minute),
		})
	}
	if _, err := calibrate.MatchOutcomes(root, []calibrate.OutcomeSource{
		{ID: "inc-1", Kind: "impact", Subsystem: "web", Files: []string{"web/x.go"}, TS: time.Now()},
	}); err != nil {
		t.Fatal(err)
	}
	if got := VerifyConfidenceLine(root); got != "confidence: 100.0% (5 samples)" {
		t.Errorf("aggregate line = %q", got)
	}
}

// TestCalibrationContradictionWritesInferenceMemory proves the
// "WhatIf said X, reality said Y" loop: matching an outcome that CONTRADICTS
// a recorded prediction writes a typed INFERENCE claim memory through the
// learning path with the deterministic statement and provenance (prediction
// log entry id/timestamp + outcome source); a later CONFIRMING outcome
// upserts the same constraint (no duplicate) with refreshed counts,
// confidence, and provenance.
func TestCalibrationContradictionWritesInferenceMemory(t *testing.T) {
	if testing.Short() {
		t.Skip("index build; skipped with -short")
	}
	root := testfixture.Repo(t)
	p, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := NewTaskService(p, nil)
	if _, _, _, err := ts.Impact("NewServer"); err != nil {
		t.Fatalf("Impact: %v", err)
	}
	store := incident.NewStore(root)

	// Contradiction: same subsystem (web) but files NOT predicted → MISS.
	if _, err := store.Save(&domain.Incident{
		ID:        "inc-1",
		CreatedAt: time.Now().Add(24 * time.Hour),
		RootCause: &domain.RootCause{Files: []string{"web/unpredicted.go"}},
	}); err != nil {
		t.Fatalf("save incident: %v", err)
	}
	if _, err := ts.CalibrationHealth(); err != nil {
		t.Fatalf("CalibrationHealth: %v", err)
	}
	mems := calibrationClaimMemories(t, p)
	if len(mems) != 1 {
		t.Fatalf("want 1 typed-claim memory, got %d", len(mems))
	}
	m := mems[0]
	if m.Type != domain.MemoryConstraint || m.ClaimType != domain.ClaimInference {
		t.Fatalf("memory = %+v, want MemoryConstraint + INFERENCE claim", m)
	}
	if !strings.Contains(m.Content, "subsystem web prediction (impact of NewServer) was WRONG in 1 cases (confidence before 0.0%, after 0.0%)") {
		t.Errorf("contradiction statement missing from content: %q", m.Content)
	}
	if !strings.Contains(m.Provenance, "prediction ") || !strings.Contains(m.Provenance, "outcome inc-1") {
		t.Errorf("provenance must carry prediction ref + outcome source, got %q", m.Provenance)
	}
	// The claim memory also surfaces as a typed-claim pattern via learning.
	patterns, err := learning.New(p.Memory()).Patterns()
	if err != nil {
		t.Fatalf("learning Patterns(): %v", err)
	}
	foundClaimPattern := false
	for _, pat := range patterns {
		if pat.Key == "claim:INFERENCE:scope:calibration:impact:web" && pat.ClaimType == domain.ClaimInference && pat.Provenance.Count == 1 {
			foundClaimPattern = true
		}
	}
	if !foundClaimPattern {
		t.Errorf("claim pattern not surfaced by claim type: %+v", patterns)
	}

	// Confirmation: an incident touching a predicted file → HIT. Upsert:
	// still exactly one memory, statement refreshed to CONFIRMED with 2 cases
	// and confidence 0.0% → 50.0%, provenance refreshed to the new outcome.
	if _, err := store.Save(&domain.Incident{
		ID:        "inc-2",
		CreatedAt: time.Now().Add(48 * time.Hour),
		RootCause: &domain.RootCause{Files: []string{"web/handler.go"}},
	}); err != nil {
		t.Fatalf("save incident 2: %v", err)
	}
	if _, err := ts.CalibrationHealth(); err != nil {
		t.Fatalf("CalibrationHealth (2nd): %v", err)
	}
	mems = calibrationClaimMemories(t, p)
	if len(mems) != 1 {
		t.Fatalf("upsert must not duplicate: want 1 memory, got %d", len(mems))
	}
	m = mems[0]
	if !strings.Contains(m.Content, "was CONFIRMED in 2 cases (confidence before 0.0%, after 50.0%)") {
		t.Errorf("upserted statement = %q", m.Content)
	}
	if !strings.Contains(m.Provenance, "outcome inc-2") {
		t.Errorf("provenance must refresh to outcome inc-2, got %q", m.Provenance)
	}

	// Idempotent re-run: nothing new to match, memory unchanged (still 1).
	if _, err := ts.CalibrationHealth(); err != nil {
		t.Fatalf("CalibrationHealth (3rd): %v", err)
	}
	if len(calibrationClaimMemories(t, p)) != 1 {
		t.Fatalf("re-run must not add memories")
	}
}

// TestCalibrationClaimsAbsentStoreWritesNothing proves the nil-guard path:
// when there is nothing to match (no predictions) the calibration health
// neither fails nor writes a typed-claim memory, and a nil platform makes
// the claim recorder a no-op (no panic, no write).
func TestCalibrationClaimsAbsentStoreWritesNothing(t *testing.T) {
	if testing.Short() {
		t.Skip("index build; skipped with -short")
	}
	root := testfixture.Repo(t)
	p, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := NewTaskService(p, nil)
	// Incident present but NO prediction recorded → nothing matches.
	if _, err := incident.NewStore(root).Save(&domain.Incident{
		ID:        "inc-x",
		CreatedAt: time.Now().Add(24 * time.Hour),
		RootCause: &domain.RootCause{Files: []string{"web/unpredicted.go"}},
	}); err != nil {
		t.Fatalf("save incident: %v", err)
	}
	if _, err := ts.CalibrationHealth(); err != nil {
		t.Fatalf("CalibrationHealth must not fail on unmatchable store: %v", err)
	}
	if mems := calibrationClaimMemories(t, p); len(mems) != 0 {
		t.Fatalf("unmatchable store must write no claim memory, got %d", len(mems))
	}
	// Nil platform: the guarded recorder must not panic and writes nothing.
	ts2 := &TaskService{platform: nil}
	ts2.recordCalibrationClaims([]calibrate.Outcome{{ID: "x", Kind: "impact", Hit: false, Subsystem: "web"}}, nil)
}
