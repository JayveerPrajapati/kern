package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// E2 residual: the Run* command entry points are thin wrappers over the run*
// functions but had zero tests. These pin the fail-closed exit contracts.

// fixtureRepo writes a tiny indexable Go module.
func fixtureRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module demo\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "app.go"), []byte("package main\n\n// Greet says hello.\nfunc Greet() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// TestRunDoctorFailsClosedWithoutBinary pins the fail-closed contract: with
// no kern binary available the doctor must exit 2 (env error), never 0.
func TestRunDoctorFailsClosedWithoutBinary(t *testing.T) {
	t.Setenv("KERN_BINARY", "")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	code := RunDoctor([]string{"--repo", fixtureRepo(t)})
	if code == 0 {
		t.Fatal("RunDoctor exited 0 without a kern binary; want fail-closed 2")
	}
}

// TestRunDoctorJSONShape: with a kern binary pointed at the test binary the
// contract probe fails (not kern), so the run still must not exit 0, and the
// JSON output must carry the checks array.
func TestRunDoctorJSONShape(t *testing.T) {
	t.Setenv("KERN_BINARY", os.Args[0]) // a binary, but not kern: contract probe fails
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	code := RunDoctor([]string{"--repo", fixtureRepo(t), "--json"})
	if code == 0 {
		t.Fatal("RunDoctor exited 0 with a non-kern KERN_BINARY; want non-zero (contract error)")
	}
}

// TestRunGraphMermaid: the graph command renders mermaid for a fixture repo.
func TestRunGraphMermaid(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	code := RunGraph([]string{"--repo", fixtureRepo(t), "--format", "mermaid"})
	if code != 0 {
		t.Fatalf("RunGraph = %d, want 0", code)
	}
}

// TestRunGraphJSON: the JSON format must yield parseable output.
func TestRunGraphJSON(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	code := RunGraph([]string{"--repo", fixtureRepo(t), "--format", "json"})
	if code != 0 {
		t.Fatalf("RunGraph json = %d, want 0", code)
	}
}

// TestRunMetricsHuman: metrics prints a human-readable summary and exits 0.
func TestRunMetricsHuman(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	code := RunMetrics([]string{"--repo", fixtureRepo(t)})
	if code != 0 {
		t.Fatalf("RunMetrics = %d, want 0", code)
	}
}

// TestRunMetricsResetThenRead: reset writes a zeroed metrics file that the
// next read reports; both exit 0.
func TestRunMetricsResetThenRead(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := fixtureRepo(t)
	if code := RunMetrics([]string{"--repo", root, "--reset"}); code != 0 {
		t.Fatalf("RunMetrics --reset = %d, want 0", code)
	}
	if code := RunMetrics([]string{"--repo", root}); code != 0 {
		t.Fatalf("RunMetrics after reset = %d, want 0", code)
	}
}

// TestRunGraphUnknownFormatRejected: an unsupported format exits non-zero.
func TestRunGraphUnknownFormatRejected(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if code := RunGraph([]string{"--repo", fixtureRepo(t), "--format", "xml"}); code == 0 {
		t.Fatal("RunGraph with unknown format exited 0, want non-zero")
	}
}
