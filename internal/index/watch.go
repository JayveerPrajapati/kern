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
)

func readFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

// indexableMaxMtime walks root without reading file contents and returns the
// newest modification time (Unix nanos) among indexable files plus their count.
// For every extension quickExt admits, detectLang returns a non-empty language,
// so a stat-only walk is equivalent to the content-checking indexableHashes.
// A walk error is returned so callers can decide to rebuild instead of serving
// a stale index.
func indexableMaxMtime(root string) (int64, int, error) {
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
			return nil
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil || !quickExt(rel) {
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
// map, which could wrongly mark an edited tree as unchanged.
func indexableHashes(root string) (map[string]string, error) {
	cur := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
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

// Watch polls root every interval and rebuilds + saves the index whenever the
// set of Go files or their content changes. onChange is called with the
// detected changes and the fresh index. onError receives every non-fatal
// failure (scan, build, or save) so a long-running watcher can surface
// problems instead of silently dropping them.
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
	for {
		cur, err := indexableHashes(root)
		if err != nil {
			if onError != nil {
				onError(err)
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(interval):
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
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
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

// FileHashes returns a map of relative file path to content hash for every
// indexable source file under root. Exported for watcher implementations that
// need to compute change sets without rebuilding the whole index. A scan error
// is returned rather than a silent partial result.
func FileHashes(root string) (map[string]string, error) {
	return indexableHashes(root)
}

// Diff reports the change set (adds, modifies, removes) between two hash maps
// as produced by FileHashes. Exported for watcher implementations.
func Diff(prev, cur map[string]string) []Change {
	return diff(prev, cur)
}
