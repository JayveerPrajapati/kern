// Package sandbox runs a command against a snapshot-copy of a project and
// restores the tree when the command fails (#15). This lets risky operations
// (agents, scripts, migrations) run safely: success keeps changes, non-zero
// exit rolls everything back exactly.
package sandbox

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// maxSnapshotBytes stops runaway copies (100 MiB).
const maxSnapshotBytes = 100 << 20

// SkipDirs are never copied into a snapshot.
var SkipDirs = map[string]bool{".git": true, ".hg": true, ".svn": true, "node_modules": true, "vendor": true, "dist": true, "build": true, ".kern": true}

// Snap is a point-in-time copy of a tree used for rollback.
type Snap struct {
	root  string
	tmp   string
	files []string
	dirs  map[string]bool // relative paths of directories that existed at snapshot time
}

// Snapshot copies root into a temp directory and returns a Snap.
func Snapshot(root string) (*Snap, error) {
	tmp, err := os.MkdirTemp("", "kern-sandbox-*")
	if err != nil {
		return nil, err
	}
	s := &Snap{root: root, tmp: tmp, dirs: map[string]bool{}}
	var bytes int64
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return nil
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return rerr
		}
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			// Record the dir before possibly skipping it so rollback can tell
			// a pre-existing dir (never pruned) from a newly created one.
			s.dirs[rel] = true
			if SkipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(tmp, rel), 0o755)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return nil
		}
		if bytes+info.Size() > maxSnapshotBytes {
			return fs.SkipDir
		}
		data, rerr := os.ReadFile(p)
		if rerr != nil {
			return nil
		}
		bytes += int64(len(data))
		s.files = append(s.files, rel)
		return os.WriteFile(filepath.Join(tmp, rel), data, 0o644)
	})
	if err != nil {
		os.RemoveAll(tmp)
		return nil, err
	}
	return s, nil
}

// Restore reverts the live tree to the snapshot: tracked files are copied
// back, files that did not exist in the snapshot are removed, and empty
// directories created since are pruned. Ignored dirs (e.g. .git) are never
// descended into or removed, so rollback can never touch VCS state.
//
// Each file is written via a temp file + rename so a failure cannot leave a
// half-written file, and the pass continues past individual failures so a
// transient error cannot leave the tree half-reverted. The first error is
// returned if anything could not be restored.
func (s *Snap) Restore() error {
	if s == nil || s.tmp == "" {
		return nil
	}
	var firstErr error
	record := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	existed := map[string]bool{}
	for _, f := range s.files {
		existed[f] = true
		src := filepath.Join(s.tmp, f)
		dst := filepath.Join(s.root, f)
		data, err := os.ReadFile(src)
		if err != nil {
			record(err)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			record(err)
			continue
		}
		record(writeAtomic(dst, data))
	}
	// Remove anything new under root that wasn't in the snapshot. Ignored
	// dirs are still skipped: their contents are not snapshotted, so deleting
	// them would destroy pre-existing state (or VCS data) we cannot restore.
	_ = filepath.WalkDir(s.root, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return nil
		}
		rel, rerr := filepath.Rel(s.root, p)
		if rerr != nil || rel == "." {
			return nil
		}
		if d.IsDir() {
			if SkipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !existed[rel] {
			record(os.Remove(p))
		}
		return nil
	})
	// Prune empty directories the command created, deepest first. Directories
	// that existed at snapshot time are never removed even if now empty.
	var dirs []string
	_ = filepath.WalkDir(s.root, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil || p == s.root {
			return nil
		}
		if d.IsDir() {
			if SkipDirs[d.Name()] {
				return filepath.SkipDir
			}
			dirs = append(dirs, p)
		}
		return nil
	})
	sort.Sort(sort.Reverse(sort.StringSlice(dirs)))
	for _, dir := range dirs {
		rel, _ := filepath.Rel(s.root, dir)
		if s.dirs[rel] {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err == nil && len(entries) == 0 {
			record(os.Remove(dir))
		}
	}
	return firstErr
}

// writeAtomic writes data to path via a same-directory temp file + rename, so
// readers never observe a partially written file and the write either lands
// fully or not at all.
func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".kern-restore-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if werr != nil {
		os.Remove(tmpName)
		return werr
	}
	if cerr != nil {
		os.Remove(tmpName)
		return cerr
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// Close removes the temp snapshot.
func (s *Snap) Close() {
	if s != nil && s.tmp != "" {
		os.RemoveAll(s.tmp)
	}
}

// Tmp returns the temp snapshot directory.
func (s *Snap) Tmp() string {
	if s == nil {
		return ""
	}
	return s.tmp
}

// Result of a sandboxed run.
type Result struct {
	OK        bool
	ExitCode  int
	Output    string
	Err       error
	Duration  time.Duration
	Restored  bool
	Snapshots int // count of snapshot bytes copied (files)
}

// Run snapshots root, executes cmd in root, and restores the tree when the
// command exits non-zero (or errors/times out). On success the changes are
// kept and Restored stays false. parent cancels the run (and triggers a
// restore) when it is cancelled; a nil parent uses context.Background().
func Run(parent context.Context, root string, cmdName string, args []string, timeout time.Duration) *Result {
	if parent == nil {
		parent = context.Background()
	}
	res := &Result{}
	start := time.Now()
	snap, err := Snapshot(root)
	if err != nil {
		res.Err = fmt.Errorf("snapshot: %w", err)
		res.Duration = time.Since(start)
		return res
	}
	defer snap.Close()
	res.Snapshots = len(snap.files)

	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	c := exec.CommandContext(ctx, cmdName, args...)
	c.Dir = root
	out, err := c.CombinedOutput()
	res.Output = string(out)
	res.Duration = time.Since(start)
	switch {
	case ctx.Err() == context.DeadlineExceeded:
		res.Err = fmt.Errorf("timed out after %s", timeout)
	case ctx.Err() == context.Canceled:
		res.Err = fmt.Errorf("cancelled")
	case err != nil:
		if ee, ok := err.(*exec.ExitError); ok {
			res.ExitCode = ee.ExitCode()
		} else {
			res.Err = err
		}
	}
	if res.ExitCode != 0 || res.Err != nil {
		if rerr := snap.Restore(); rerr != nil {
			res.Err = fmt.Errorf("%v (restore also failed: %v)", res.Err, rerr)
		} else {
			res.Restored = true
		}
		res.OK = false
		res.Duration = time.Since(start)
		return res
	}
	res.OK = true
	res.Duration = time.Since(start)
	return res
}

// Escape checks a path stays within root (defense against traversal).
func Escape(root, p string) bool {
	clean := filepath.Clean(filepath.Join(root, p))
	return !strings.HasPrefix(clean, filepath.Clean(root)+string(filepath.Separator))
}
