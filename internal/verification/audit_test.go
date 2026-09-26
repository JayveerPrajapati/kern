package verification

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestCaptureOutputPassClipAndLog pins F4 on the capture side: a PASSing
// check's embedded output is clipped to the 32KB tail, while the FULL output
// is persisted to .kern/audit/<run-id>/verify-<check>.log and the path is
// carried back for the result's LogPath field.
func TestCaptureOutputPassClipAndLog(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	full := strings.Repeat("noisy line of output\n", 3000) // ~60KB
	clipped, rel := captureOutput(dir, now, "test", full, true)

	if len([]rune(clipped)) > passOutputCap {
		t.Errorf("clipped PASS output = %d runes, want <= %d", len([]rune(clipped)), passOutputCap)
	}
	if !strings.HasSuffix(clipped, "noisy line of output\n") {
		t.Error("clipped PASS output must keep the TAIL (the actionable end)")
	}
	if strings.HasPrefix(clipped, "noisy") {
		t.Error("clipped PASS output should be truncated from the head")
	}
	if rel == "" {
		t.Fatal("LogPath empty; want a .kern/audit/<run-id>/verify-test.log path")
	}
	if !strings.HasPrefix(rel, filepath.Join(".kern", "audit")) || !strings.HasSuffix(rel, "verify-test.log") {
		t.Errorf("LogPath = %q, want .kern/audit/<run-id>/verify-test.log", rel)
	}
	data, err := os.ReadFile(filepath.Join(dir, rel))
	if err != nil {
		t.Fatalf("log file not written at %s: %v", rel, err)
	}
	if string(data) != full {
		t.Errorf("log file must hold the FULL output (%d bytes), got %d bytes", len(full), len(data))
	}
}

// TestCaptureOutputFailClip pins the FAIL cap: a FAILing check keeps a larger
// (256KB) tail, and the full log is still persisted.
func TestCaptureOutputFailClip(t *testing.T) {
	dir := t.TempDir()
	full := strings.Repeat("x", 400<<10) // 400KB
	clipped, rel := captureOutput(dir, time.Now(), "build", full, false)

	if len([]rune(clipped)) > failOutputCap {
		t.Errorf("clipped FAIL output = %d runes, want <= %d", len([]rune(clipped)), failOutputCap)
	}
	if !strings.HasSuffix(clipped, strings.Repeat("x", 256<<10)) {
		t.Error("clipped FAIL output must keep the tail")
	}
	if rel == "" {
		t.Fatal("LogPath empty for a FAILing check")
	}
	data, err := os.ReadFile(filepath.Join(dir, rel))
	if err != nil {
		t.Fatalf("log file not written: %v", err)
	}
	if len(data) != 400<<10 {
		t.Errorf("log file must hold the FULL 400KB output, got %d bytes", len(data))
	}
}

// TestCaptureOutputEmptyPinsNoLog: empty output writes no log and yields an
// empty LogPath — nil-safe for existing callers.
func TestCaptureOutputEmpty(t *testing.T) {
	dir := t.TempDir()
	clipped, rel := captureOutput(dir, time.Now(), "build", "   \n\t ", true)
	if clipped != "" || rel != "" {
		t.Errorf("empty output: clipped=%q rel=%q, want both empty", clipped, rel)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("empty output must not create any files, got %v", entries)
	}
}

// TestCaptureOutputBadRootDegrades: an unwritable root must degrade to the
// clipped output with no LogPath — never an error.
func TestCaptureOutputBadRootDegrades(t *testing.T) {
	clipped, rel := captureOutput("/nonexistent/root-xyz", time.Now(), "build", "some output", false)
	if !strings.Contains(clipped, "some output") {
		t.Errorf("clipped = %q, want the output retained", clipped)
	}
	if rel != "" {
		t.Errorf("rel = %q, want empty when the write fails", rel)
	}
}

// TestVerifyTestsWritesAuditLog pins F4 end-to-end through the engine: a
// VerifyTests run persists the full output log and carries its path on
// TestResult.LogPath, while Output stays the (small) verbatim text. `go
// test -v` always emits output (unlike `go build`, which is silent on
// success), so this exercises the capture path with real output.
func TestVerifyTestsWritesAuditLog(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real-execution verification in -short mode")
	}
	t.Parallel()
	root := verifyFixture(t)
	res := NewEngine(root).VerifyTests()
	if res == nil {
		t.Fatal("nil test result")
	}
	if !res.OK {
		t.Fatalf("fixture tests should pass: %s", trunc(res.Output))
	}
	if res.Output == "" {
		t.Fatal("embedded Output must remain populated for go test -v")
	}
	if res.LogPath == "" {
		t.Fatal("LogPath empty; want .kern/audit/<run-id>/verify-test.log")
	}
	if !strings.HasSuffix(res.LogPath, "verify-test.log") {
		t.Errorf("LogPath = %q, want .../verify-test.log", res.LogPath)
	}
	data, err := os.ReadFile(filepath.Join(root, res.LogPath))
	if err != nil {
		t.Fatalf("audit log not present at %s: %v", res.LogPath, err)
	}
	if string(data) != res.Output {
		t.Errorf("log must hold the full output (%d bytes), embedded Output has %d bytes", len(data), len(res.Output))
	}
}

// TestVerifyBuildSilentNoLog pins the empty-output rule: a silent successful
// build (go build prints nothing) writes no log and leaves LogPath empty.
func TestVerifyBuildSilentNoLog(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real-execution verification in -short mode")
	}
	t.Parallel()
	root := verifyFixture(t)
	res := NewEngine(root).VerifyBuild()
	if res == nil {
		t.Fatal("nil build result")
	}
	if !res.OK {
		t.Fatalf("fixture build should pass: %s", trunc(res.Output))
	}
	if strings.TrimSpace(res.Output) != "" {
		t.Skipf("build produced unexpected output (%d bytes); empty-output rule not exercised", len(res.Output))
	}
	if res.LogPath != "" {
		t.Errorf("LogPath = %q, want empty for a silent build", res.LogPath)
	}
}
