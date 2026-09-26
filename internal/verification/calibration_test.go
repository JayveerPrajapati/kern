package verification

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestVerifyRecordsPrediction proves the verify choke point records a
// "verify"-kind calibration prediction (Feature Batch C) — the append lands
// in <root>/.kern/predictions.jsonl with the run's verdict.
func TestVerifyRecordsPrediction(t *testing.T) {
	root := verifyFixture(t)
	e := NewEngine(root)
	res := e.Verify([]string{"security"}) // no exec: pure file scan, fast
	if res.Verdict == "" {
		t.Fatal("Verify returned an empty verdict")
	}
	data, err := os.ReadFile(filepath.Join(root, ".kern", "predictions.jsonl"))
	if err != nil {
		t.Fatalf("predictions log not written: %v", err)
	}
	if !strings.Contains(string(data), `"kind":"verify"`) {
		t.Errorf("log line missing verify kind: %s", data)
	}
	if !strings.Contains(string(data), `"verdict":"`+string(res.Verdict)) {
		t.Errorf("log line missing verdict %s: %s", res.Verdict, data)
	}
}

// TestVerifyRecordingFailureNeverFailsVerify proves the calibration append is
// strictly best-effort: when the prediction log cannot be written (here the
// path is a directory), the verify run still completes with a verdict.
func TestVerifyRecordingFailureNeverFailsVerify(t *testing.T) {
	root := verifyFixture(t)
	if err := os.MkdirAll(filepath.Join(root, ".kern", "predictions.jsonl"), 0o755); err != nil {
		t.Fatal(err)
	}
	e := NewEngine(root)
	res := e.Verify([]string{"security"})
	if res.Verdict == "" {
		t.Fatal("Verify must still complete with a verdict when recording fails")
	}
	if res.Security == nil {
		t.Error("security check must still run when recording fails")
	}
}
