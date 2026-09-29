package execution

import (
	"fmt"
	"hash/fnv"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/JayveerPrajapati/kern/internal/ignore"
	"github.com/JayveerPrajapati/kern/internal/sandbox"
)

// Worktree is an isolated copy of a project for experimentation. Unlike a
// snapshot (for rollback), a worktree is a working copy where changes are made
// and validated before being merged back.
type Worktree struct {
	srcRoot string // original project root
	workDir string // isolated copy root
	cleaner func() // optional custom cleaner (e.g. for git worktree removal)
}

// NewWorktree creates an isolated copy of the project, reusing sandbox.Snapshot
// to copy the tree (skipping .git, node_modules, vendor) into a temp dir.
func NewWorktree(srcRoot string) (*Worktree, error) {
	// Execution start (audit H1): a crashed Diff leaves the repo's .git
	// stranded in the parent dir as .kern-git-aside-<pid>-<ts>-<repo-hash>.
	// Warn about any orphans (scoped to this repo's hash; legacy suffix-less
	// asides still reported) so the user can restore manually — never
	// auto-delete.
	warnOrphanedGitAsides(srcRoot)
	snap, err := sandbox.Snapshot(srcRoot)
	if err != nil {
		return nil, fmt.Errorf("copy worktree: %w", err)
	}
	return &Worktree{srcRoot: srcRoot, workDir: snap.Tmp()}, nil
}

// NewWorktreeWithCleaner creates a Worktree with a custom working directory and cleaner callback.
func NewWorktreeWithCleaner(srcRoot, workDir string, cleaner func()) *Worktree {
	return &Worktree{
		srcRoot: srcRoot,
		workDir: workDir,
		cleaner: cleaner,
	}
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
	// The ignore matcher filters diff sections for repository-ignored
	// paths (setup-generated wiring, node_modules, generated docs). Loaded
	// once per Diff call; the walk reads only .gitignore/.kernignore files.
	ign := ignore.Load(w.srcRoot)
	// `git diff --no-index` aborts with exit 128 ("fatal: cannot hash")
	// when the source tree contains a file git cannot hash — unix sockets,
	// FIFOs, device nodes (e.g. the event relay's .kern/events.sock). Move
	// every non-regular file out of the compared tree first and restore it
	// afterwards; the inode survives the round-trip, so a bound socket
	// keeps working (new dials briefly fail while it is moved aside).
	moves := moveUnhashableAside(w.srcRoot)
	defer restoreMoved(moves)

	// Fast-path: if the worktree is backed by git (an isolated git worktree),
	// git diff HEAD in the worktree is indexed, instant, and avoids
	// recursively crawling unindexed filesystem trees.
	if fi, err := os.Stat(filepath.Join(w.workDir, ".git")); err == nil && fi != nil {
		cmd := exec.Command("git", "-C", w.workDir, "diff", "HEAD")
		if out, err := cmd.CombinedOutput(); err == nil {
			return redactCredentials(string(out)), nil
		}
	}

	// Hide .git in srcRoot if present so git diff --no-index does not traverse
	// internal git repository metadata (objects, hooks, COMMIT_EDITMSG).
	// sandbox.Snapshot already excluded .git from workDir; traversing .git
	// under git diff --no-index triggers fatal exit 128 on git versions with
	// repository boundary protections (CVE-2024-32002/32004).
	gitPath := filepath.Join(w.srcRoot, ".git")
	if fi, err := os.Stat(gitPath); err == nil && fi != nil {
		// The aside MUST live OUTSIDE both compared trees. With a relative
		// srcRoot ("." — the normal `kern execute` invocation from inside the
		// repo), filepath.Dir(".") is "." itself, which would drop the aside
		// INTO the tree git diff --no-index compares — leaking every .git
		// internal into the printed diff (dogfooding A1-N2: ~1100 spurious
		// "a/.kern-git-aside-<pid>-.../..." sections incl. binary object dumps).
		// Resolve srcRoot to an absolute path first so Dir() names the real
		// parent directory.
		absSrc, aerr := filepath.Abs(w.srcRoot)
		if aerr != nil {
			absSrc = w.srcRoot
		}
		hiddenGit := filepath.Join(filepath.Dir(absSrc), fmt.Sprintf(".kern-git-aside-%d-%d-%s", os.Getpid(), time.Now().UnixNano(), repoHash8(w.srcRoot)))
		if rerr := os.Rename(gitPath, hiddenGit); rerr == nil {
			// Signal-safe restore: the aside is registered so SIGINT/SIGTERM
			// restore it before exit; the defer covers every normal return
			// path. A crash can no longer strand the repo without its .git
			// (audit H1).
			trackGitAside(hiddenGit, gitPath)
			defer restoreGitAside(hiddenGit, gitPath)
		} else {
			hiddenGit = filepath.Join(os.TempDir(), fmt.Sprintf("kern-git-aside-%d-%d-%s", os.Getpid(), time.Now().UnixNano(), repoHash8(w.srcRoot)))
			if rerr := os.Rename(gitPath, hiddenGit); rerr == nil {
				trackGitAside(hiddenGit, gitPath)
				defer restoreGitAside(hiddenGit, gitPath)
			}
		}
	}

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
	// paths belong to a skipped directory — and also sections for files the
	// repository itself ignores (.gitignore/.kernignore: setup-generated agent
	// wiring, .opencode/node_modules, generated docs, ...), which the snapshot
	// may legitimately contain but which are not part of a change surface.
	var filtered []string
	skip := false // true while inside a section whose path is skip-listed
	for _, ln := range lines {
		if strings.HasPrefix(ln, "diff --git ") {
			skip = skippedDiffSection(ln)
			if !skip && ign != nil {
				skip = ignoredDiffSection(ln, ign)
			}
			if skip {
				continue
			}
		} else if skip {
			continue
		}
		filtered = append(filtered, ln)
	}
	// Audit H1: the diff can carry .git/config content (remote URLs with
	// embedded credentials) when the aside move fails or git traverses the
	// repo metadata. Scrub credentials from the final output so no secret
	// reaches the printed diff, artifacts, or MCP surfaces.
	return redactCredentials(strings.Join(filtered, "\n")), nil
}

// ignoredDiffSection reports whether a diff section's path is ignored by the
// repository's .gitignore/.kernignore rules. Matcher.Ignored is cheap (regexp
// per rule) and the matcher is shared across sections via Diff's caller.
func ignoredDiffSection(header string, ign *ignore.Matcher) bool {
	rest := strings.TrimPrefix(header, "diff --git ")
	for _, p := range strings.Fields(rest) {
		for _, marker := range []string{"a/", "b/"} {
			if strings.HasPrefix(p, marker) {
				p = strings.TrimPrefix(p, marker)
				break
			}
		}
		if p == "/dev/null" {
			continue
		}
		if ign.Ignored(p) {
			return true
		}
	}
	return false
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

// Cleanup removes the worktree directory and runs custom cleanup if present.
func (w *Worktree) Cleanup() error {
	if w == nil {
		return nil
	}
	if w.cleaner != nil {
		w.cleaner()
	}
	if w.workDir != "" {
		return os.RemoveAll(w.workDir)
	}
	return nil
}

// --- Credential redaction (audit H1) -----------------------------------------

var (
	// reURLCredentials matches scheme://user:token@host and rewrites it to
	// scheme://user:***@host so credentials embedded in remote URLs (e.g. a
	// GitHub PAT in .git/config) never reach diff output.
	reURLCredentials = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://)([^/@:\s]+):([^/@\s]+)@`)
	// reBareHexUserinfo scrubs a colon-less userinfo that is a bare hex
	// token — a classic 40-hex PAT used as the WHOLE userinfo
	// (https://<token>@host). The colon form above and the prefixed forms in
	// reBareGitHubPAT both miss it.
	reBareHexUserinfo = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://)([0-9a-fA-F]{40,})@`)
	// reBareGitHubPAT scrubs bare GitHub PAT tokens (ghp_ personal, gho_
	// OAuth, ghu_ user-to-server, github_pat_ fine-grained) that are not
	// embedded in a URL.
	reBareGitHubPAT = regexp.MustCompile(`\b(gh[pous]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{50,})\b`)
)

// redactCredentials scrubs credentials from text that may be printed or
// recorded: URL userinfo (scheme://user:token@host -> scheme://user:***@host,
// and a bare hex token as the whole userinfo -> scheme://***@host) plus bare
// GitHub PAT tokens.
func redactCredentials(s string) string {
	s = reURLCredentials.ReplaceAllString(s, "${1}${2}:***@")
	s = reBareHexUserinfo.ReplaceAllString(s, "${1}***@")
	return reBareGitHubPAT.ReplaceAllString(s, "[REDACTED]")
}

// --- Signal-safe .git aside restore (audit H1) --------------------------------
//
// Diff() moves the source repo's .git aside (to the parent dir or temp) while
// running git diff --no-index, and must put it back. Restore now happens on
// three paths: the normal defer, a SIGINT/SIGTERM handler, and (as a last
// resort) detection of orphaned asides by the next execution start.

var (
	gitAsideSignalOnce sync.Once
	gitAsideMu         sync.Mutex
	gitAsideRegistry   = map[string]string{} // hidden path -> original path
)

// trackGitAside registers a moved-aside .git directory so a signal handler
// can restore it, and installs the handler on first use.
func trackGitAside(hidden, original string) {
	gitAsideMu.Lock()
	gitAsideRegistry[hidden] = original
	gitAsideMu.Unlock()
	ensureGitAsideSignalRestore()
}

// restoreGitAside moves a hidden .git directory back into place and
// unregisters it. Safe to call more than once (second call is a no-op).
func restoreGitAside(hidden, original string) {
	gitAsideMu.Lock()
	if _, ok := gitAsideRegistry[hidden]; !ok {
		gitAsideMu.Unlock()
		return
	}
	delete(gitAsideRegistry, hidden)
	gitAsideMu.Unlock()
	_ = os.Rename(hidden, original)
}

// restoreAllGitAsides restores every registered aside. Used by the signal
// handler so an interrupt cannot strand the repo without its .git.
func restoreAllGitAsides() {
	gitAsideMu.Lock()
	defer gitAsideMu.Unlock()
	for hidden, original := range gitAsideRegistry {
		_ = os.Rename(hidden, original)
	}
	gitAsideRegistry = map[string]string{}
}

// ensureGitAsideSignalRestore installs a SIGINT/SIGTERM handler that restores
// all moved-aside .git directories, then re-raises the signal with default
// disposition so the process exits with the conventional 128+signal code.
func ensureGitAsideSignalRestore() {
	gitAsideSignalOnce.Do(func() {
		ch := make(chan os.Signal, 2)
		signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
		go func() {
			s := <-ch
			restoreAllGitAsides()
			signal.Stop(ch)
			if p, err := os.FindProcess(os.Getpid()); err == nil {
				_ = p.Signal(s)
			}
			time.Sleep(200 * time.Millisecond)
			os.Exit(1)
		}()
	})
}

// --- Orphaned aside detection (audit H1) --------------------------------------

// repoHash8 returns a stable 8-hex-char identity of the repo path so
// orphaned git-aside detection only warns about asides belonging to THIS
// repo — a concurrent `kern execute` in a different repo (whose aside sits in
// the same shared parent or temp dir) must not trip the warning. Legacy
// asides created before the suffix existed carry no repo identity and are
// still reported, conservatively.
func repoHash8(root string) string {
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = root
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(filepath.Clean(abs)))
	return fmt.Sprintf("%08x", h.Sum32())
}

// isGitAsideForRepo reports whether an aside directory name (after the
// prefix) belongs to the repo identified by wantHash. Names with 3+ dash
// fields end in the repo hash (pid-ts-hash); 2-field legacy names have no
// identity and always match (conservative).
func isGitAsideForRepo(name, prefix, wantHash string) bool {
	fields := strings.Split(strings.TrimPrefix(name, prefix), "-")
	if len(fields) >= 3 {
		return fields[len(fields)-1] == wantHash
	}
	return true // legacy aside (pre-suffix) — report conservatively
}

// findOrphanedGitAsides returns aside directories (in the repo's parent or in
// the temp dir) that a crashed Diff left behind. They hold the repo's .git and
// may contain credentials, so they are named but never auto-deleted. Only
// asides carrying this repo's identity (or legacy no-identity ones) are
// reported — another repo's live aside is not ours to warn about.
func findOrphanedGitAsides(srcRoot string) []string {
	var orphans []string
	want := repoHash8(srcRoot)
	parent := filepath.Dir(srcRoot)
	if entries, err := os.ReadDir(parent); err == nil {
		for _, e := range entries {
			if e.IsDir() && strings.HasPrefix(e.Name(), ".kern-git-aside-") && isGitAsideForRepo(e.Name(), ".kern-git-aside-", want) {
				orphans = append(orphans, filepath.Join(parent, e.Name()))
			}
		}
	}
	if entries, err := os.ReadDir(os.TempDir()); err == nil {
		for _, e := range entries {
			if e.IsDir() && strings.HasPrefix(e.Name(), "kern-git-aside-") && isGitAsideForRepo(e.Name(), "kern-git-aside-", want) {
				orphans = append(orphans, filepath.Join(os.TempDir(), e.Name()))
			}
		}
	}
	return orphans
}

// warnOrphanedGitAsides prints a WARNING naming every orphaned git-aside
// directory and how to restore it manually. It never deletes anything.
func warnOrphanedGitAsides(srcRoot string) {
	orphans := findOrphanedGitAsides(srcRoot)
	if len(orphans) == 0 {
		return
	}
	fmt.Fprintf(os.Stderr, "WARNING: %d orphaned git-aside director(ies) from a previously interrupted `kern execute` — the repo may be missing its .git:\n", len(orphans))
	for _, o := range orphans {
		fmt.Fprintf(os.Stderr, "  - %s\n", o)
	}
	fmt.Fprintln(os.Stderr, "  To restore manually: move each aside back to its repo's .git, e.g. `mv <aside> <repo>/.git`. They are NOT auto-deleted.")
}
