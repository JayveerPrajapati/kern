package index

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/lock"
)

// countReads swaps the package readFile seam with a counting wrapper. Both
// the staleness content walk (indexableHashesObserved) and Update's per-file
// change detection (updateComputeFile) read file contents through readFile,
// so the counter observes every content read while a stat-only walk reads
// nothing. The returned restore func must be deferred.
func countReads() (*atomic.Int64, func()) {
	orig := readFile
	var n atomic.Int64
	readFile = func(path string) ([]byte, error) {
		n.Add(1)
		return orig(path)
	}
	return &n, func() { readFile = orig }
}

// TestLoadOrBuildStaleHashesTreeOnce pins the M5a double-walk fix: on the
// stale path the freshness check runs a full content walk (identity
// finishFreshness), and the incremental Update that follows must REUSE that
// walk's per-file hashes instead of re-reading and re-hashing the tree. In
// this non-git fixture every staleness check content-walks, so the stale
// LoadOrBuild runs exactly two walks (the pre-lock probe and the under-lock
// re-check) and Update re-reads ONLY the files whose content changed since
// the previous index — every unchanged file is served from the retained
// walk. Before the fix Update re-read every file: a third pass over the
// whole tree.
func TestLoadOrBuildStaleHashesTreeOnce(t *testing.T) {
	root := fixtureTree(t)
	built, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := built.Save(); err != nil {
		t.Fatal(err)
	}
	// Make the persisted index stale: edit one file. The tree still has the
	// same file set, so all staleness walks hash len(FileHashes) files.
	writeFileAt(t, root, "a/one.go", "package a\n\nfunc One() int { return 1 }\n\nfunc OneExtra() {}\n")

	// Files whose content differs from the persisted index: exactly those
	// Update must re-read for extraction (hashes differ from prev), counted
	// before the read counter is armed.
	changed := 0
	cur, err := FileHashes(root)
	if err != nil {
		t.Fatal(err)
	}
	for f, h := range cur {
		if ph, ok := built.FileHashes[f]; !ok || ph != h {
			changed++
		}
	}
	if changed == 0 {
		t.Fatal("fixture must have at least one changed file")
	}

	reads, restore := countReads()
	defer restore()

	got, err := LoadOrBuild(root)
	if err != nil {
		t.Fatalf("LoadOrBuild: %v", err)
	}

	// Two staleness walks hash every file; Update re-reads only the changed
	// ones. Before the fix Update re-read ALL of them (3N+... total).
	want := 2*len(built.FileHashes) + changed
	if got := reads.Load(); got != int64(want) {
		t.Fatalf("source files read %d times, want %d (two staleness walks + only the %d changed file(s) re-read by Update)", got, want, changed)
	}

	// Correctness: the reused-hash update must equal a full rebuild.
	full, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	assertEquivalent(t, "stale LoadOrBuild with reused hashes", got, full)
}

// TestUpdateWithSnapshotRevalidatesMtimes pins the correctness guard on hash
// reuse: a file edited between the staleness walk and the update has a
// different mtime, so it must be re-read and re-extracted. The optimization
// only skips reads for files whose mtime proves them unchanged since the
// walk that produced their hash — it can never serve a stale contribution.
func TestUpdateWithSnapshotRevalidatesMtimes(t *testing.T) {
	root := fixtureTree(t)
	prior, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := prior.Save(); err != nil {
		t.Fatal(err)
	}
	prev, err := Load(root)
	if err != nil || prev == nil {
		t.Fatalf("Load: %v", err)
	}
	// The staleness check that puts the caller on the update path: it runs a
	// content walk and retains the observation (hashes + mtimes) on prev.
	// The fixture tree matches the index here, so the verdict is fresh, but
	// the retained observation is what the following update consumes.
	if prev.Stale() {
		t.Fatal("fresh fixture must not report stale")
	}
	if prev.staleSnapshot == nil {
		t.Fatal("staleness check must retain the content-walk observation")
	}

	// Edit a file AFTER the staleness walk: its mtime now differs from the
	// snapshot's, so UpdateWithSnapshot must re-read (and re-extract) it.
	writeFileAt(t, root, "a/one.go", "package a\n\nfunc One() int { return 1 }\n\nfunc OneExtra() {}\n")

	reads, restore := countReads()
	defer restore()

	updated, err := UpdateWithSnapshot(root, prev)
	if err != nil {
		t.Fatalf("UpdateWithSnapshot: %v", err)
	}

	// Exactly the edited file is re-read; the four mtime-unchanged files are
	// served from the retained walk.
	if got := reads.Load(); got != 1 {
		t.Fatalf("files re-read by UpdateWithSnapshot: %d, want exactly 1 (the edited file)", got)
	}

	full, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	assertEquivalent(t, "UpdateWithSnapshot after inter-walk edit", updated, full)
}

// TestDebouncedRebuildContextCancelled pins the buildlock ctx-awareness: a
// wedged lock holder must not stall a rebuild-needing call past its own
// deadline — cancelling the context mid-wait aborts promptly with
// context.Canceled instead of sleeping out the full BuildLockWait budget.
func TestDebouncedRebuildContextCancelled(t *testing.T) {
	restoreBuildLockTimings(t)
	root := t.TempDir()
	BuildLockWait = 60 * time.Second // cancellation must beat the budget
	buildLockPoll = 20 * time.Millisecond

	holder, err := lock.Acquire(root, buildLockScope)
	if err != nil {
		t.Fatalf("acquire holder lock: %v", err)
	}
	defer holder.Release()

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)

	start := time.Now()
	ix, err := debouncedRebuildContext(ctx, root,
		func() (*Index, bool) { return nil, false }, // never fresh while held
		func(prev *Index) (*Index, error) { return &Index{Root: root}, nil })
	elapsed := time.Since(start)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v (index %+v)", err, ix)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("cancellation took %s; the wait must abort promptly", elapsed)
	}
}

// TestDebouncedRebuildContextPreCancelled pins the already-cancelled entry:
// a caller that has already given up must not even attempt the lock or the
// rebuild.
func TestDebouncedRebuildContextPreCancelled(t *testing.T) {
	restoreBuildLockTimings(t)
	root := t.TempDir()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	ix, err := debouncedRebuildContext(ctx, root,
		func() (*Index, bool) { return &Index{Root: root}, true },
		func(prev *Index) (*Index, error) { return &Index{Root: root}, nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v (index %+v)", err, ix)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("pre-cancelled ctx took %s to reject", elapsed)
	}
}
