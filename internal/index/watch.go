package index

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/JayveerPrajapati/kern/internal/cache"
	"github.com/JayveerPrajapati/kern/internal/ignore"
)

func readFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

// indexableMaxMtime walks root without reading file contents and returns the
// newest modification time (Unix nanos) among indexable files plus their count.
// For every extension quickExt admits, detectLang returns a non-empty language,
// so a stat-only walk is equivalent to the content-checking indexableHashes.
// A walk error is returned so callers can decide to rebuild instead of serving
// a stale index. The ignore matcher mirrors Build's file-selection policy so
// gitignored files never influence the staleness decision.
func indexableMaxMtime(root string, ign *ignore.Matcher) (int64, int, error) {
	var maxMtime int64
	var count int
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && ignoreDirs[d.Name()] {
				return filepath.SkipDir
			}
			// Honor .gitignore/.kernignore directory patterns, as Build does.
			if path != root && ign != nil {
				if rel, rerr := filepath.Rel(root, path); rerr == nil && ign.Ignored(filepath.ToSlash(rel)) {
					return filepath.SkipDir
				}
			}
			return nil
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil || !quickExt(rel) {
			return nil
		}
		// Honor .gitignore/.kernignore file patterns, as Build does.
		if ign != nil && ign.Ignored(filepath.ToSlash(rel)) {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return ierr
		}
		count++
		if mt := info.ModTime().UnixNano(); mt > maxMtime {
			maxMtime = mt
		}
		return nil
	})
	if err != nil {
		return 0, 0, err
	}
	return maxMtime, count, nil
}

// HasIndexableSources reports whether the tree under root contains at least one
// indexable source file, walking only until the first match. Used by health
// checks that must not pay for a full build (e.g. doctor).
func HasIndexableSources(root string) bool {
	found := false
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || found {
			return nil
		}
		if d.IsDir() {
			if path != root && ignoreDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil || !quickExt(rel) {
			return nil
		}
		data, derr := readFile(path)
		if derr != nil {
			return nil
		}
		if isIndexable(rel, data) {
			found = true
		}
		return nil
	})
	return found
}

// indexableHashes walks root and returns a map of relative file path to
// content hash for every indexable source file. Used by the watcher to detect
// changes and by Load/Stale to decide whether a cached index is out of date.
// A walk or read error is returned rather than silently producing a partial
// map, which could wrongly mark an edited tree as unchanged. The ignore
// matcher mirrors Build's file-selection policy so gitignored files never
// appear in the manifest.
func indexableHashes(root string, ign *ignore.Matcher) (map[string]string, error) {
	cur := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && ignoreDirs[d.Name()] {
				return filepath.SkipDir
			}
			// Honor .gitignore/.kernignore directory patterns, as Build does.
			if path != root && ign != nil {
				if rel, rerr := filepath.Rel(root, path); rerr == nil && ign.Ignored(filepath.ToSlash(rel)) {
					return filepath.SkipDir
				}
			}
			return nil
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil || !quickExt(rel) {
			return nil
		}
		// Honor .gitignore/.kernignore file patterns, as Build does.
		if ign != nil && ign.Ignored(filepath.ToSlash(rel)) {
			return nil
		}
		data, derr := readFile(path)
		if derr != nil {
			return derr
		}
		if !isIndexable(rel, data) {
			return nil
		}
		cur[rel] = cache.Hash(data)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return cur, nil
}

// ChangeKind describes a file change detected by the watcher.
type ChangeKind string

const (
	ChangeAdded    ChangeKind = "added"
	ChangeModified ChangeKind = "modified"
	ChangeRemoved  ChangeKind = "removed"
)

// Change is one file change detected by the watcher.
type Change struct {
	Kind ChangeKind `json:"kind"`
	File string     `json:"file"`
}

// Adaptive poll-interval thresholds for Watch. Small change sets (fewer than
// adaptSmall files) poll at the base interval so follow-up edits are picked up
// quickly; larger change sets back off by one scale unit per hundred changed
// files (adaptScalePerHundred), capped at adaptMaxScale, because the rebuild
// that follows a big change set is itself expensive; an idle tree decays back
// toward the base interval. All thresholds are integer counts of changed files
// per poll cycle.
const (
	adaptSmall           = 20
	adaptScalePerHundred = 1
	adaptMaxScale        = 8
)

// adaptiveInterval returns the interval for the NEXT Watch poll cycle given
// the base interval, the number of changes detected in the last cycle
// (lastChanges), and the interval that last cycle used (lastInterval).
//
//   - no changes (lastChanges == 0): decay toward base — if lastInterval is
//     above base because a large change set backed off, halve it toward base;
//     once at (or below) base, poll at base.
//   - small change sets (0 < lastChanges <= adaptSmall): poll at base, a fast
//     follow-up after small edits.
//   - large change sets (lastChanges > adaptSmall): back off to
//     base * scale where scale = 1 + lastChanges/adaptScalePerHundred, capped
//     at adaptMaxScale.
//
// Pure and deterministic: the same inputs always yield the same interval, so
// Watch's polling cadence is fully predictable from the change history.
func adaptiveInterval(base time.Duration, lastChanges int, lastInterval time.Duration) time.Duration {
	if lastChanges <= 0 {
		if lastInterval > base {
			half := lastInterval / 2
			if half < base {
				return base
			}
			return half
		}
		return base
	}
	if lastChanges <= adaptSmall {
		return base
	}
	// One scale unit per hundred changed files: 100 changes -> scale 2,
	// 1000 -> 11, capped at adaptMaxScale.
	scale := 1 + (lastChanges/100)*adaptScalePerHundred
	if scale > adaptMaxScale {
		scale = adaptMaxScale
	}
	return base * time.Duration(scale)
}

// Watch polls root every interval and rebuilds + saves the index whenever the
// set of Go files or their content changes. onChange is called with the
// detected changes and the fresh index. onError receives every non-fatal
// failure (scan, build, or save) so a long-running watcher can surface
// problems instead of silently dropping them.
//
// The poll interval is adaptive (see adaptiveInterval): it stays at the base
// interval after no or small change sets and backs off — up to
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
	// base interval and recomputed per cycle by adaptiveInterval so the
	// polling cadence reacts to the change history.
	wait := interval
	for {
		cur, err := indexableHashes(root, ignore.Load(root))
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
		changes := diff(prev, cur)
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
		wait = adaptiveInterval(interval, len(changes), wait)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
}

func diff(prev, cur map[string]string) []Change {
	var changes []Change
	for f, h := range cur {
		ph, ok := prev[f]
		if !ok {
			changes = append(changes, Change{Kind: ChangeAdded, File: f})
		} else if ph != h {
			changes = append(changes, Change{Kind: ChangeModified, File: f})
		}
	}
	for f := range prev {
		if _, ok := cur[f]; !ok {
			changes = append(changes, Change{Kind: ChangeRemoved, File: f})
		}
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].File < changes[j].File })
	return changes
}

// CatchUpMaxChanges is the incremental-vs-full reconcile policy: diffs at or
// below this many changed files are applied incrementally (index.Update);
// larger diffs rebuild (index.Build). Shared by service.LoadOrBuild and
// project.Session.rebuildIndex.
const CatchUpMaxChanges = 200

// FileHashes returns a map of relative file path to content hash for every
// indexable source file under root. Exported for watcher implementations that
// need to compute change sets without rebuilding the whole index. A scan error
// is returned rather than a silent partial result.
func FileHashes(root string) (map[string]string, error) {
	return indexableHashes(root, ignore.Load(root))
}

// Diff reports the change set (adds, modifies, removes) between two hash maps
// as produced by FileHashes. Exported for watcher implementations.
func Diff(prev, cur map[string]string) []Change {
	return diff(prev, cur)
}
