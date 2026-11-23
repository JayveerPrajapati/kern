package mcp

import (
	"context"
	"fmt"
	"github.com/JayveerPrajapati/kern/internal/app"
	"github.com/JayveerPrajapati/kern/internal/fsutil"
	"github.com/JayveerPrajapati/kern/internal/index"
	mcproot "github.com/JayveerPrajapati/kern/internal/mcp/root"
	"github.com/JayveerPrajapati/kern/internal/project"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// truncateMCP truncates s to n bytes with a visible continuation marker.
func truncateMCP(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n... (truncated)"
}

// sessionFor returns the project session for root, creating and caching one
// per root so index state and stats identity are shared across tool calls.
// The cache is bounded (session.MaxSessions): inserting beyond the cap evicts
// the least-recently-used entry that has been idle for at least
// session.SessionIdleEvict, closing its watcher and releasing its index, so a
// server that touches many distinct roots cannot accumulate a
// project.Session per root forever. Delegates to the session LRU.
func (s *Server) sessionFor(root string) *project.Session {
	sess, _ := s.sessCache.For(root)
	return sess
}

// evictSessionState drops the root-owned companion state that travels with a
// session: the cached Platform (full call graph plus twin-merged knowledge
// state) and its per-root build lock. Called by the session cache's onEvict
// callback when an idle session is evicted, so a long-lived server touching
// many distinct roots cannot leak one graph per root (mirrors the sessions
// LRU bound). The per-root build lock goes too; the next platformFor for this
// root allocates a fresh one.
func (s *Server) evictSessionState(root string) {
	s.mu.Lock()
	delete(s.platforms, root)
	delete(s.platformLocks, root)
	s.mu.Unlock()
}

// resolveRoot cleans a tool root argument to an absolute path and requires it
// to be an existing directory. An empty root falls back to the current working
// directory. This guards every index-using tool against traversal-style root
// values and turns confusing downstream index errors into clear ones.
func resolveRoot(r string) string {
	return mcproot.ResolveRoot(r)
}

// withinRoot resolves file against root (absolute paths are used as-is) and
// requires the result to stay inside root, rejecting `..` escapes, absolute
// paths that point outside the project boundary, and symlink escapes (a
// symlink inside the project that points outside). It returns the resolved
// absolute path.
func withinRoot(root, file string) (string, error) {
	return fsutil.WithinRoot(root, file)
}

// validateRoot rejects a tool root that exists on disk but is not a directory
// (e.g. a file). A nonexistent root is allowed because several tools create the
// target directory before indexing it. The filesystem root ("/") is also
// rejected: it is never a project to index. It returns "" on success so it can
// be called inline as `if msg := validateRoot(root); msg != "" { return "", msg }`.
func validateRoot(root string) error {
	clean := filepath.Clean(resolveRoot(root))
	if isFilesystemRoot(clean) {
		return fmt.Errorf("root %q is a filesystem root and cannot be treated as a project", clean)
	}
	if st, err := os.Stat(clean); err == nil {
		if !st.IsDir() {
			return fmt.Errorf("root %q exists but is not a directory", clean)
		}
	}
	return nil
}

// indexScope carries the symbol index loaded during one tool call so provenance
// can be stamped onto that same call's response. It lives on the per-request
// context instead of the Server struct, so concurrent tool calls never share a
// mutable lastIndex and read each other's provenance. token is the client's MCP
// progress token (from the request's _meta), threaded so slow handlers (verify)
// can emit phase-level progress notifications (M4). etag/unchanged carry the
// conditional-fetch verdict (ADR-0012): runTool stamps the response etag on
// every eligible tool and marks the call unchanged when it short-circuits, so
// toolCallResponse can attach result["etag"] / result["unchanged"].
// routedName/routedArgs memoize resolveCacheCall's kern_meta classification
// for THIS call (runTool resolves it once at the top and the D1 cache gates,
// fingerprints and keys reuse the memo instead of re-running the classifier),
// so the memo is valid only for the (name, args) runTool received and must be
// reset at the top of every runTool entry (composed steps re-enter under the
// same scope).
type indexScope struct {
	ix        *index.Index
	prov      *Provenance // structured evidence stamped by retrieval handlers
	token     string      // MCP progress token; "" = client did not opt in
	etag      string      // conditional-fetch etag for eligible responses ("" = tool not eligible)
	unchanged bool        // true when the response is a short-circuit "unchanged (etag E)"
	sliced    bool        // true when this call is a retained-output slice re-read (R7): the response is served from the cursor store without executing the tool; toolCallResponse still applies the output budget (A3) and, on truncation, mints a chained anchor for the elided remainder
	routed    routedCall  // kern_meta classification memo (see resolveCacheCall)
}

// routedCall memoizes resolveCacheCall's output for one kern_meta call so the
// classifier runs once per call instead of once per cache gate/key/fingerprint
// (D1). routedSet distinguishes "no memo" (compute) from "memo present".
type routedCall struct {
	name      string
	args      map[string]any
	routedSet bool
}
type indexScopeKey struct{}

// loadIndex returns the session's symbol index, reused while fresh and rebuilt
// when stale or missing (see project.Session.Index). The returned index is
// recorded on the per-call scope so the tool response can be stamped with
// provenance.
func (s *Server) loadIndex(ctx context.Context, root string) (*index.Index, error) {
	// ADR-0011: prefer the watcher-published index when one exists — the
	// background reactive watcher has already rebuilt and atomically swapped
	// it in, so it is at least as fresh as anything the session can produce,
	// and the swap is lock-free (each call sees either the old or the new
	// instance, never a half-built one).
	if ix, ok := s.watchedIndex(root); ok {
		s.indexedRoots.Store(root, true)
		if scope, ok := ctx.Value(indexScopeKey{}).(*indexScope); ok {
			scope.ix = ix
		}
		if err := rejectEmptyIndex(root, ix); err != nil {
			return nil, err
		}
		return ix, nil
	}
	// The historical "first index build in progress" notice was removed: it
	// predicted the build by decoding the full on-disk index (index.Load +
	// tree-OID probe — a multi-MB decode per root per process) purely to
	// print a stderr line, then Session.Index loaded it again. Tool calls
	// wait on the build below silently; preloadIndexes and the background
	// watch warm roots ahead of time.
	ix, err := s.sessionFor(root).IndexWithin(indexWaitBudget())
	if err != nil {
		return nil, err
	}
	s.indexedRoots.Store(root, true)
	if scope, ok := ctx.Value(indexScopeKey{}).(*indexScope); ok {
		scope.ix = ix
	}
	// F2: a root that yields an empty index (zero files → the recorded
	// content root is the SHA-256 of the empty string) must surface as a
	// real error instead of a successful 0-symbol "fresh" result. A
	// configured/selected root that indexes nothing is almost always a
	// misconfiguration — a missing dir, a fileless cwd, or a workspace env
	// that was ignored — and silently serving it hides the problem.
	if err := rejectEmptyIndex(root, ix); err != nil {
		return nil, err
	}
	return ix, nil
}

// indexWaitBudget bounds how long one tool call waits on an index build. On a
// large repo the first build takes tens of seconds; past this budget the call
// returns a retryable "still building" error (the build keeps running in the
// background) instead of hanging until the host gives up. Override with
// KERN_INDEX_WAIT_SECONDS; 0 waits indefinitely.
func indexWaitBudget() time.Duration {
	const def = 25 * time.Second
	v := strings.TrimSpace(os.Getenv("KERN_INDEX_WAIT_SECONDS"))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return def
	}
	return time.Duration(n) * time.Second
}

// rejectEmptyIndex turns a silently-empty index into a real error. The index
// is empty exactly when zero files were indexed, which makes the recorded
// content root aggregateHash({}) — the SHA-256 of the empty string — and
// every freshness check trivially "fresh". Callers must not present that as
// a clean answer.
func rejectEmptyIndex(root string, ix *index.Index) error {
	if ix == nil || len(ix.FileHashes) > 0 {
		return nil
	}
	abs := resolveAbs(root)
	if os.Getenv("KERN_GREENFIELD") == "1" || os.Getenv("KERN_ALLOW_EMPTY") == "1" {
		return nil
	}
	reason := "no indexable source files were found under it"
	if st, err := os.Stat(abs); err != nil || !st.IsDir() {
		reason = fmt.Sprintf("the path %q does not exist or is not a directory", abs)
	}
	return fmt.Errorf("empty index for %s: %s — the root argument or KERN_MCP_ROOTS/KERN_ROOTS is not pointing at a code tree", abs, reason)
}

// platformEntry ties a cached Platform to the exact index instance it was
// built from. The Platform owns the call graph and the twin-merged knowledge
// graph; constructing it runs intel.FromIndex plus four twin
// extractors, each a full filesystem walk. Reusing it while the session
// serves the same index instance turns ~5 tree walks per high-level tool
// call into zero.
type platformEntry struct {
	ix *index.Index
	p  *app.Platform
}

// platformFor returns the shared application Platform for root, caching it
// per root keyed by the index instance it was built from. While
// project.Session serves the same *index.Index pointer (fresh index, 1s
// staleness cooldown), the cached Platform is returned; a real index rebuild
// allocates a new instance, so the next call rebuilds the Platform exactly
// once. The Platform and its graph are treated as read-only after
// construction (same contract as web.App), so sharing across tool calls is
// safe. Handlers that previously called loadIndex + app.NewWithIndex per
// call should use this instead.
func (s *Server) platformFor(ctx context.Context, root string) (*app.Platform, error) {
	ix, err := s.loadIndex(ctx, root)
	if err != nil {
		return nil, err
	}
	root = resolveRoot(root)
	s.mu.Lock()
	if s.platforms == nil {
		s.platforms = map[string]*platformEntry{}
		s.platformLocks = map[string]*sync.Mutex{}
	}
	if e, ok := s.platforms[root]; ok && e.ix == ix {
		p := e.p
		s.mu.Unlock()
		return p, nil
	}
	lk := s.platformLocks[root]
	if lk == nil {
		lk = &sync.Mutex{}
		s.platformLocks[root] = lk
	}
	s.mu.Unlock()
	// Serialize the build per root: two concurrent first-calls for the same
	// root used to both run app.NewWithIndex (several full tree walks each)
	// and discard the loser's graph. With the lock exactly one goroutine
	// builds per root; the rest wait and reuse the cached Platform
	// (re-checked under the lock).
	lk.Lock()
	defer lk.Unlock()
	s.mu.Lock()
	if e, ok := s.platforms[root]; ok && e.ix == ix {
		p := e.p
		s.mu.Unlock()
		return p, nil
	}
	s.mu.Unlock()
	p, err := app.NewWithIndex(root, ix)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	// Another caller may have populated the cache while we built; prefer the
	// existing entry when it matches the same index instance (identical
	// content, saves the duplicate graph).
	if e, ok := s.platforms[root]; ok && e.ix == ix {
		p = e.p
	} else {
		s.platforms[root] = &platformEntry{ix: ix, p: p}
	}
	s.mu.Unlock()
	return p, nil
}

// commit returns the short HEAD commit of root, cached per root. Git is
// optional: a non-repo root or missing git yields "" (omitted from the stamp).
func (s *Server) commit(root string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.commits[root]; ok {
		return c
	}
	c := ""
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "git", "-C", root, "rev-parse", "--short", "HEAD").Output(); err == nil {
		c = strings.TrimSpace(string(out))
	}
	s.commits[root] = c
	return c
}
