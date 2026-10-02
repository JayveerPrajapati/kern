package sandbox

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// makeFakeSandbox creates a directory named kern-sandbox-<name> under root
// (with a subdir so removal exercises the recursive path) and stamps it with
// the given mtime, simulating an orphaned snapshot copy.
func makeFakeSandbox(t *testing.T, root, name string, mtime time.Time) string {
	t.Helper()
	dir := filepath.Join(root, sandboxPrefix+name)
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.Chtimes(dir, mtime, mtime); err != nil {
		t.Fatalf("chtimes %s: %v", dir, err)
	}
	return dir
}

func TestSweepOrphanSandboxes(t *testing.T) {
	root := t.TempDir()
	old := makeFakeSandbox(t, root, "old", time.Now().Add(-48*time.Hour))
	fresh := makeFakeSandbox(t, root, "fresh", time.Now())
	unrelated := filepath.Join(root, "unrelated-dir")
	if err := os.MkdirAll(unrelated, 0o755); err != nil {
		t.Fatal(err)
	}
	// The non-matching name is old too: it must still be untouched.
	if err := os.Chtimes(unrelated, time.Now().Add(-48*time.Hour), time.Now().Add(-48*time.Hour)); err != nil {
		t.Fatal(err)
	}

	if err := sweepOrphanSandboxes(root, defaultOrphanTTL); err != nil {
		t.Fatalf("sweepOrphanSandboxes: %v", err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("old sandbox dir %s should have been swept, stat err=%v", old, err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("fresh sandbox dir %s should be kept, stat err=%v", fresh, err)
	}
	if _, err := os.Stat(unrelated); err != nil {
		t.Errorf("non-matching dir %s must be untouched, stat err=%v", unrelated, err)
	}
}

func TestSweepOrphanSandboxesTTLHonored(t *testing.T) {
	root := t.TempDir()
	// 2h old: removed by a 1h TTL, kept by the 24h default.
	mid := makeFakeSandbox(t, root, "mid", time.Now().Add(-2*time.Hour))
	if err := sweepOrphanSandboxes(root, time.Hour); err != nil {
		t.Fatalf("sweep with 1h ttl: %v", err)
	}
	if _, err := os.Stat(mid); !os.IsNotExist(err) {
		t.Errorf("2h-old dir should be swept with a 1h TTL, stat err=%v", err)
	}
	mid2 := makeFakeSandbox(t, root, "mid2", time.Now().Add(-2*time.Hour))
	if err := sweepOrphanSandboxes(root, defaultOrphanTTL); err != nil {
		t.Fatalf("sweep with default ttl: %v", err)
	}
	if _, err := os.Stat(mid2); err != nil {
		t.Errorf("2h-old dir must be kept with the 24h default TTL, stat err=%v", err)
	}
}

func TestSweepOrphanTTLEnvOverride(t *testing.T) {
	root := t.TempDir()
	// 2h old: older than a 1h env TTL -> swept.
	old := makeFakeSandbox(t, root, "old", time.Now().Add(-2*time.Hour))
	// 30min old: younger than the env TTL -> kept.
	fresh := makeFakeSandbox(t, root, "fresh", time.Now().Add(-30*time.Minute))

	t.Setenv("KERN_SANDBOX_ORPHAN_TTL", "1h")
	sweepOrphanSandboxesOnceIn(root)

	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("1h env TTL should sweep the 2h-old dir, stat err=%v", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("1h env TTL should keep the 30min-old dir, stat err=%v", err)
	}
}

func TestSweepOrphanDisabledEnv(t *testing.T) {
	root := t.TempDir()
	old := makeFakeSandbox(t, root, "old", time.Now().Add(-48*time.Hour))

	t.Setenv("KERN_SANDBOX_SWEEP_DISABLE", "1")
	t.Setenv("KERN_SANDBOX_ORPHAN_TTL", "1ns")
	sweepOrphanSandboxesOnceIn(root)

	if _, err := os.Stat(old); err != nil {
		t.Errorf("sweep disabled: the 48h-old dir must be left alone, stat err=%v", err)
	}
}

func TestSweepOrphanTTLInvalidFallsBack(t *testing.T) {
	t.Setenv("KERN_SANDBOX_ORPHAN_TTL", "not-a-duration")
	if got := orphanSweepTTL(); got != defaultOrphanTTL {
		t.Errorf("invalid env TTL should fall back to the default, got %s", got)
	}
	t.Setenv("KERN_SANDBOX_ORPHAN_TTL", "-1h")
	if got := orphanSweepTTL(); got != defaultOrphanTTL {
		t.Errorf("non-positive env TTL should fall back to the default, got %s", got)
	}
	t.Setenv("KERN_SANDBOX_ORPHAN_TTL", "1h")
	if got := orphanSweepTTL(); got != time.Hour {
		t.Errorf("valid env TTL should be honored, got %s", got)
	}
	os.Unsetenv("KERN_SANDBOX_ORPHAN_TTL")
	if got := orphanSweepTTL(); got != defaultOrphanTTL {
		t.Errorf("unset env TTL should use the default, got %s", got)
	}
}

// TestSweepBlueprintSandboxOrphans pins the blueprint-sandbox-* coverage: the
// blueprint worktree sandbox (internal/blueprint/sandbox/sandbox.go) leaks its
// temp dir on a mid-run death exactly like a snapshot copy, so the sweep must
// remove aged blueprint-sandbox-<id> dirs (including the work/ git-worktree
// shape) while keeping fresh ones.
func TestSweepBlueprintSandboxOrphans(t *testing.T) {
	root := t.TempDir()
	old := filepath.Join(root, "blueprint-sandbox-123")
	if err := os.MkdirAll(filepath.Join(old, "work", "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}
	fresh := filepath.Join(root, "blueprint-sandbox-456")
	if err := os.MkdirAll(fresh, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := sweepOrphanSandboxes(root, defaultOrphanTTL); err != nil {
		t.Fatalf("sweepOrphanSandboxes: %v", err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("old blueprint-sandbox dir should have been swept, stat err=%v", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("fresh blueprint-sandbox dir should be kept, stat err=%v", err)
	}
}
