package calibrate

import (
	"strings"
	"testing"
	"time"
)

// seedPrediction writes one prediction directly for a root+ts.
func seedPrediction(t *testing.T, root, kind, target, sub string, files []string, ts time.Time) {
	t.Helper()
	if err := RecordPrediction(root, Prediction{Kind: kind, Change: "c", Target: target, Subsystem: sub, PredictedFiles: files, TS: ts}); err != nil {
		t.Fatalf("seed: %v", err)
	}
}

func TestMatchOutcomesHitMissAndOrder(t *testing.T) {
	root := t.TempDir()
	t0 := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	seedPrediction(t, root, "impact", "A", "web", []string{"web/server.go"}, t0.Add(-2*time.Hour))
	seedPrediction(t, root, "impact", "B", "internal/loop", []string{"internal/loop/loop.go"}, t0.Add(-1*time.Hour))
	seedPrediction(t, root, "impact", "C", "cmd", []string{"cmd/kern/x.go"}, t0.Add(-30*time.Minute))

	srcs := []OutcomeSource{
		// Hit by file intersection (pred A predicted web/server.go).
		{ID: "inc-1", Kind: "impact", Subsystem: "web", Files: []string{"web/server.go"}, TS: t0},
		// Miss: same subsystem (internal/loop) but files not predicted.
		{ID: "inc-2", Kind: "impact", Subsystem: "internal/loop", Files: []string{"internal/loop/other.go"}, TS: t0},
		// Uncounted: different subsystem, no file intersection (pred C is in cmd).
		{ID: "inc-3", Kind: "impact", Subsystem: "docs", Files: []string{"docs/x.md"}, TS: t0},
		// Order rule: incident PRECEDES the prediction → never matches.
		{ID: "inc-4", Kind: "impact", Subsystem: "web", Files: []string{"web/server.go"}, TS: t0.Add(-3 * time.Hour)},
	}
	out, err := MatchOutcomes(root, srcs)
	if err != nil {
		t.Fatalf("MatchOutcomes: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("want 2 matched outcomes (1 hit + 1 miss), got %d: %+v", len(out), out)
	}
	var hit, miss bool
	for _, o := range out {
		if o.Subsystem == "web" {
			hit = o.Hit
		}
		if o.Subsystem == "internal/loop" {
			miss = !o.Hit
		}
	}
	if !hit {
		t.Error("file-intersection candidate must be a HIT")
	}
	if !miss {
		t.Error("same-subsystem non-intersecting candidate must be a MISS")
	}
	// Idempotent: re-matching the same incidents adds nothing.
	out2, err := MatchOutcomes(root, srcs)
	if err != nil {
		t.Fatalf("re-match: %v", err)
	}
	if len(out2) != 0 {
		t.Errorf("re-match must be idempotent (no new outcomes), got %d", len(out2))
	}
	if n, _ := loadLog[Outcome](outcomeLogPath(root)); len(n) != 2 {
		t.Errorf("persisted outcomes = %d, want 2", len(n))
	}
}

func TestMatchOutcomesKindMismatchNeverMatches(t *testing.T) {
	root := t.TempDir()
	t0 := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	seedPrediction(t, root, "verify", "", "", nil, t0.Add(-time.Hour))
	out, err := MatchOutcomes(root, []OutcomeSource{
		{ID: "inc-1", Kind: "impact", Subsystem: "", Files: nil, TS: t0},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 0 {
		t.Errorf("kind mismatch must not match, got %d", len(out))
	}
}

func TestModelConfidenceMathAndInsufficientFloor(t *testing.T) {
	root := t.TempDir()
	t0 := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	// 3 hits + 1 miss for "web" → confidence 0.75, samples 4 → Insufficient.
	for i := 0; i < 3; i++ {
		seedPrediction(t, root, "impact", "A", "web", []string{"web/server.go"}, t0.Add(-time.Duration(10+i)*time.Minute))
	}
	seedPrediction(t, root, "impact", "B", "web", []string{"web/other.go"}, t0.Add(-5*time.Minute))
	if _, err := MatchOutcomes(root, []OutcomeSource{
		{ID: "inc-1", Kind: "impact", Subsystem: "web", Files: []string{"web/server.go"}, TS: t0},
	}); err != nil {
		t.Fatal(err)
	}
	model, err := Model(root)
	if err != nil {
		t.Fatalf("Model: %v", err)
	}
	if len(model) != 1 || model[0].Subsystem != "web" {
		t.Fatalf("model = %+v", model)
	}
	c := model[0]
	if c.Hits != 3 || c.Misses != 1 || c.Samples != 4 {
		t.Errorf("counts = %+v, want hits=3 misses=1 samples=4", c)
	}
	if c.Confidence != 0.75 {
		t.Errorf("confidence = %v, want 0.75", c.Confidence)
	}
	if !c.Insufficient {
		t.Error("samples=4 must be Insufficient (< 5)")
	}
	// 5+ samples clears the floor.
	for i := 0; i < 5; i++ {
		seedPrediction(t, root, "impact", "C", "internal/loop", []string{"internal/loop/loop.go"}, t0.Add(-time.Duration(30+i)*time.Minute))
	}
	if _, err := MatchOutcomes(root, []OutcomeSource{
		{ID: "inc-2", Kind: "impact", Subsystem: "internal/loop", Files: []string{"internal/loop/loop.go"}, TS: t0},
	}); err != nil {
		t.Fatal(err)
	}
	model, _ = Model(root)
	if len(model) != 2 {
		t.Fatalf("model = %+v", model)
	}
	for _, c := range model {
		if c.Subsystem == "internal/loop" && c.Insufficient {
			t.Error("samples=5 must NOT be Insufficient")
		}
	}
}

func TestModelSortsBySubsystem(t *testing.T) {
	root := t.TempDir()
	t0 := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	seedPrediction(t, root, "impact", "A", "cmd", nil, t0.Add(-time.Hour))
	seedPrediction(t, root, "impact", "B", "internal/loop", nil, t0.Add(-time.Hour))
	if _, err := MatchOutcomes(root, []OutcomeSource{
		{ID: "inc-1", Kind: "impact", Subsystem: "cmd", Files: []string{"cmd/x.go"}, TS: t0},
		{ID: "inc-2", Kind: "impact", Subsystem: "internal/loop", Files: []string{"internal/loop/loop.go"}, TS: t0},
	}); err != nil {
		t.Fatal(err)
	}
	model, err := Model(root)
	if err != nil {
		t.Fatal(err)
	}
	if model[0].Subsystem != "cmd" || model[1].Subsystem != "internal/loop" {
		t.Errorf("model not sorted: %+v", model)
	}
}

func TestModelEmptyConfidenceZero(t *testing.T) {
	root := t.TempDir()
	model, err := Model(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(model) != 0 {
		t.Errorf("empty root model = %+v", model)
	}
}

func TestHealthRenders(t *testing.T) {
	root := t.TempDir()
	seedPrediction(t, root, "impact", "A", "web", []string{"web/server.go"}, time.Date(2026, 9, 21, 8, 0, 0, 0, time.UTC))
	if _, err := MatchOutcomes(root, []OutcomeSource{
		{ID: "inc-1", Kind: "impact", Subsystem: "web", Files: []string{"web/server.go"}, TS: time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)},
	}); err != nil {
		t.Fatal(err)
	}
	got, err := Health(root)
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	for _, want := range []string{"calibration: 1 prediction(s) recorded, 1 matched outcome(s)", "web", "100.0%", "insufficient data"} {
		if !strings.Contains(got, want) {
			t.Errorf("Health missing %q:\n%s", want, got)
		}
	}
	// Empty store renders gracefully.
	empty, err := Health(t.TempDir())
	if err != nil || !strings.Contains(empty, "no calibration data yet") {
		t.Errorf("empty Health = %q, %v", empty, err)
	}
}

func TestConfidenceLine(t *testing.T) {
	root := t.TempDir()
	if got := ConfidenceLine(root, "web"); got != "confidence: web insufficient data" {
		t.Errorf("empty model line = %q", got)
	}
	t0 := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	seedPrediction(t, root, "impact", "A", "web", []string{"web/server.go"}, t0.Add(-time.Hour))
	seedPrediction(t, root, "impact", "B", "web", []string{"web/other.go"}, t0.Add(-time.Hour))
	if _, err := MatchOutcomes(root, []OutcomeSource{
		{ID: "inc-1", Kind: "impact", Subsystem: "web", Files: []string{"web/server.go"}, TS: t0},
	}); err != nil {
		t.Fatal(err)
	}
	if got := ConfidenceLine(root, "web"); got != "confidence: web 50.0% (2 samples)" {
		t.Errorf("line = %q", got)
	}
	if got := ConfidenceLine(root, "cmd"); got != "confidence: cmd insufficient data" {
		t.Errorf("unknown subsystem line = %q", got)
	}
}

func TestAggregateConfidenceLine(t *testing.T) {
	root := t.TempDir()
	if got := AggregateConfidenceLine(root); got != "confidence: insufficient data" {
		t.Errorf("empty aggregate = %q", got)
	}
	t0 := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		seedPrediction(t, root, "impact", "A", "web", []string{"web/server.go"}, t0.Add(-time.Duration(10+i)*time.Minute))
		seedPrediction(t, root, "impact", "B", "internal/loop", []string{"internal/loop/loop.go"}, t0.Add(-time.Duration(20+i)*time.Minute))
	}
	seedPrediction(t, root, "impact", "C", "cmd", []string{"cmd/x.go"}, t0.Add(-3*time.Minute))
	seedPrediction(t, root, "impact", "D", "cmd", []string{"cmd/y.go"}, t0.Add(-2*time.Minute))
	// 8 predictions: 6 hit (3 web + 3 internal/loop), 2 miss (cmd) → 75.0%, 8 samples.
	if _, err := MatchOutcomes(root, []OutcomeSource{
		{ID: "inc-1", Kind: "impact", Subsystem: "web", Files: []string{"web/server.go"}, TS: t0},
		{ID: "inc-2", Kind: "impact", Subsystem: "internal/loop", Files: []string{"internal/loop/loop.go"}, TS: t0},
		{ID: "inc-3", Kind: "impact", Subsystem: "cmd", Files: []string{"cmd/z.go"}, TS: t0},
	}); err != nil {
		t.Fatal(err)
	}
	if got := AggregateConfidenceLine(root); got != "confidence: 75.0% (8 samples)" {
		t.Errorf("aggregate = %q", got)
	}
}
