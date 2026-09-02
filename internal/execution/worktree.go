package execution

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/sandbox"
)

// Worktree is an isolated copy of a project for experimentation. Unlike a
// snapshot (for rollback), a worktree is a working copy where changes are made
// and validated before being merged back.
type Worktree struct {
	srcRoot string // original project root
	workDir string // isolated copy root
}

// NewWorktree creates an isolated copy of the project, reusing sandbox.Snapshot
// to copy the tree (skipping .git, node_modules, vendor) into a temp dir.
func NewWorktree(srcRoot string) (*Worktree, error) {
	snap, err := sandbox.Snapshot(srcRoot)
	if err != nil {
		return nil, fmt.Errorf("copy worktree: %w", err)
	}
	return &Worktree{srcRoot: srcRoot, workDir: snap.Tmp()}, nil
}

// Dir returns the worktree's working directory.
func (w *Worktree) Dir() string {
	if w == nil {
		return ""
	}
	return w.workDir
}

// SourceRoot returns the original project root this worktree was copied from.
func (w *Worktree) SourceRoot() string {
	if w == nil {
		return ""
	}
	return w.srcRoot
}

// Apply applies a patch (unified diff string) to the worktree using `git
// apply`. `git apply` works on any directory (it does not require a .git
// repo), so we always use it — this avoids depending on the `patch` utility,
// which is not installed by default on Windows or stock macOS. Git is already
// a kern dependency (the indexer invokes git), so no new requirement is added.
func (w *Worktree) Apply(patch string) error {
	// git apply requires newline-terminated input; extractPatch/TrimSpace may
	// strip the trailing newline, causing "corrupt patch at line N" on the
	// last hunk line. Ensure the patch ends with a newline.
	if !strings.HasSuffix(patch, "\n") {
		patch += "\n"
	}
	// Security: a crafted patch may reference paths that escape the worktree
	// (e.g. "../outside" or "/abs/path"). Reject such patches before git apply
	// ever runs, so writes cannot land outside the worktree.
	if err := validatePatchPaths(patch); err != nil {
		return fmt.Errorf("apply patch rejected: %w", err)
	}
	cmd := exec.Command("git", "apply", "-")
	cmd.Dir = w.workDir
	cmd.Stdin = strings.NewReader(patch)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("apply patch failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Diff returns the unified diff between the worktree and the original, using
// `git diff --no-index` (a two-tree compare that works whether or not the
// worktree is a git repo). Header paths are normalized to be relative to the
// worktree root so the patch applies cleanly inside a copy.
func (w *Worktree) Diff() (string, error) {
	// `git diff --no-index` aborts with exit 128 ("fatal: cannot hash")
	// when the source tree contains a file git cannot hash — unix sockets,
	// FIFOs, device nodes (e.g. the event relay's .kern/events.sock). Move
	// every non-regular file out of the compared tree first and restore it
	// afterwards; the inode survives the round-trip, so a bound socket
	// keeps working (new dials briefly fail while it is moved aside).
	moves := moveUnhashableAside(w.srcRoot)
	defer restoreMoved(moves)

	cmd := exec.Command("git", "diff", "--no-index", "--", w.srcRoot, w.workDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		// git diff --no-index exits 1 when the trees differ; that is the
		// expected success case for a non-empty diff.
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() != 1 {
			return "", fmt.Errorf("git diff failed: %w: %s", err, strings.TrimSpace(string(out)))
		}
	}
	diff := string(out)
	// git diff --no-index emits each path relative to the filesystem root with
	// the leading slash stripped (e.g. "var/folders/.../kern-sandbox-X/added.txt"),
	// preceded by a "a/" or "b/" prefix (e.g. "b/var/folders/.../added.txt").
	// Rewrite those absolute path prefixes down to relative paths so the diff
	// carries "a/<rel>" / "b/<rel>" headers that `git apply` and `patch -p1`
	// both accept. We must match the leading-slash-stripped form: using the raw
	// root (with its leading "/") would also consume the separator after the
	// "a/" / "b/" prefix, corrupting the header into "badded.txt".
	trim := func(p string) string { return strings.TrimPrefix(p, string(filepath.Separator)) }
	sep := string(filepath.Separator)
	// Only rewrite the absolute path prefixes on diff header lines (diff --git,
	// +++, ---, rename from/to). Stripping across the whole diff would also
	// corrupt content lines that happen to contain the path prefix.
	lines := strings.Split(diff, "\n")
	for i, ln := range lines {
		if isDiffHeaderLine(ln) {
			ln = strings.ReplaceAll(ln, trim(w.srcRoot)+sep, "")
			ln = strings.ReplaceAll(ln, trim(w.workDir)+sep, "")
			lines[i] = ln
		}
	}
	// The worktree snapshot skips sandbox.SkipDirs (VCS metadata, node_modules,
	// vendor, build output, ...) but the source tree still contains them, so
	// `git diff --no-index` reports every skipped file as deleted. That noise
	// would corrupt the execute result diff, so drop whole sections whose
	// paths belong to a skipped directory.
	var filtered []string
	skip := false // true while inside a section whose path is skip-listed
	for _, ln := range lines {
		if strings.HasPrefix(ln, "diff --git ") {
			skip = skippedDiffSection(ln)
			if skip {
				continue
			}
		} else if skip {
			continue
		}
		filtered = append(filtered, ln)
	}
	return strings.Join(filtered, "\n"), nil
}

// moveUnhashableAside moves non-regular files (sockets, FIFOs, device
// nodes) out of root so git can hash every remaining entry, and returns
// the [original, hidden] pairs for restoreMoved. Symlinks stay put: git
// hashes them by target. Directories are never moved.
func moveUnhashableAside(root string) [][2]string {
	var moves [][2]string
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil // unreadable entries are git's problem to report
		}
		info, ierr := d.Info()
		if ierr != nil {
			return nil
		}
		if info.Mode().Type()&(os.ModeSocket|os.ModeNamedPipe|os.ModeDevice) == 0 {
			return nil
		}
		hidden := filepath.Join(os.TempDir(),
			fmt.Sprintf("kern-diff-hidden-%d-%d", os.Getpid(), len(moves)))
		if os.Rename(path, hidden) == nil {
			moves = append(moves, [2]string{path, hidden})
		}
		return nil
	})
	return moves
}

// restoreMoved moves files hidden by moveUnhashableAside back into place.
func restoreMoved(moves [][2]string) {
	for i := len(moves) - 1; i >= 0; i-- {
		_ = os.Rename(moves[i][1], moves[i][0])
	}
}

// skippedDiffSection reports whether a normalized "diff --git a/X b/Y" header
// references a path inside one of the snapshot's skipped directories.
func skippedDiffSection(header string) bool {
	rest := strings.TrimPrefix(header, "diff --git ")
	for _, p := range strings.Fields(rest) {
		// Paths carry "a/" or "b/" prefixes after normalization; "/dev/null"
		// is the git sentinel for added/deleted files and never a skip path.
		for _, marker := range []string{"a/", "b/"} {
			if strings.HasPrefix(p, marker) {
				p = strings.TrimPrefix(p, marker)
				break
			}
		}
		if p == "/dev/null" {
			continue
		}
		head := p
		if idx := strings.Index(head, "/"); idx >= 0 {
			head = head[:idx]
		}
		if sandbox.SkipDirs[head] {
			return true
		}
	}
	return false
}

// isDiffHeaderLine reports whether a diff line is a header line carrying a
// path, where the absolute-path prefix normalization is safe to apply.
func isDiffHeaderLine(ln string) bool {
	for _, prefix := range []string{"diff --git ", "+++ ", "--- ", "@@ ", "rename from ", "rename to "} {
		if strings.HasPrefix(ln, prefix) {
			return true
		}
	}
	return false
}

// validatePatchPaths scans a unified diff for any header path that escapes the
// worktree (contains ".." or is absolute). It returns an error describing the
// offending path so a crafted patch cannot write outside the worktree.
func validatePatchPaths(patch string) error {
	for _, ln := range strings.Split(patch, "\n") {
		trimmed := strings.TrimPrefix(ln, "\t")
		if strings.HasPrefix(trimmed, "diff --git ") ||
			strings.HasPrefix(trimmed, "+++ ") ||
			strings.HasPrefix(trimmed, "--- ") ||
			strings.HasPrefix(trimmed, "rename from ") ||
			strings.HasPrefix(trimmed, "rename to ") {
			if pathEscapes(trimmed) {
				return fmt.Errorf("path escapes worktree: %q", trimmed)
			}
		}
	}
	return nil
}

// pathEscapes reports whether a diff header line references a path outside the
// worktree, i.e. contains ".." or starts with "/".
func pathEscapes(line string) bool {
	// Strip the leading marker (a/ b/ +++/ ---/ diff --git a/ b/ rename).
	rest := line
	for _, marker := range []string{"diff --git ", "+++ ", "--- ", "rename from ", "rename to "} {
		if strings.HasPrefix(rest, marker) {
			rest = strings.TrimPrefix(rest, marker)
			break
		}
	}
	// diff --git emits two paths separated by a space; take the first.
	if idx := strings.Index(rest, " "); idx >= 0 {
		rest = rest[:idx]
	}
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return false
	}
	// /dev/null is the git diff sentinel for new files; it is not an escape.
	if rest == "/dev/null" {
		return false
	}
	if strings.HasPrefix(rest, "/") {
		return true
	}
	// Reject any path segment equal to ".." or ending with ".." traversals.
	for _, seg := range strings.Split(rest, "/") {
		if seg == ".." {
			return true
		}
	}
	return false
}

// Cleanup removes the worktree directory.
func (w *Worktree) Cleanup() error {
	if w == nil || w.workDir == "" {
		return nil
	}
	return os.RemoveAll(w.workDir)
}
