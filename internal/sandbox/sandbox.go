// Package sandbox runs a command against a snapshot-copy of a project and
// restores the tree when the command fails. This lets risky operations (agents,
// scripts, migrations) run safely: success keeps changes, non-zero exit rolls
// everything back exactly.
package sandbox

import (
	"context"
	"fmt"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/processgroup"
)

// maxSnapshotBytes is the default per-file snapshot cap (100 MiB): a file that
// would push the running snapshot total over this size is not copied into the
// snapshot. The cap prevents runaway copies from OOMing on huge generated
// files. It is configurable per-invocation via KERN_SANDBOX_MAX_SNAPSHOT_BYTES
// (see snapshotCap); files skipped for exceeding it are surfaced to callers
// via Snap.skippedOverCap and Result.SkippedFiles, never silently dropped.
const maxSnapshotBytes = 100 << 20

// snapshotCap returns the effective per-file snapshot size cap: the
// KERN_SANDBOX_MAX_SNAPSHOT_BYTES override when set (a plain byte count such
// as 500000000, or a suffixed size such as 500MB), else the default
// maxSnapshotBytes. An unparsable override logs a warning and falls back to
// the default rather than failing the snapshot.
func snapshotCap() int64 {
	if v := os.Getenv("KERN_SANDBOX_MAX_SNAPSHOT_BYTES"); v != "" {
		if n, err := parseByteSize(v); err == nil && n > 0 {
			return n
		}
		log.Printf("WARNING: invalid KERN_SANDBOX_MAX_SNAPSHOT_BYTES %q; using default snapshot cap of %d bytes", v, int64(maxSnapshotBytes))
	}
	return int64(maxSnapshotBytes)
}

// parseByteSize parses a byte size: a plain integer (bytes, e.g. "500000000")
// or a number with a unit suffix ("500MB", "2GB", "64KiB"; K/M/G are powers of
// 1024). Empty, non-numeric, and non-positive values are errors.
func parseByteSize(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty byte size")
	}
	up := strings.ToUpper(s)
	unit := int64(1)
	for _, suf := range []struct {
		suffix string
		mult   int64
	}{
		{"GIB", 1 << 30}, {"GB", 1 << 30}, {"G", 1 << 30},
		{"MIB", 1 << 20}, {"MB", 1 << 20}, {"M", 1 << 20},
		{"KIB", 1 << 10}, {"KB", 1 << 10}, {"K", 1 << 10},
	} {
		if strings.HasSuffix(up, suf.suffix) {
			unit = suf.mult
			up = strings.TrimSuffix(up, suf.suffix)
			break
		}
	}
	up = strings.TrimSpace(up)
	if up == "" {
		return 0, fmt.Errorf("missing numeric value in byte size %q", s)
	}
	n, err := strconv.ParseInt(up, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid byte size %q", s)
	}
	if n <= 0 {
		return 0, fmt.Errorf("byte size must be positive: %q", s)
	}
	return n * unit, nil
}

// envAllowlist is the set of environment variables that are safe and useful
// for sandboxed build/test commands. Everything else (API keys, tokens,
// credentials) is stripped so a sandboxed command cannot read or exfiltrate
// the operator's secrets.
var envAllowlist = map[string]bool{
	"PATH":             true,
	"HOME":             true,
	"USER":             true,
	"LOGNAME":          true,
	"SHELL":            true,
	"LANG":             true,
	"LC_ALL":           true,
	"LC_CTYPE":         true,
	"TERM":             true,
	"TZ":               true,
	"TMPDIR":           true,
	"TMP":              true,
	"TEMP":             true,
	"GOROOT":           true,
	"GOPATH":           true,
	"GOCACHE":          true,
	"GOMODCACHE":       true,
	"GOPROXY":          true,
	"GOSUMDB":          true,
	"GOTOOLCHAIN":      true,
	"GOFLAGS":          true,
	"CGO_ENABLED":      true,
	"CC":               true,
	"CXX":              true,
	"MAKEFLAGS":        true,
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
var SkipDirs = map[string]bool{".git": true, ".hg": true, ".svn": true, "node_modules": true, "vendor": true, "dist": true, "build": true, ".kern": true, ".blueprint": true, "bin": true, "graphify-out": true}

// Snap is a point-in-time copy of a tree used for rollback.
type Snap struct {
	root           string
	tmp            string
	files          []string
	skipped        map[string]bool  // pre-existing files not copied (size cap / read errors); never deleted on restore
	skippedOverCap []string         // relative paths skipped for exceeding the snapshot cap; surfaced to callers
	skippedSize    map[string]int64 // sizes (bytes) of skippedOverCap files at snapshot time, for post-run change detection
	dirs           map[string]bool  // relative paths of directories that existed at snapshot time
}

// Snapshot copies root into a temp directory and returns a Snap.
func Snapshot(root string) (*Snap, error) {
	tmp, err := os.MkdirTemp("", "kern-sandbox-*")
	if err != nil {
		return nil, err
	}
	s := &Snap{root: root, tmp: tmp, skipped: map[string]bool{}, skippedSize: map[string]int64{}, dirs: map[string]bool{}}
	var bytes int64
	capBytes := snapshotCap()
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
		if bytes+info.Size() > capBytes {
			// Skip this file but keep walking: it is pre-existing, so a
			// rollback must never remove it. Skipping the rest of the dir
			// (fs.SkipDir) would silently exclude its other files too.
			// F7: files at or above the snapshot cap are not copied into the
			// snapshot, so if the sandboxed command MODIFIES or DELETES such a
			// file, rollback cannot restore its original contents. This gap is
			// surfaced, not silent: the file is recorded in skippedOverCap,
			// reported to the caller via Result.SkippedFiles, and Run refuses
			// to present a rollback as complete when one of these files
			// changed. The cap itself is configurable via
			// KERN_SANDBOX_MAX_SNAPSHOT_BYTES for repos with large generated
			// files; the 100 MiB default stays to bound memory and disk.
			s.skipped[rel] = true
			s.skippedOverCap = append(s.skippedOverCap, rel)
			s.skippedSize[rel] = info.Size()
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

// changedSkipped returns the relative paths of files that were skipped at
// snapshot time for exceeding the cap and that the sandboxed command has since
// modified or deleted (detected by size change or disappearance). A rollback
// cannot restore these files — no copy is held — so Run surfaces them before
// restoring rather than silently presenting an incomplete rollback as a full
// one.
func (s *Snap) changedSkipped() []string {
	var changed []string
	for _, rel := range s.skippedOverCap {
		fi, err := os.Lstat(filepath.Join(s.root, rel))
		if err != nil {
			if os.IsNotExist(err) {
				changed = append(changed, rel)
			}
			continue
		}
		if fi.Size() != s.skippedSize[rel] {
			changed = append(changed, rel)
		}
	}
	sort.Strings(changed)
	return changed
}

// Restore reverts the live tree to the snapshot: tracked files are copied
// back, files that did not exist in the snapshot are removed, and empty
// directories created since are pruned. Ignored dirs (e.g. .git) are never
// descended into or removed, so rollback can never touch VCS state.
// Each file is written via a temp file + rename so a failure cannot leave a
// half-written file, and the pass continues past individual failures so a
// transient error cannot leave the tree half-reverted. The first error is
// returned if anything could not be restored. A file that was skipped at
// snapshot time (over the snapshot cap — see snapshotCap — or a read error)
// and DELETED by the run cannot be restored; Restore reports it as a loud
// error naming the file and the cap so the data loss is not silent.
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
			record(fmt.Errorf("sandbox: file %q existed at snapshot time but was DELETED by the run and could not be restored (it was skipped at snapshot time because it exceeded the snapshot cap of %d bytes or could not be read); data may be lost", rel, snapshotCap()))
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
	// SkippedFiles lists files that were not copied into the snapshot because
	// they exceeded the snapshot cap (KERN_SANDBOX_MAX_SNAPSHOT_BYTES, default
	// 100 MiB). Changes to these files cannot be rolled back, so callers should
	// treat them with care and raise the cap if they must be restore-protected.
	SkippedFiles []string
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
	// Surface skipped (over-cap) files immediately so callers know upfront
	// which files are not restore-protected by this snapshot.
	res.SkippedFiles = snap.skippedOverCap

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
		// F7: files that exceeded the snapshot cap were never copied into the
		// snapshot, so if the failed run modified or deleted one of them,
		// rollback cannot restore its original contents. Detect this BEFORE
		// restoring and surface it as an error (rather than failing late inside
		// Restore or silently presenting an incomplete rollback as a complete
		// one): the caller can then decide whether to keep the change or
		// restore the file manually.
		if changed := snap.changedSkipped(); len(changed) > 0 {
			res.Err = fmt.Errorf("cannot safely roll back: file(s) %q exceeded the snapshot cap of %d bytes and were modified or deleted by the run, so their original contents cannot be restored (raise KERN_SANDBOX_MAX_SNAPSHOT_BYTES and re-run to snapshot them, or restore manually)", strings.Join(changed, ", "), snapshotCap())
			res.OK = false
			res.Restored = false
			res.Duration = time.Since(start)
			return res
		}
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
