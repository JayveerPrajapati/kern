package sandbox

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// sandboxPrefix is the literal prefix every snapshot copy's temp dir name
// starts with: os.MkdirTemp("", "kern-sandbox-*") replaces the "*" with a
// random suffix, so the resulting names are "kern-sandbox-<random>".
const sandboxPrefix = "kern-sandbox-"

// blueprintSandboxPrefix is the temp-dir prefix of the blueprint worktree
// sandbox (os.MkdirTemp("", "blueprint-sandbox-") in
// internal/blueprint/sandbox/sandbox.go, with a registered git worktree at
// <dir>/work). A process dying mid-run leaks the dir exactly like a snapshot
// copy, so the sweep covers it too; a stale .git worktree registration is
// pruned lazily by git, so plain removal is safe.
const blueprintSandboxPrefix = "blueprint-sandbox-"

// isOrphanSandboxDir reports whether name carries one of the kern-owned
// sandbox temp-dir prefixes the sweep is allowed to remove (age-gated).
func isOrphanSandboxDir(name string) bool {
	return strings.HasPrefix(name, sandboxPrefix) || strings.HasPrefix(name, blueprintSandboxPrefix)
}

// defaultOrphanTTL is how old (by ModTime) an orphaned snapshot copy must be
// before the sweep removes it. A live snapshot is only ever written during a
// run; a copy untouched for 24h is a leak from a process that died before it
// could run Snap.Cleanup / Worktree.Cleanup (SIGKILL, crash).
const defaultOrphanTTL = 24 * time.Hour

// sweepOnce guarantees the orphan sweep runs at most once per process, on the
// first Snapshot() call — a single cheap ReadDir of the system temp root.
var sweepOnce sync.Once

// sweepOrphanSandboxes removes kern-owned sandbox temp dirs (kern-sandbox-*
// snapshot copies and blueprint-sandbox-* worktree sandboxes) under dir
// (normally os.TempDir()) whose ModTime is older than ttl. Entries still
// newer than ttl (a live snapshot in progress, or a copy owned by another
// running process) and names that do not match a kern-owned prefix are never
// touched. Removal errors are collected and the first is returned — callers
// treat the sweep as best-effort and must not fail on it.
func sweepOrphanSandboxes(dir string, ttl time.Duration) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	cutoff := time.Now().Add(-ttl)
	var firstErr error
	for _, e := range entries {
		if !isOrphanSandboxDir(e.Name()) {
			continue
		}
		info, ierr := e.Info()
		if ierr != nil {
			// Cannot age-check this entry: leave it alone rather than risk
			// deleting a live copy.
			continue
		}
		if info.ModTime().After(cutoff) {
			continue
		}
		if rerr := os.RemoveAll(filepath.Join(dir, e.Name())); rerr != nil && firstErr == nil {
			firstErr = rerr
		}
	}
	return firstErr
}

// orphanSweepTTL returns the effective orphan TTL: the
// KERN_SANDBOX_ORPHAN_TTL override when set to a valid positive Go duration,
// else the 24h default. An invalid or non-positive override logs a warning
// and falls back to the default rather than aborting or sweeping too eagerly.
func orphanSweepTTL() time.Duration {
	if v := os.Getenv("KERN_SANDBOX_ORPHAN_TTL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
		log.Printf("WARNING: invalid KERN_SANDBOX_ORPHAN_TTL %q; using default orphan TTL of %s", v, defaultOrphanTTL)
	}
	return defaultOrphanTTL
}

// orphanSweepDisabled reports whether KERN_SANDBOX_SWEEP_DISABLE=1 turns the
// orphan sweep off entirely.
func orphanSweepDisabled() bool {
	return os.Getenv("KERN_SANDBOX_SWEEP_DISABLE") == "1"
}

// sweepOrphanSandboxesOnce runs the best-effort orphan sweep over the system
// temp dir, honoring the KERN_SANDBOX_ORPHAN_TTL / KERN_SANDBOX_SWEEP_DISABLE
// env overrides. It never returns an error: a sweep failure is logged and
// ignored so Snapshot is never broken by the sweep.
func sweepOrphanSandboxesOnce() {
	sweepOrphanSandboxesOnceIn(os.TempDir())
}

// sweepOrphanSandboxesOnceIn is sweepOrphanSandboxesOnce against an explicit
// dir (the system temp dir in production; a t.TempDir in tests).
func sweepOrphanSandboxesOnceIn(dir string) {
	if orphanSweepDisabled() {
		return
	}
	if err := sweepOrphanSandboxes(dir, orphanSweepTTL()); err != nil {
		log.Printf("sandbox: orphan sweep of %s failed (best-effort, ignored): %v", dir, err)
	}
}
