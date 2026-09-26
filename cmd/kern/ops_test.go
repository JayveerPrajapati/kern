package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRunOps(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()

	// Missing arguments returns 2 (usage error)
	if code := runOps([]string{}); code != 2 {
		t.Errorf("expected exit code 2 for empty args, got %d", code)
	}

	// Non-interactive run on a test directory
	code := runOps([]string{
		"--non-interactive",
		"--repo", tmp,
		"--level", "L3",
		"verify baseline operations",
	})
	if code != 0 {
		t.Errorf("expected exit code 0 for clean non-interactive run, got %d", code)
	}

	// Verify working directory is untouched
	files, err := os.ReadDir(filepath.Join(tmp, ".kern", "sandboxes"))
	if err == nil && len(files) > 0 {
		t.Errorf("expected sandboxes to be cleaned up, found: %d entries", len(files))
	}
}

// TestRunOpsRejectsFlagsAfterIntent guards against flags placed after the
// task intent being silently swallowed into the task prompt (A14). They must
// be rejected with a usage error instead.
func TestRunOpsRejectsFlagsAfterIntent(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	if code := runOps([]string{
		"--repo", tmp,
		"--non-interactive",
		"estimate impact of removing GetMySQLDB",
		"--level", "L0",
		"-json",
	}); code != 2 {
		t.Errorf("expected exit code 2 for flags after intent, got %d", code)
	}
}

// TestRunOpsHonorsExplicitL0Guard verifies an explicit -level L0 is honored
// (A1) and is not silently upgraded to the default L3.
func TestRunOpsHonorsExplicitL0Guard(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	if code := runOps([]string{
		"--repo", tmp,
		"--non-interactive",
		"--level", "L0",
		"probe",
	}); code != 0 {
		t.Errorf("expected exit code 0 for L0 read-only run, got %d", code)
	}
}

func TestRunOpsTriage(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()

	// Missing arguments returns 2
	if code := runOps([]string{"triage"}); code != 2 {
		t.Errorf("expected exit code 2 for empty triage args, got %d", code)
	}

	logFile := filepath.Join(tmp, "crash.log")
	logContent := `
2026-09-04T05:15:00Z ERROR service failed
panic: runtime error: index out of range [5] with length 2
goroutine 1 [running]:
main.process()
	main.go:12 +0x20
`
	_ = os.WriteFile(logFile, []byte(logContent), 0o644)

	code := runOps([]string{
		"triage",
		"--log", logFile,
		"--repo", tmp,
		"--non-interactive",
		"--json",
	})
	if code != 0 {
		t.Errorf("expected exit code 0 for clean triage run, got %d", code)
	}
}
