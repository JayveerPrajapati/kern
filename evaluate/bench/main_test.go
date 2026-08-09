package main

import "testing"

// TestGatesAreMet pins the benchmark's hard gates so a regression in any
// compression surface fails CI instead of silently shipping weaker numbers.
func TestGatesAreMet(t *testing.T) {
	failures := checkGates(runMetrics())
	if len(failures) > 0 {
		t.Fatalf("bench gates failed: %v", failures)
	}
}

// TestSamplesAreNonDegenerate guards against accidentally replacing the corpus
// with inputs that produce no measurable output (the harness only measures
// line-structured inputs honestly).
func TestSamplesAreNonDegenerate(t *testing.T) {
	for _, m := range runMetrics() {
		if m.raw == "" || m.out == "" {
			t.Fatalf("sample %s degenerated (raw=%d out=%d)", m.name, len(m.raw), len(m.out))
		}
	}
}
