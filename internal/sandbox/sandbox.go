// Package sandbox runs a command against a snapshot-copy of a project and
// restores the tree when the command fails. This lets risky operations (agents,
// scripts, migrations) run safely: success keeps changes, non-zero exit rolls
// everything back exactly.
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

	"github.com/JayveerPrajapati/kern/internal/processgroup"
)

// maxSnapshotBytes stops runaway copies (100 MiB).
const maxSnapshotBytes = 100 << 20

// envAllowlist is the set of environment variables that are safe and useful
// for sandboxed build/test commands. Everything else (API keys, tokens,
// credentials) is stripped so a sandboxed command cannot read or exfiltrate
// the operator's secrets.
var envAllowlist = map[string]bool{
	"PATH":            true,
	"HOME":            true,
	"USER":            true,
	"LOGNAME":         true,
	"SHELL":           true,
	"LANG":            true,
	"LC_ALL":          true,
	"LC_CTYPE":        true,
	"TERM":            true,
	"TZ":              true,
	"TMPDIR":          true,
	"TMP":             true,
	"TEMP":            true,
	"GOROOT":          true,
	"GOPATH":          true,
	"GOCACHE":         true,
	"GOMODCACHE":      true,
	"GOPROXY":         true,
	"GOSUMDB":         true,
	"GOTOOLCHAIN":     true,
	"GOFLAGS":         true,
	"CGO_ENABLED":     true,
	"CC":              true,
	"CXX":             true,
	"MAKEFLAGS":       true,
	"npm_config_cache": true,
}

// sanitizedEnv returns an environment containing only allowlisted vars from
// the operator's environment. All secrets, tokens, and API keys are dropped.
func sanitizedEnv() []string {
	src := os.Environ()
	out := make([]string, 0, len(src))
	for _, kv := range src {
		eq := strings.IndexByte(kv, '=')
		if eq < 0 {
			continue
		}
		if envAllowlist[kv[:eq]] {
			out = append(out, kv)
		}
	}
	return out
}

// SkipDirs are never copied into a snapshot.
var SkipDirs = map[string]bool{".git": true, ".hg": true, ".svn": true, "node_modules": true, "vendor": true, "dist": true, "build": true, ".kern": true, "bin": true, "graphify-out": true}

// Snap is a point-in-time copy of a tree used for rollback.
type Snap struct {
	root    string
	tmp     string
	files   []string
	skipped map[string]bool // pre-existing files not copied (size cap / read errors); never deleted on restore
	dirs    map[string]bool // relative paths of directories that existed at snapshot time
}

// Snapshot copies root into a temp directory and returns a Snap.
func Snapshot(root string) (*Snap, error) {
	tmp, err := os.MkdirTemp("", "kern-sandbox-*")
	if err != nil {
		return nil, err
	}
	s := &Snap{root: root, tmp: tmp, skipped: map[string]bool{}, dirs: map[string]bool{}}
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
			s.skipped[rel] = true
			return nil
		}
		if bytes+info.Size() > maxSnapshotBytes {
			// Skip this file but keep walking: it is pre-existing, so a
			// rollback must never remove it. Skipping the rest of the dir
			// (fs.SkipDir) would silently exclude its other files too.
			//
			// F7 (accepted design / known gap): files larger than maxSnapshotBytes
			// are not copied into the snapshot, so if the sandboxed command
			// MODIFIES such a file, rollback cannot restore the original contents
			// (the pre-existing file is left in place but in its post-command
			// state). This is a deliberate trade-off to bound snapshot memory and
			// disk; rollback guarantees apply to snapshotted files only. Do not
			// change this behaviour without a larger design.
			s.skipped[rel] = true
			return nil
		}
		data, rerr := os.ReadFile(p)
		if rerr != nil {
			s.skipped[rel] = true
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
// returned if anything could not be restored. A file that was skipped at
// snapshot time (size cap / read error) and DELETED by the run cannot be
// restored; Restore reports it as a loud error so the data loss is not silent.
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
		// Defense against path traversal: never restore a snapshot file whose
		// path escapes the snapshot root (Escape). Snapshot paths are produced
		// by walking the tree, but we treat them as untrusted before writing
		// them back into the live tree.
		if Escape(s.root, f) {
			record(fmt.Errorf("sandbox: refusing to restore path %q because it escapes the snapshot root", f))
			continue
		}
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
	// Files skipped at snapshot time (size cap / read errors) are also never
	// removed: they pre-date the run, and rollback cannot restore them.
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
		if !existed[rel] && !s.skipped[rel] {
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
	// Detect skipped files that the failed run DELETED. Files skipped at
	// snapshot time (size cap / read errors) have no backup copy, so a rollback
	// cannot restore them — but their loss must never be silent. If such a file
	// that existed at snapshot time is gone now, surface a loud error so the
	// operator knows data was lost rather than quietly proceeding.
	for rel := range s.skipped {
		if Escape(s.root, rel) {
			continue
		}
		if _, err := os.Lstat(filepath.Join(s.root, rel)); os.IsNotExist(err) {
			record(fmt.Errorf("sandbox: file %q existed at snapshot time but was DELETED by the run and could not be restored (it was skipped at snapshot time by the size/read limits); data may be lost", rel))
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
	// Sanitize the environment so sandboxed commands cannot read or exfiltrate
	// secrets (API keys, tokens) from the operator's environment. Only a
	// whitelist of build/locale-safe vars is passed through.
	c.Env = sanitizedEnv()
	// Run the command in its own process group so that on timeout the whole
	// group (the command and any grandchildren it spawns) is killed, not just
	// the direct child.
	processgroup.Set(c)
	out, err := c.CombinedOutput()
	res.Output = string(out)
	res.Duration = time.Since(start)
	if ctx.Err() == context.DeadlineExceeded || ctx.Err() == context.Canceled {
		// The context kill only reaches the direct child; kill the process
		// group so any grandchildren (e.g. a build spawning subprocesses) also
		// die and cannot linger or mutate the tree after a timeout.
		processgroup.Kill(c)
	}
	switch {
	case ctx.Err() == context.DeadlineExceeded:
		res.ExitCode = -1 // signal-killed, not a clean exit
		res.Err = fmt.Errorf("timed out after %s", timeout)
	case ctx.Err() == context.Canceled:
		res.ExitCode = -1
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
// root is resolved to an absolute path first, so the check is correct whether
// the caller passed "." (the runSandbox default), a relative path, or an
// absolute one. Without this normalization a relative root like "." makes
// every file appear to "escape" (filepath.Clean(".")+"./" never matches
// "go.mod"), which caused Restore to refuse every file and then delete the
// entire working tree — a critical data-loss bug.
func Escape(root, p string) bool {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		absRoot = filepath.Clean(root)
	}
	clean := filepath.Clean(filepath.Join(absRoot, p))
	return !strings.HasPrefix(clean, absRoot+string(filepath.Separator))
}
