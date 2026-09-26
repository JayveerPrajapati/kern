package main

import (
	"strings"
	"testing"
)

// TestCalibrationFlagParses locks the --calibration doctor flag into the
// shared flag parser (mirroring --arch-drift).
func TestCalibrationFlagParses(t *testing.T) {
	f, _, err := parseFlags([]string{"--calibration"})
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	if !f.calibration {
		t.Error("--calibration must set f.calibration")
	}
}

// TestCalibrationFindingsSection renders the doctor calibration section on a
// fresh fixture: it must produce an "ok"-level finding whose detail shows the
// prediction count and the no-outcome-data note (non-fatal).
func TestCalibrationFindingsSection(t *testing.T) {
	dir := calibrateCLIFixture(t)
	findings := calibrationFindings(dir)
	found := false
	for _, f := range findings {
		if f.Check != "calibration" {
			continue
		}
		found = true
		if f.Level == "warn" || f.Level == "fail" {
			t.Fatalf("calibration section must be non-fatal, got %s: %s", f.Level, f.Detail)
		}
		if !strings.Contains(f.Detail, "prediction") {
			t.Errorf("calibration detail missing prediction count: %s", f.Detail)
		}
		if !strings.Contains(f.Detail, "no outcome data") {
			t.Errorf("calibration detail missing no-outcome-data note: %s", f.Detail)
		}
	}
	if !found {
		t.Fatal("no calibration finding produced")
	}
}

// TestRunVerifyTextAppendsConfidenceLine proves the CLI kern verify text path
// appends the aggregate calibration confidence line (fresh store →
// "insufficient data") and that --json output stays a plain object.
func TestRunVerifyTextAppendsConfidenceLine(t *testing.T) {
	t.Setenv("KERN_ALLOW_EXEC", "1")
	root := jsonCliFixture(t)
	out := captureStdout(t, func() { runVerify([]string{"security", "--root", root}) })
	if !strings.Contains(out, "confidence: insufficient data") {
		t.Fatalf("verify text missing confidence line:\n%s", out)
	}
	// JSON path unchanged: still a structured object, no text lines.
	jout := captureStdout(t, func() { runVerify([]string{"security", "--root", root, "--json"}) })
	if !strings.HasPrefix(strings.TrimSpace(jout), "{") {
		t.Fatalf("verify --json must stay a JSON object:\n%s", jout)
	}
	if strings.Contains(jout, "confidence:") {
		t.Errorf("verify --json must not contain the text confidence line:\n%s", jout)
	}
}
