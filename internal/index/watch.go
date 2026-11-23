package index

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/JayveerPrajapati/kern/internal/ignore"
	"github.com/JayveerPrajapati/kern/internal/indexwatch"
)

// The hashing/content-walk family (IndexableHashes, WalkQuickExt, the Change
// set, AdaptiveInterval) lives in internal/indexwatch; this file keeps the
// pieces that are bound to the Index type (Watch's rebuild loop) or to the
// tunables machinery (proofWalkMaxBytes), plus compatibility re-exports so
// the package's public surface (index.Watch, index.FileHashes, index.Diff,
// index.Change, index.HasIndexableSources, index.CatchUpMaxChanges) is
// unchanged by the extraction.

// Change is one file change detected by the watcher.
type Change = indexwatch.Change

// ChangeKind describes a file change detected by the watcher.
type ChangeKind = indexwatch.ChangeKind

const (
	ChangeAdded       = indexwatch.ChangeAdded
	ChangeModified    = indexwatch.ChangeModified
	ChangeRemoved     = indexwatch.ChangeRemoved
	CatchUpMaxChanges = indexwatch.CatchUpMaxChanges
)

// readFile is a var so tests can swap in a counting wrapper to prove how many
// times file contents are read (staleness walks and update change-detection
// both read through it; stat-only walks do not). The hashing walks that moved
// to internal/indexwatch read through that package's own ReadFile seam —
// index-side tests that count reads must swap BOTH seams (see countReads in
// stalehash_test.go).
var readFile = func(path string) ([]byte, error) {
	return os.ReadFile(path)
}

// errSnapshotMismatch signals snapshotStillCurrent's walk that a recorded
// file's stat no longer matches; any non-nil error means "gate failed".
var errSnapshotMismatch = errors.New("snapshot stat mismatch")

// snapshotStillCurrent re-validates a retained observation with a STAT-ONLY
// walk — no file content is read. Every file the snapshot recorded must
// still exist with an identical mtime AND size, the snapshot's files must
// all be covered, and the total walked count must match the count observed
// by the walk that produced the snapshot (a new file forces the full
// walk). This is the hot-path guard for repeated staleness checks on a
// dirty tree (where the git tree-OID fast path cannot vouch): the first
// check pays the full read+hash walk, every subsequent check pays only
// stats as long as nothing changed. Trust model: mtime+size (never mtime
// alone — a size-changing mtime-preserving edit, e.g. git apply, is caught
// by the size leg; a same-size mtime-preserving edit evades the gate only
// until gateTTL expires, which FreshnessProofStrict and the clean-tree git
// OID path keep catching regardless).
func snapshotStillCurrent(root string, ign *ignore.Matcher, snap *FreshnessSnapshot) bool {
	if snap == nil || len(snap.Mtimes) == 0 {
		return false
	}
	walked, seen := 0, 0
	err := indexwatch.WalkQuickExt(root, ign, proofWalkMaxBytes(), func(rel, path string, info fs.FileInfo) error {
		walked++
		wantM, ok := snap.Mtimes[rel]
		if !ok {
			return nil
		}
		seen++
		if wantM != info.ModTime().UnixNano() || snap.Sizes[rel] != info.Size() {
			return errSnapshotMismatch
		}
		return nil
	})
	return err == nil && seen == len(snap.Mtimes) && walked == snap.WalkCount
}

// proofWalkMaxBytes returns the adaptive per-file size cap the freshness
// walks apply — the same resolveTunables value the build walks use — so the
// proof and build walks select the same file set. An explicit
// WithMaxFileBytes build is a rare operator-option case the persisted index
// does not record; the adaptive default is the deterministic per-machine
// baseline both sides share.
func proofWalkMaxBytes() int64 {
	return resolveTunables(&buildConfig{}, resolveResources()).maxFileBytes
}

// Watch polls root every interval and rebuilds + saves the index whenever the
// set of Go files or their content changes. onChange is called with the
// detected changes and the fresh index. onError receives every non-fatal
// failure (scan, build, or save) so a long-running watcher can surface
// problems instead of silently dropping them.
//
// The poll interval is adaptive (see indexwatch.AdaptiveInterval): it stays
// at the base interval after no or small change sets and backs off — up to
// adaptMaxScale × base — after large change sets, decaying back toward base
// as the tree settles.
func Watch(ctx context.Context, root string, interval time.Duration, onChange func(changes []Change, ix *Index), onError func(err error)) error {
	abs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	root = abs
	prev := map[string]string{}
	if ix, err := Load(root); err == nil && ix != nil {
		prev = ix.FileHashes
	}
	// wait tracks the interval the previous poll cycle used, starting at the
	// base interval and recomputed per cycle by AdaptiveInterval so the
	// polling cadence reacts to the change history.
	wait := interval
	for {
		cur, err := indexwatch.IndexableHashes(root, ignore.Load(root), proofWalkMaxBytes())
		if err != nil {
			if onError != nil {
				onError(err)
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(wait):
			}
			continue
		}
		changes := indexwatch.Diff(prev, cur)
		if len(changes) > 0 {
			ix, err := Build(root)
			if err != nil {
				if onError != nil {
					onError(fmt.Errorf("rebuild after %d change(s): %w", len(changes), err))
				}
			} else if err := ix.Save(); err != nil {
				if onError != nil {
					onError(fmt.Errorf("save after %d change(s): %w", len(changes), err))
				}
			} else {
				if onChange != nil {
					onChange(changes, ix)
				}
				prev = cur
			}
		}
		wait = indexwatch.AdaptiveInterval(interval, len(changes), wait)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
}

// FileHashes returns a map of relative file path to content hash for every
// indexable source file under root. Exported for watcher implementations that
// need to compute change sets without rebuilding the whole index. A scan error
// is returned rather than a silent partial result.
func FileHashes(root string) (map[string]string, error) {
	return indexwatch.FileHashes(root, proofWalkMaxBytes())
}

// Diff reports the change set (adds, modifies, removes) between two hash maps
// as produced by FileHashes. Exported for watcher implementations.
func Diff(prev, cur map[string]string) []Change {
	return indexwatch.Diff(prev, cur)
}

// HasIndexableSources reports whether the tree under root contains at least
// one indexable source file (cheap first-match walk). Delegates to the
// extracted walk family.
func HasIndexableSources(root string) bool {
	return indexwatch.HasIndexableSources(root)
}
