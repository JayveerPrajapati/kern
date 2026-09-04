// Package main is the Blueprint CLI entry point.
package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gMetricsRun runs `blueprint metrics` with the given extra args and returns
// (stdout, stderr, exit code). Stdout/stderr are captured separately so the
// tests can assert where the report goes.
func gMetricsRun(t *testing.T, binPath string, extraArgs ...string) (string, string, int) {
	t.Helper()
	args := append([]string{"metrics"}, extraArgs...)
	cmd := exec.Command(binPath, args...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		} else {
			t.Fatalf("run blueprint metrics: %v", err)
		}
	}
	return stdout.String(), stderr.String(), code
}

// metricsStats is the subset of the metrics JSON report the tests assert on.
type metricsStats struct {
	ValidationCount int `json:"validation_count"`
	PassCount       int `json:"pass_count"`
	BlockedCount    int `json:"blocked_count"`
	WarningCount    int `json:"warning_count"`
	ErrorCount      int `json:"error_count"`
}

// TestMetrics_JSON verifies `blueprint metrics --repo <dir> --json` emits a
// valid JSON document with the expected metric fields on stdout.
func TestMetrics_JSON(t *testing.T) {
	bin := g4BuildBinary(t)
	dir := t.TempDir()

	out, stderr, code := gMetricsRun(t, bin, "--repo", dir, "--json")
	if code != 0 {
		t.Fatalf("exit=%d want 0; stderr:\n%s", code, stderr)
	}
	var stats metricsStats
	if err := json.Unmarshal([]byte(out), &stats); err != nil {
		t.Fatalf("metrics --json output is not valid JSON: %v\n%s", err, out)
	}
	// A fresh repo has no recorded validations; the fields must still exist
	// and be zero-valued.
	if stats.ValidationCount != 0 {
		t.Errorf("validation_count = %d, want 0 (fresh repo)", stats.ValidationCount)
	}
	if stats.PassCount != 0 || stats.BlockedCount != 0 || stats.WarningCount != 0 || stats.ErrorCount != 0 {
		t.Errorf("unexpected non-zero counters: %+v", stats)
	}
}

// TestMetrics_Terminal verifies `blueprint metrics --repo <dir>` emits a
// human-readable report on stdout and exits 0.
func TestMetrics_Terminal(t *testing.T) {
	bin := g4BuildBinary(t)
	dir := t.TempDir()

	out, stderr, code := gMetricsRun(t, bin, "--repo", dir)
	if code != 0 {
		t.Fatalf("exit=%d want 0; stderr:\n%s", code, stderr)
	}
	if strings.TrimSpace(out) == "" {
		t.Fatal("metrics terminal output is empty; want a human-readable report on stdout")
	}
	for _, want := range []string{"Validations", "Latency p50"} {
		if !strings.Contains(out, want) {
			t.Errorf("terminal report missing %q:\n%s", want, out)
		}
	}
}

// TestMetrics_NoRepo verifies a non-repo path (one where .blueprint cannot be
// a directory) is an operational error: exit 2.
func TestMetrics_NoRepo(t *testing.T) {
	bin := g4BuildBinary(t)
	// A regular FILE as the repo root: .blueprint under it cannot be a
	// directory, so the metrics file cannot be read → exit 2.
	file := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, stderr, code := gMetricsRun(t, bin, "--repo", file)
	if code != 2 {
		t.Fatalf("exit=%d want 2 (operational error); stderr:\n%s", code, stderr)
	}
	if !strings.Contains(stderr, "metrics") {
		t.Errorf("stderr missing metrics error context:\n%s", stderr)
	}
}

// TestMetrics_Reset verifies `blueprint metrics --repo <dir> --reset` clears
// accumulated metrics: it writes a fresh zeroed metrics file and exits 0.
func TestMetrics_Reset(t *testing.T) {
	bin := g4BuildBinary(t)
	dir := t.TempDir()

	// Seed a non-zero metrics file first so "reset" actually clears data.
	path := filepath.Join(dir, ".blueprint", "metrics.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	seeded := `{"validation_count": 42, "pass_count": 40, "blocked_count": 2}`
	if err := os.WriteFile(path, []byte(seeded), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, stderr, code := gMetricsRun(t, bin, "--repo", dir, "--reset"); code != 0 {
		t.Fatalf("metrics --reset exit=%d want 0; stderr:\n%s", code, stderr)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read metrics file after reset: %v", err)
	}
	var stats metricsStats
	if err := json.Unmarshal(data, &stats); err != nil {
		t.Fatalf("metrics file after reset is not valid JSON: %v\n%s", err, data)
	}
	if stats.ValidationCount != 0 || stats.PassCount != 0 || stats.BlockedCount != 0 {
		t.Fatalf("metrics not cleared by --reset: %+v", stats)
	}
}
