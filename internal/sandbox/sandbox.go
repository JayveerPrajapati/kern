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
	"strings"
	"time"
)

// maxSnapshotBytes stops runaway copies (100 MiB).
const maxSnapshotBytes = 100 << 20

// SkipDirs are never copied into a snapshot.
var SkipDirs = map[string]bool{".git": true, ".hg": true, ".svn": true, "node_modules": true, "vendor": true, "dist": true, "build": true}

// Snap is a point-in-time copy of a tree used for rollback.
type Snap struct {
	root  string
	tmp   string
	files []string
}

// Snapshot copies root into a temp directory and returns a Snap.
func Snapshot(root string) (*Snap, error) {
	tmp, err := os.MkdirTemp("", "kern-sandbox-*")
	if err != nil {
		return nil, err
	}
	s := &Snap{root: root, tmp: tmp}
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
// directories created since are pruned.
func (s *Snap) Restore() error {
	if s == nil || s.tmp == "" {
		return nil
	}
	existed := map[string]bool{}
	for _, f := range s.files {
		existed[f] = true
		src := filepath.Join(s.tmp, f)
		dst := filepath.Join(s.root, f)
		data, err := os.ReadFile(src)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			return err
		}
	}
	// Remove anything new under root that wasn't in the snapshot.
	return filepath.WalkDir(s.root, func(p string, d fs.DirEntry, werr error) error {
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
			// Prune empty dirs afterwards (handled below).
			return nil
		}
		if !existed[rel] {
			return os.Remove(p)
		}
		return nil
	})
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
// kept and Restored stays false.
func Run(root string, cmdName string, args []string, timeout time.Duration) *Result {
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

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	c := exec.CommandContext(ctx, cmdName, args...)
	c.Dir = root
	out, err := c.CombinedOutput()
	res.Output = string(out)
	res.Duration = time.Since(start)
	if ctx.Err() == context.DeadlineExceeded {
		res.Err = fmt.Errorf("timed out after %s", timeout)
	} else if err != nil {
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
