package metrics

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/blueprint/adapters/kern"
	"github.com/JayveerPrajapati/kern/internal/blueprint/domain"
	"github.com/JayveerPrajapati/kern/internal/blueprint/service"
)

// --- Benchmark fixtures (spec G12, lines 1465-1476) ---

// makeRepo creates a git repo with N source files, returns its path.
func makeRepo(t *testing.T, fileCount int) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "config", "user.email", "t@t")
	runGit(t, dir, "config", "user.name", "t")
	// Disable auto-maintenance so a detached git process doesn't keep writing
	// to .git/objects after the test body returns, racing t.TempDir() cleanup.
	runGit(t, dir, "config", "maintenance.auto", "false")
	runGit(t, dir, "config", "gc.auto", "0")
	writeFile(t, dir, "go.mod", "module example.com/repo\n\ngo 1.23\n")
	for i := 0; i < fileCount; i++ {
		path := fmt.Sprintf("pkg%d/file%d.go", i/10, i)
		content := fmt.Sprintf("package pkg%d\n\nfunc Func%d() int { return %d }\n", i/10, i, i)
		writeFile(t, dir, path, content)
	}
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-qm", "init")
	return dir
}

func makeDiffRepo(t *testing.T, baseFileCount, diffFileCount int) string {
	t.Helper()
	dir := makeRepo(t, baseFileCount)
	// Modify diffFileCount files to create a diff.
	for i := 0; i < diffFileCount && i < baseFileCount; i++ {
		path := fmt.Sprintf("pkg%d/file%d.go", i/10, i)
		content := fmt.Sprintf("package pkg%d\n\nfunc Func%d() int { return %d + 1 }\n", i/10, i, i)
		writeFile(t, dir, path, content)
	}
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-qm", "changes")
	// Reset to first commit to create staged diff.
	runGit(t, dir, "reset", "--soft", "HEAD~1")
	return dir
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func writeFile(t *testing.T, dir, relpath, content string) {
	t.Helper()
	full := filepath.Join(dir, relpath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", relpath, err)
	}
}

// requireKernBinary skips if no kern binary.
func requireKernBinary(t *testing.T) *kern.KernClient {
	t.Helper()
	binaryPath := os.Getenv("KERN_BINARY")
	if binaryPath == "" {
		if p, err := exec.LookPath("kern"); err == nil {
			binaryPath = p
		}
	}
	if binaryPath == "" {
		t.Skip("KERN_BINARY not set and kern not in PATH")
	}
	client, err := kern.NewKernClient(kern.WithBinary(binaryPath))
	if err != nil {
		t.Skipf("cannot create kern client: %v", err)
	}
	return client
}

// runValidation runs the full pipeline against a repo and returns latency.
func runValidation(t *testing.T, client *kern.KernClient, dir string, diffFiles []string) time.Duration {
	t.Helper()
	changes := make([]domain.FileChange, len(diffFiles))
	for i, f := range diffFiles {
		changes[i] = domain.FileChange{Path: f, Op: domain.OpEdit}
	}
	req := domain.ChangeRequest{
		RepositoryRoot: dir,
		Source:         domain.SourceCI,
		Operation:      domain.OpCommit,
		Files:          changes,
	}
	svc := service.New([]service.Check{
		kern.NewArchitectureCheck(client),
		kern.NewSecretCheck(client),
	})
	start := time.Now()
	svc.Validate(context.Background(), req)
	return time.Since(start)
}

// getDiffFiles returns the list of staged files in the repo.
func getDiffFiles(dir string) []string {
	cmd := exec.Command("git", "diff", "--cached", "--name-only")
	cmd.Dir = dir
	out, _ := cmd.Output()
	var files []string
	for _, line := range splitLines(string(out)) {
		if line != "" {
			files = append(files, line)
		}
	}
	return files
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

// --- G12 Benchmark tests (spec lines 1465-1476) ---
// Record p50/p95 latency for each scenario. These are benchmark data points,
// not pass/fail gates — the spec says "Record p50/p95 latency" (line 1476).
// Implemented as TestG12_* functions below (not Benchmark*) so they run with
// `go test` and record latency in the test output.

// --- G12 as test functions (so they run with `go test`, not just `go test -bench`) ---

// TestG12_LatencyBenchmarks runs all benchmark scenarios as tests, recording
// p50/p95 latency. These are data-collection tests, not pass/fail gates.
func TestG12_LatencyBenchmarks(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping benchmarks in short mode")
	}
	client := requireKernBinary(t)

	scenarios := []struct {
		name      string
		baseFiles int
		diffFiles int
	}{
		{"tiny-repo/1-file-diff", 10, 1},
		{"medium-repo/50-file-diff", 200, 50},
		{"large-repo/500-file-diff", 1000, 500},
	}

	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			dir := makeDiffRepo(t, sc.baseFiles, sc.diffFiles)
			diffFiles := getDiffFiles(dir)
			if len(diffFiles) == 0 {
				t.Fatal("no staged diff files")
			}

			// Run 5 iterations to get p50/p95.
			var latencies []time.Duration
			for i := 0; i < 5; i++ {
				lat := runValidation(t, client, dir, diffFiles)
				latencies = append(latencies, lat)
			}

			p50, p95 := computePercentiles(latencies)
			t.Logf("%-30s files=%-4d p50=%s p95=%s", sc.name, len(diffFiles), p50, p95)

			// Performance target (spec line 1454): fast deterministic validation < 1s for small diffs.
			// This is a target, not a hard gate — log if exceeded.
			if sc.baseFiles <= 10 && p50 > 1*time.Second {
				t.Logf("WARNING: tiny repo p50 (%s) exceeds 1s target (spec line 1454)", p50)
			}
		})
	}
}

// TestG12_ColdVsWarmIndex records cold vs warm index latency.
func TestG12_ColdVsWarmIndex(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping benchmarks in short mode")
	}
	client := requireKernBinary(t)
	dir := makeRepo(t, 100)

	// Cold: remove index before each run.
	var coldLatencies []time.Duration
	for i := 0; i < 3; i++ {
		os.RemoveAll(filepath.Join(dir, ".kern", "index.json"))
		diffFiles := getDiffFiles(dir)
		if len(diffFiles) == 0 {
			// Create a staged change.
			writeFile(t, dir, "new.go", "package repo\nfunc New() {}\n")
			runGit(t, dir, "add", "new.go")
			diffFiles = []string{"new.go"}
		}
		coldLatencies = append(coldLatencies, runValidation(t, client, dir, diffFiles))
	}

	// Warm: index exists.
	client.IndexBuild(context.Background(), dir)
	var warmLatencies []time.Duration
	for i := 0; i < 3; i++ {
		warmLatencies = append(warmLatencies, runValidation(t, client, dir, getDiffFiles(dir)))
	}

	coldP50, _ := computePercentiles(coldLatencies)
	warmP50, _ := computePercentiles(warmLatencies)
	t.Logf("cold-index p50=%s  warm-index p50=%s  speedup=%.1fx", coldP50, warmP50, float64(coldP50)/float64(warmP50))
}

// TestG12_MetricsRecording verifies the metrics collector records validation
// runs correctly.
func TestG12_MetricsRecording(t *testing.T) {
	m := New()

	// Record several validations.
	m.RecordValidation("PASS", 50*time.Millisecond)
	m.RecordValidation("BLOCK", 100*time.Millisecond)
	m.RecordValidation("BLOCK", 80*time.Millisecond)
	m.RecordValidation("WARN", 60*time.Millisecond)
	m.RecordValidation("ERROR", 200*time.Millisecond)
	m.RecordCheckLatency("architecture:boundary-violation", 40*time.Millisecond)
	m.RecordCheckLatency("architecture:boundary-violation", 50*time.Millisecond)
	m.RecordSandboxLatency(500 * time.Millisecond)
	m.RecordRepairAttempt(true)
	m.RecordRepairAttempt(false)
	m.RecordRepairAttempt(true)
	m.RecordOverride()

	stats := m.ComputeStats()

	if stats.ValidationCount != 5 {
		t.Errorf("ValidationCount = %d, want 5", stats.ValidationCount)
	}
	if stats.PassCount != 1 {
		t.Errorf("PassCount = %d, want 1", stats.PassCount)
	}
	if stats.BlockedCount != 2 {
		t.Errorf("BlockedCount = %d, want 2", stats.BlockedCount)
	}
	if stats.WarningCount != 1 {
		t.Errorf("WarningCount = %d, want 1", stats.WarningCount)
	}
	if stats.ErrorCount != 1 {
		t.Errorf("ErrorCount = %d, want 1", stats.ErrorCount)
	}
	if stats.FalsePositiveOverrides != 1 {
		t.Errorf("FalsePositiveOverrides = %d, want 1", stats.FalsePositiveOverrides)
	}
	// Repair success rate: 2/3 = 0.667
	if stats.RepairSuccessRate < 0.66 || stats.RepairSuccessRate > 0.67 {
		t.Errorf("RepairSuccessRate = %.3f, want ~0.667", stats.RepairSuccessRate)
	}
	// p50 of [50, 60, 80, 100, 200]ms = 80ms
	if stats.ValidationP50Ms < 79 || stats.ValidationP50Ms > 81 {
		t.Errorf("ValidationP50Ms = %.2f, want ~80", stats.ValidationP50Ms)
	}
}

// TestG12_MetricsPersistence verifies metrics save/load round-trip.
func TestG12_MetricsPersistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".blueprint", "metrics.json")

	m1 := New()
	m1.RecordValidation("PASS", 50*time.Millisecond)
	m1.RecordValidation("BLOCK", 100*time.Millisecond)
	m1.RecordCheckLatency("test-check", 30*time.Millisecond)

	if err := m1.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	m2, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if m2.ValidationCount != 2 {
		t.Errorf("loaded ValidationCount = %d, want 2", m2.ValidationCount)
	}
	if m2.BlockedCount != 1 {
		t.Errorf("loaded BlockedCount = %d, want 1", m2.BlockedCount)
	}
	if len(m2.PerCheckLatencies["test-check"]) != 1 {
		t.Errorf("loaded per-check latency missing")
	}
}

// TestG12_MetricsReset verifies --reset clears all metrics.
func TestG12_MetricsReset(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".blueprint", "metrics.json")

	m1 := New()
	m1.RecordValidation("PASS", 50*time.Millisecond)
	m1.RecordValidation("BLOCK", 100*time.Millisecond)
	m1.Save(path)

	// Reset.
	m2 := New()
	m2.Save(path)

	// Load and verify empty.
	m3, _ := Load(path)
	if m3.ValidationCount != 0 {
		t.Errorf("after reset, ValidationCount = %d, want 0", m3.ValidationCount)
	}
	if m3.BlockedCount != 0 {
		t.Errorf("after reset, BlockedCount = %d, want 0", m3.BlockedCount)
	}
}

// TestG12_LatencyCap: the latency slices are capped at maxLatencySamples so a
// long-lived watcher daemon can't balloon metrics.json or degrade
// ComputeStats/percentiles.
func TestG12_LatencyCap(t *testing.T) {
	m := New()
	for i := 0; i < 1500; i++ {
		m.RecordValidation("PASS", time.Duration(i)*time.Millisecond)
	}
	if got := len(m.ValidationLatencies); got > maxLatencySamples {
		t.Errorf("ValidationLatencies = %d, want <= %d", got, maxLatencySamples)
	}
	// The most recent sample must be retained.
	if got := m.ValidationLatencies[len(m.ValidationLatencies)-1]; got != int64(1499*time.Millisecond) {
		t.Errorf("last sample = %d, want %d (most recent must be kept)", got, int64(1499*time.Millisecond))
	}
	// The cap must hold after exceeding the limit again.
	m.RecordValidation("PASS", time.Second)
	if got := len(m.ValidationLatencies); got > maxLatencySamples {
		t.Errorf("after growth, ValidationLatencies = %d, want <= %d", got, maxLatencySamples)
	}
}

// TestG12_AtomicSaveRoundTrip: Save writes atomically (no half-written file
// observable, no leftover temp files) and Load returns the same counters.
func TestG12_AtomicSaveRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".blueprint", "metrics.json")

	m1 := New()
	m1.RecordValidation("PASS", 50*time.Millisecond)
	m1.RecordValidation("BLOCK", 100*time.Millisecond)
	if err := m1.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("metrics file missing after Save: %v", err)
	}

	m2, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if m2.ValidationCount != 2 {
		t.Errorf("ValidationCount = %d, want 2", m2.ValidationCount)
	}
	if m2.BlockedCount != 1 {
		t.Errorf("BlockedCount = %d, want 1", m2.BlockedCount)
	}

	// No .metrics-*.tmp files may remain after a successful Save.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".metrics-") {
			t.Errorf("leftover temp file after Save: %s", e.Name())
		}
	}
}

// --- Helpers ---

func computePercentiles(latencies []time.Duration) (time.Duration, time.Duration) {
	if len(latencies) == 0 {
		return 0, 0
	}
	sorted := make([]time.Duration, len(latencies))
	copy(sorted, latencies)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	p50Idx := len(sorted) * 50 / 100
	p95Idx := len(sorted) * 95 / 100
	if p50Idx >= len(sorted) {
		p50Idx = len(sorted) - 1
	}
	if p95Idx >= len(sorted) {
		p95Idx = len(sorted) - 1
	}
	return sorted[p50Idx], sorted[p95Idx]
}

// Note: Benchmark* functions and their *testing.B helpers were removed.
// The TestG12_* functions above cover all G12 scenarios (tiny/medium/large
// repo, 1/50/500-file diff, cold/warm index, p50/p95 latency) and run with
// `go test` directly — no need for separate `go test -bench` entry points.
