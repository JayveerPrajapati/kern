package calibrate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSubsystemOf(t *testing.T) {
	cases := map[string]string{
		"internal/loop/loop.go":     "internal/loop",
		"internal/app/task.go":      "internal/app",
		"cmd/kern/flags.go":         "cmd",
		"web/handler.go":            "web",
		"go.mod":                    "go.mod",
		"docs/architecture/x.md":    "docs",
		"./internal/calibrate/x.go": "internal/calibrate",
		"":                          "",
	}
	for in, want := range cases {
		if got := SubsystemOf(in); got != want {
			t.Errorf("SubsystemOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRecordPredictionRoundTrip(t *testing.T) {
	root := t.TempDir()
	p := Prediction{
		Kind:           "impact",
		Change:         "rewrite loop",
		Target:         "NewServer",
		Subsystem:      "web",
		PredictedFiles: []string{"web/server.go"},
		TS:             time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC),
	}
	if err := RecordPrediction(root, p); err != nil {
		t.Fatalf("RecordPrediction: %v", err)
	}
	data, err := os.ReadFile(predictionLogPath(root))
	if err != nil {
		t.Fatalf("log missing: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 1 {
		t.Fatalf("want 1 line, got %d: %q", len(lines), data)
	}
	var got Prediction
	if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
		t.Fatalf("line not valid JSON: %v", err)
	}
	if got.ID == "" || got.Kind != "impact" || got.Subsystem != "web" || !got.TS.Equal(p.TS) {
		t.Errorf("round-trip mismatch: %+v", got)
	}
	if len(got.PredictedFiles) != 1 || got.PredictedFiles[0] != "web/server.go" {
		t.Errorf("PredictedFiles lost: %v", got.PredictedFiles)
	}
	// Deterministic ID: identical inputs (incl. TS) hash identically.
	p2 := p
	if err := RecordPrediction(root, p2); err != nil {
		t.Fatalf("RecordPrediction: %v", err)
	}
	data2, _ := os.ReadFile(predictionLogPath(root))
	lines2 := strings.Split(strings.TrimSpace(string(data2)), "\n")
	if len(lines2) != 2 {
		t.Fatalf("want 2 lines after second append, got %d", len(lines2))
	}
	if !strings.Contains(lines2[0], got.ID) || !strings.Contains(lines2[1], got.ID) {
		t.Errorf("identical inputs must share the deterministic ID: %s", got.ID)
	}
	// 0600 perms.
	if st, err := os.Stat(predictionLogPath(root)); err == nil && st.Mode().Perm() != 0o600 {
		t.Errorf("log perms = %o, want 600", st.Mode().Perm())
	}
}

func TestRecordPredictionCapTrimsOldest(t *testing.T) {
	root := t.TempDir()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < predictionLogCap+10; i++ {
		if err := RecordPrediction(root, Prediction{
			Kind:      "impact",
			Change:    "c",
			Target:    "t",
			Subsystem: "web",
			TS:        base.Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatalf("RecordPrediction %d: %v", i, err)
		}
	}
	lines, err := readJSONLines(predictionLogPath(root))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(lines) != predictionLogCap {
		t.Fatalf("want %d lines, got %d", predictionLogCap, len(lines))
	}
	// The 10 oldest (ts = base..base+9s) must be gone; the newest survives.
	var first, last Prediction
	_ = json.Unmarshal(lines[0], &first)
	_ = json.Unmarshal(lines[len(lines)-1], &last)
	if !first.TS.Equal(base.Add(time.Duration(10) * time.Second)) {
		t.Errorf("oldest kept ts = %v, want base+10s", first.TS)
	}
	if !last.TS.Equal(base.Add(time.Duration(predictionLogCap+9) * time.Second)) {
		t.Errorf("newest ts = %v, want base+%ds", last.TS, predictionLogCap+9)
	}
}

func TestRecordPredictionMkdirsDotKern(t *testing.T) {
	root := filepath.Join(t.TempDir(), "nested", "repo")
	if err := RecordPrediction(root, Prediction{Kind: "verify", Verdict: "PASS", TS: time.Now()}); err != nil {
		t.Fatalf("RecordPrediction into nested root: %v", err)
	}
	if _, err := os.Stat(predictionLogPath(root)); err != nil {
		t.Fatalf("log not created: %v", err)
	}
}

func TestRecordPredictionFailsOnFileRoot(t *testing.T) {
	// root pointing at a regular FILE: MkdirAll fails → error surfaces.
	root := t.TempDir()
	f := filepath.Join(root, "not-a-dir")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RecordPrediction(f, Prediction{Kind: "verify"}); err == nil {
		t.Error("RecordPrediction with file root must error")
	}
}
