package index

import (
	"context"
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
func indexableMaxMtime(root string) (int64, int) {
	var maxMtime int64
	var count int
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
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
		info, ierr := d.Info()
		if ierr != nil {
			return nil
		}
		count++
		if mt := info.ModTime().UnixNano(); mt > maxMtime {
			maxMtime = mt
		}
		return nil
	})
	return maxMtime, count
}

// indexableHashes walks root and returns a map of relative file path to
// content hash for every indexable source file. Used by the watcher to detect
// changes and by Load/Stale to decide whether a cached index is out of date.
func indexableHashes(root string) map[string]string {
	cur := map[string]string{}
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
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
		if !isIndexable(rel, data) {
			return nil
		}
		cur[rel] = cache.Hash(data)
		return nil
	})
	return cur
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
// detected changes and the fresh index.
func Watch(ctx context.Context, root string, interval time.Duration, onChange func(changes []Change, ix *Index)) error {
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
		cur := indexableHashes(root)
		changes := diff(prev, cur)
		if len(changes) > 0 {
			ix, err := Build(root)
			if err == nil {
				if err := ix.Save(); err == nil {
					if onChange != nil {
						onChange(changes, ix)
					}
					prev = cur
				}
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
// need to compute change sets without rebuilding the whole index.
func FileHashes(root string) map[string]string {
	return indexableHashes(root)
}

// Diff reports the change set (adds, modifies, removes) between two hash maps
// as produced by FileHashes. Exported for watcher implementations.
func Diff(prev, cur map[string]string) []Change {
	return diff(prev, cur)
}
