package index

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/JayveerPrajapati/kern/internal/ignore"
)

// IndexIdentity is the build-time content identity of an index. ContentRoot
// is the authoritative fingerprint (a flat SHA-256 over sorted "path=hash"
// lines of every indexed file); TreeOID and GitCommit are best-effort git
// stamps, empty when root is not a git worktree or git is unavailable.
type IndexIdentity struct {
	TreeOID     string    `json:"tree_oid,omitempty"`
	ContentRoot string    `json:"content_root"`
	GitCommit   string    `json:"git_commit,omitempty"`
	BuiltAt     time.Time `json:"built_at"`
}

// FreshnessVerdict is the outcome of a freshness proof.
type FreshnessVerdict string

const (
	// FreshnessFresh means the on-disk tree still matches what the index was
	// built from.
	FreshnessFresh FreshnessVerdict = "fresh"
	// FreshnessStale means at least one indexed file changed since Build.
	FreshnessStale FreshnessVerdict = "stale"
	// FreshnessUnknown means there is no baseline identity to compare against
	// (nil index / nil Identity) or the current tree could not be verified.
	FreshnessUnknown FreshnessVerdict = "unknown"
)

// FreshnessProof pairs the recorded build-time identity with the current
// on-disk state and a verdict, so consumers can audit WHY an index was judged
// fresh or stale instead of trusting a hard-coded literal.
type FreshnessProof struct {
	Verdict   FreshnessVerdict `json:"verdict"`
	Recorded  IndexIdentity    `json:"recorded"`
	Current   IndexIdentity    `json:"current"`
	CheckedAt time.Time        `json:"checked_at"`
}

// Stale reports whether the proof judged the index out of date. Unknown is
// treated as stale (fail-closed): an index whose freshness cannot be proven
// must not be trusted.
func (p FreshnessProof) Stale() bool { return p.Verdict != FreshnessFresh }

// FreshnessSnapshot is the per-file content observation captured during a
// staleness check's content walk: every indexable file's content hash plus
// the mtime observed in the SAME walk (indexableHashesObserved). A
// subsequent incremental Update can reuse these hashes instead of re-reading
// and re-hashing the whole tree — each file's hash is trusted only after the
// current stat re-validates that its mtime is unchanged since the walk, so a
// file edited between the staleness check and the update is re-read like any
// other changed file. The zero value (nil maps) means "no usable
// observation".
type FreshnessSnapshot struct {
	Hashes map[string]string `json:"-"`
	Mtimes map[string]int64  `json:"-"`
	// Sizes is the per-file size (bytes) observed in the same walk as
	// Mtimes. The stat-gate compares mtime AND size: a size-changing
	// mtime-preserving edit (e.g. git apply) is caught by this leg even
	// though the mtime alone would have cleared it.
	Sizes map[string]int64 `json:"-"`
	// WalkCount is the number of files the producing walk VISITED
	// (quickExt+ignore filtered — a superset of Hashes, which only counts
	// content-indexable files). The stat-gate compares against this so a
	// newly added file forces the full walk even when no recorded file
	// changed.
	WalkCount int `json:"-"`
	// VerifiedAt is when the producing walk completed. The stat-gate is
	// only honored for gateTTL after this (a same-size mtime-preserving
	// edit evades the stats; the TTL bounds how long that can go
	// unverified).
	VerifiedAt time.Time `json:"-"`
	// WasFresh records whether the producing walk MATCHED the index's
	// recorded content root. The stat-gate proves "tree unchanged since
	// that walk" — WasFresh turns that into the correct verdict: an
	// unchanged tree over a mismatching walk must serve STALE, not fresh.
	WasFresh bool `json:"-"`
}

// retainStaleSnapshot stores a completed content-walk observation on ix for
// a following UpdateWithSnapshot or stat-gated staleness check; a nil
// observation clears any previous one. wasFresh records whether the walk
// matched the index's recorded content root (the verdict the stat-gate
// re-serves while the tree stays unchanged). Retention is bounded: every
// subsequent staleness check either replaces it (content walk) or clears it
// (no walk ran).
func (ix *Index) retainStaleSnapshot(obs walkObservation, wasFresh bool) {
	mu := ix.staleMuRef()
	mu.Lock()
	if obs.hashes == nil {
		ix.staleSnapshot = nil
		mu.Unlock()
		return
	}
	ix.staleSnapshot = &FreshnessSnapshot{
		Hashes:     obs.hashes,
		Mtimes:     obs.mtimes,
		Sizes:      obs.sizes,
		WalkCount:  obs.walkCount,
		VerifiedAt: time.Now(),
		WasFresh:   wasFresh,
	}
	mu.Unlock()
	ix.persistFreshnessSnapshot(obs)
}

// freshnessSnapshotFile is the cross-process freshness observation. A non-git
// root has no git OID to consult, so without persisting the walk every
// one-shot process re-hashes the whole tree; the stat-gate can re-validate
// this observation with stats only (mtime+size+file-set) for gateTTL.
const freshnessSnapshotFile = "freshness-snapshot.json"

type persistedFreshness struct {
	Root        string            `json:"root"`
	ContentRoot string            `json:"content_root"`
	Hashes      map[string]string `json:"hashes"`
	Mtimes      map[string]int64  `json:"mtimes"`
	Sizes       map[string]int64  `json:"sizes"`
	WalkCount   int               `json:"walk_count"`
	VerifiedAt  time.Time         `json:"verified_at"`
}

// persistFreshnessSnapshot writes the completed content walk beside the index.
// Best-effort: a failure only costs the next process a full walk.
func (ix *Index) persistFreshnessSnapshot(obs walkObservation) {
	if ix == nil || ix.Root == "" || obs.hashes == nil {
		return
	}
	data, err := json.Marshal(persistedFreshness{
		Root:        ix.Root,
		ContentRoot: aggregateHash(obs.hashes),
		Hashes:      obs.hashes,
		Mtimes:      obs.mtimes,
		Sizes:       obs.sizes,
		WalkCount:   obs.walkCount,
		VerifiedAt:  time.Now(),
	})
	if err != nil {
		return
	}
	dir := filepath.Join(ix.Root, ".kern")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	path := filepath.Join(dir, freshnessSnapshotFile)
	tmp, err := os.CreateTemp(dir, ".freshness-snapshot.*.tmp")
	if err != nil {
		return
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return
	}
	if err := tmp.Close(); err != nil {
		return
	}
	_ = os.Rename(name, path)
}

// loadPersistedFreshness seeds the in-memory stat-gate with the observation a
// previous process persisted, so a non-git root's staleness check can be
// stat-only instead of re-reading and re-hashing every file.
func (ix *Index) loadPersistedFreshness() {
	if ix == nil || ix.Root == "" {
		return
	}
	data, err := os.ReadFile(filepath.Join(ix.Root, ".kern", freshnessSnapshotFile))
	if err != nil {
		return
	}
	var p persistedFreshness
	if err := json.Unmarshal(data, &p); err != nil || p.Hashes == nil {
		return
	}
	if filepath.Clean(p.Root) != filepath.Clean(ix.Root) {
		return
	}
	if ix.Identity == nil {
		return
	}
	// An observation taken before the current index was written describes a
	// previous index generation (e.g. the stale verdict from before a
	// rebuild). Seeding it would make every process serve that stale verdict
	// until the TTL expires even though the rebuilt index matches the tree.
	if p.VerifiedAt.Before(ix.UpdatedAt) {
		return
	}
	wasFresh := p.ContentRoot == ix.Identity.ContentRoot
	ix.staleSnapshot = &FreshnessSnapshot{
		Hashes:     p.Hashes,
		Mtimes:     p.Mtimes,
		Sizes:      p.Sizes,
		WalkCount:  p.WalkCount,
		VerifiedAt: p.VerifiedAt,
		WasFresh:   wasFresh,
	}
}

// clearStaleSnapshot drops any retained observation. Called whenever a
// staleness check decides WITHOUT a content walk, so a snapshot is never
// reused across decisions it did not come from.
func (ix *Index) clearStaleSnapshot() {
	mu := ix.staleMuRef()
	mu.Lock()
	defer mu.Unlock()
	ix.staleSnapshot = nil
}

// retainedSnapshot returns the current observation under staleMu, so
// UpdateWithSnapshot's read cannot race a concurrent staleness check's
// retention (staleness checks fan out off any app-level lock by design).
func (ix *Index) retainedSnapshot() *FreshnessSnapshot {
	mu := ix.staleMuRef()
	mu.Lock()
	defer mu.Unlock()
	return ix.staleSnapshot
}

// identityGit holds the walk-independent git observations of an index
// identity (tree OID, commit). StartIdentityGit launches them on a
// background goroutine so Build/Update can overlap the (slow) git staging
// dance with their file walk; joinIdentity waits and assembles the final
// identity once FileHashes are final.
type identityGit struct {
	wg     sync.WaitGroup
	oid    string
	commit string
}

// isGitWorktree reports whether root is located within a git repository worktree.
// When root is not a git worktree, expensive git subprocesses are bypassed.
func isGitWorktree(root string) bool {
	if root == "" {
		return false
	}
	if os.Getenv("GIT_DIR") != "" || os.Getenv("GIT_WORK_TREE") != "" {
		return true
	}
	dir, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return true
		}
		parent := filepath.Dir(dir)
		if parent == dir || parent == "." {
			break
		}
		dir = parent
	}
	return false
}

// startIdentityGit begins observing root's git identity concurrently. Call
// joinIdentity when the build/update walk has finished.
func startIdentityGit(root string) *identityGit {
	g := &identityGit{}
	if !isGitWorktree(root) {
		return g
	}
	ensureGitExclude(root)
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		g.oid = treeOID(root)
		if out, err := runGit(root, "rev-parse", "--short", "HEAD"); err == nil {
			g.commit = out
		}
	}()
	return g
}

// joinIdentity waits for the git observations and returns the assembled
// identity. fileHashes/builtAt must be final (the walk complete).
func (g *identityGit) joinIdentity(fileHashes map[string]string, builtAt time.Time) *IndexIdentity {
	g.wg.Wait()
	return &IndexIdentity{
		ContentRoot: aggregateHash(fileHashes),
		BuiltAt:     builtAt,
		TreeOID:     g.oid,
		GitCommit:   g.commit,
	}
}

// aggregateHash is a flat content fingerprint: SHA-256 over sorted "path=hash"
// lines (one per indexed file), hex-encoded. It is not a Merkle tree — paths
// are sorted and hashed as a single stream, so the digest changes whenever any
// file is added, removed, or edited.
func aggregateHash(fileHashes map[string]string) string {
	paths := make([]string, 0, len(fileHashes))
	for p := range fileHashes {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	h := sha256.New()
	for _, p := range paths {
		h.Write([]byte(p + "=" + fileHashes[p] + "\n"))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// treeOID returns the git tree object ID of the CURRENT WORKING TREE at root,
// or "" when root is not a git worktree (or git is unavailable).
//
// Fast path: when nothing changed outside the excluded tool-state dirs, the
// current tree object is exactly HEAD's tree object — two cheap queries (a
// porcelain status and a rev-parse), no staging and no object writes. The
// porcelain pathspecs exclude BOTH .kern and .blueprint; status is read-only
// and does not error on a pathspec that names an ignored path, so the .kern
// pathspec is safe here even though the `git add` form of it is fatal in the
// slow path (see below). The two paths describe the same file set.
//
// Slow path (tree dirty, or status unavailable): a plain `git write-tree`
// only reflects the staged index, so an unstaged edit — exactly what
// `git apply` produces, mtime included — would be invisible to it, and the
// mtime-preserving-edit regression this identity exists to catch would sail
// through as "fresh". Instead we point GIT_INDEX_FILE at a throwaway index,
// stage the working tree into it, and write-tree from that. The repo's real
// index is never touched; the only side effect is a dangling tree/blob in
// the object database, which git gc reaps.
//
// .kern and .blueprint hold untracked tool state (index.json, audit logs,
// approvals) that is never part of the code identity. Without excluding them,
// every blueprint audit append would change the tree OID, mark the source
// index stale, and force a rebuild — and because Save() writes
// .kern/index.json AFTER Build has captured the identity, leaving it in the
// tree would defeat the TreeOID fast path entirely on fresh repos. The two
// are excluded differently because of a git quirk:
//
//   - .blueprint is NOT gitignored (verified: `git check-ignore .blueprint`
//     reports not ignored), so it must be excluded by an explicit pathspec
//     in BOTH paths.
//   - .kern IS gitignored by convention — kern's own setup wires `.kern/`
//     into the repo's ignore rules (.git/info/exclude via ensureGitExclude
//     on Save, or a tracked .gitignore in manual setups) — so `git add -A`
//     auto-skips it. The slow path MUST NOT exclude it by pathspec: `git
//     add` exits 1 with "The following paths are ignored by one of your
//     .gitignore files: .kern" when a pathspec names an existing ignored
//     path, and --ignore-errors does not suppress that, so the whole slow
//     path aborts and treeOID returns "" — silently disabling the tree-OID
//     identity in every repo that has a .kern directory (the live defect
//     this comment documents). Gitignored paths need no pathspec; the fast
//     path keeps the .kern pathspec only because porcelain status tolerates
//     it.
//
// treeOIDFast is the cheap half of treeOID: two git queries (porcelain
// status + rev-parse), no staging, no object writes. It returns HEAD's tree
// OID when the working tree is porcelain-clean outside the excluded
// tool-state dirs, and "" when the tree is dirty, root is not a worktree,
// or git is unavailable. Callers on a hot path use this first and only pay
// for the full staging form when they truly need a decisive OID.
func treeOIDFast(root string) string {
	if !isGitWorktree(root) {
		return ""
	}
	if out, err := runGit(root, "status", "--porcelain", "--", ".", ":(exclude).kern", ":(exclude).kern/**", ":(exclude).blueprint", ":(exclude).blueprint/**"); err == nil && out == "" {
		if head, err := runGit(root, "rev-parse", "HEAD^{tree}"); err == nil && head != "" {
			return head
		}
	}
	return ""
}

func treeOID(root string) string {
	if !isGitWorktree(root) {
		return ""
	}
	ensureGitExclude(root)
	// Fast path: porcelain-clean outside the excluded dirs means staging the
	// working tree (slow path) would produce exactly HEAD's tree object.
	if oid := treeOIDFast(root); oid != "" {
		return oid
	}

	tmp, err := os.CreateTemp("", "kern-treeoid-*")
	if err != nil {
		return ""
	}
	idxPath := tmp.Name()
	_ = tmp.Close()
	// Remove the (empty) placeholder: git treats a pre-existing 0-byte index
	// file as corrupt ("index file smaller than expected") instead of as an
	// empty index, so let git create the file itself.
	if err := os.Remove(idxPath); err != nil {
		return ""
	}
	defer func() { _ = os.Remove(idxPath) }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	env := append(os.Environ(), "GIT_INDEX_FILE="+idxPath)

	// Stage the whole working tree into the throwaway index. No
	// --ignore-errors (B3, deep-dive 2026-10-03): the flag silently DROPS
	// unreadable/locked files from the throwaway index, so an edit to such a
	// file would be invisible to the tree OID and a stale index could be
	// judged fresh. Without it, an unreadable file makes `git add` fail,
	// treeOID returns "", and callers fall back to the content-hash proof —
	// fail-closed. git's own ignore rules are honored exactly as the index's
	// ignore policy honors them for gitignored paths. .blueprint is excluded
	// by an explicit pathspec (it is NOT gitignored); .kern is excluded by
	// git's own ignore rules instead — a pathspec naming it makes `git add`
	// exit 1, aborting the entire slow path (see the doc comment above for
	// the full explanation).
	add := exec.CommandContext(ctx, "git", "-C", root, "add", "-A", "--", ".", ":(exclude).blueprint")
	add.Env = env
	if err := add.Run(); err != nil {
		return ""
	}

	cmd := exec.CommandContext(ctx, "git", "-C", root, "write-tree")
	cmd.Env = env
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// runGit runs `git -C <root> <args...>` with a 5s timeout and returns the
// trimmed stdout. It returns an error when git is absent, root is not a
// worktree, or the command exits non-zero. Local package helper (internal/git
// does not exist; kept package-local).
func runGit(root string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// proofMemo caches one non-strict FreshnessProof per index instance:
// single-flight (concurrent callers share one in-flight computation — a
// 100-request burst pays one tree walk or git staging pass, not 100) and
// TTL-bounded (warm calls re-serve the last verdict instead of re-walking
// the tree). It is never serialized.
type proofMemo struct {
	mu       sync.Mutex
	proof    FreshnessProof
	until    time.Time
	inFlight bool
	done     chan struct{}
}

// serve returns the cached proof when one exists and is not expired.
func (m *proofMemo) serve() (FreshnessProof, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.inFlight && time.Now().Before(m.until) {
		return m.proof, true
	}
	return FreshnessProof{}, false
}

// claim installs the caller as the computation leader (returns its done
// channel, true) or hands back the running leader's done channel (false).
func (m *proofMemo) claim() (chan struct{}, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.inFlight {
		return m.done, false
	}
	m.inFlight = true
	m.done = make(chan struct{})
	return m.done, true
}

// store publishes the leader's result, starts the TTL window, and wakes
// every waiter on the leader's done channel.
func (m *proofMemo) store(p FreshnessProof, ttl time.Duration) {
	m.mu.Lock()
	m.proof = p
	m.until = time.Now().Add(ttl)
	m.inFlight = false
	ch := m.done
	m.done = nil
	m.mu.Unlock()
	if ch != nil {
		close(ch)
	}
}

// proofCacheTTL bounds how long a memoized non-strict freshness verdict is
// re-served before the next call recomputes it.
// KERN_FRESHNESS_PROOF_CACHE_MS overrides (0 disables memoization).
func proofCacheTTL() time.Duration {
	if v := os.Getenv("KERN_FRESHNESS_PROOF_CACHE_MS"); v != "" {
		if ms, err := strconv.Atoi(v); err == nil {
			if ms <= 0 {
				return 0
			}
			return time.Duration(ms) * time.Millisecond
		}
	}
	return 5 * time.Second
}

// NoteFreshness records a verdict the caller just decided from a live walk
// (an Update or Build), so later consumers in this process — the index stamp,
// a second Stale check — reuse it instead of re-proving freshness. Bounded by
// the same TTL as the proof memo; a no-op when the memo is disabled.
func (ix *Index) NoteFreshness(v FreshnessVerdict) {
	if ix == nil {
		return
	}
	ttl := proofCacheTTL()
	if ttl <= 0 {
		return
	}
	p := FreshnessProof{CheckedAt: time.Now().UTC(), Verdict: v}
	if ix.Identity != nil {
		p.Recorded = *ix.Identity
	}
	ix.proofMemo().store(p, ttl)
}

// FreshnessProof verifies that the on-disk tree at root still matches the
// identity recorded when the index was built. Non-strict mode takes the cheap
// git fast path (working-tree tree OID compare) and only falls back to a full
// content re-hash when git cannot vouch for the tree. Verdicts:
//
//   - unknown: nil index or nil Identity (no baseline to compare against).
//   - fresh:   git sees no change, or the re-hashed content root matches.
//   - stale:   the re-hashed content root no longer matches.
//
// See FreshnessProofStrict for the mode that always re-hashes.
//
// M#10/M#13 (2026-10-24): the non-strict proof is memoized per index
// instance — the last verdict is re-served for proofCacheTTL() and
// concurrent callers share one in-flight computation. A warm proof costs
// a git staging pass or a stat-only tree walk (hundreds of ms to seconds
// on large repos), which every index-backed tool call previously paid per
// call; the verdict is advisory metadata (provenance stamps, staleness
// notes), so serving one up to proofCacheTTL() old stays within the
// semantics the snapshot gate already accepts (gateTTL, 5 min since the
// 2026-10-27 idle-tax fix; was 30 s). Set
// KERN_FRESHNESS_PROOF_CACHE_MS=0 to disable. FreshnessProofStrict is
// never memoized.
func (ix *Index) FreshnessProof(root string) FreshnessProof {
	ttl := proofCacheTTL()
	if ttl <= 0 || root != ix.Root {
		return ix.computeFreshnessProof(root)
	}
	memo := ix.proofMemo()
	if p, ok := memo.serve(); ok {
		return p
	}
	if ch, leader := memo.claim(); !leader {
		<-ch
		if p, ok := memo.serve(); ok {
			return p
		}
		// The flight finished without a stored result; compute directly.
		return ix.computeFreshnessProof(root)
	}
	p := ix.computeFreshnessProof(root)
	memo.store(p, ttl)
	return p
}

// staleMuRef returns the index's stale-snapshot mutex, allocating it on first
// use. It is a pointer so copying an Index value (snapshot encoding, tests)
// never copies a lock.
func (ix *Index) staleMuRef() *sync.Mutex {
	if ix.staleMu == nil {
		proofMemoInit.Lock()
		if ix.staleMu == nil {
			ix.staleMu = &sync.Mutex{}
		}
		proofMemoInit.Unlock()
	}
	return ix.staleMu
}

// proofMemoInit guards the lazy allocation of the per-index freshness memo so
// a zero-value Index used concurrently still gets exactly one memo.
var proofMemoInit sync.Mutex

// proofMemo returns the index's freshness memo, allocating it on first use.
func (ix *Index) proofMemo() *proofMemo {
	if ix.freshnessMemo == nil {
		proofMemoInit.Lock()
		if ix.freshnessMemo == nil {
			ix.freshnessMemo = &proofMemo{}
		}
		proofMemoInit.Unlock()
	}
	return ix.freshnessMemo
}

// computeFreshnessProof is the uncached non-strict proof computation.
func (ix *Index) computeFreshnessProof(root string) FreshnessProof {
	// Stat-gate first: a retained content walk that is recent and still
	// stat-current decides the verdict with stats only. This must run BEFORE
	// freshnessBaseline: on a dirty working tree the baseline's git tree-OID
	// probe costs ~150ms on a medium repo, while the stat walk is a few ms —
	// and the gate's verdict would ignore the probe anyway.
	if proof, ok := ix.statGateProof(root); ok {
		return proof
	}
	proof, ok := ix.freshnessBaseline(root)
	if !ok {
		// No baseline: no content walk ran, so no observation is retained.
		ix.clearStaleSnapshot()
		return proof // unknown
	}
	// Fast path: git's working-tree OID is unchanged, so no indexed file
	// changed — done without a content re-walk. This catches mtime-preserving
	// edits (git apply) that the stat gate cannot.
	if proof.Recorded.TreeOID != "" && proof.Recorded.TreeOID == proof.Current.TreeOID {
		// Fresh without a content walk: drop any earlier observation so the
		// retained snapshot always matches the walk the last verdict came from.
		ix.clearStaleSnapshot()
		proof.Verdict = FreshnessFresh
		return proof
	}
	return ix.finishFreshness(root, proof)
}

// FreshnessProofStrict always recomputes the content root (a full re-hash of
// every indexable file), so it also covers changes git cannot see — files
// outside the git tree, .kernignore-only exclusions, dirty smudge/clean
// filters — at the cost of a tree walk. `kern index --status --strict` uses
// this.
//
// Provenance contract: the verdict comes SOLELY from the content walk. The
// git commit is observed concurrently as provenance only; Current.TreeOID is
// LAZY — it is computed ONLY when the walk FAILS and git must be trusted as
// the fallback (recorded vs current tree OID compare). On a successful walk
// Current.TreeOID stays "" and is omitted from the emitted JSON (omitempty),
// so the strict re-verify path never pays for the git tree-OID slow path
// (which stages the whole working tree into a throwaway index). Recorded
// TreeOID is unaffected: it is still recorded at build time, and the warm
// TreeOIDProbe freshness check depends on it.
func (ix *Index) FreshnessProofStrict(root string) FreshnessProof {
	proof := FreshnessProof{CheckedAt: time.Now().UTC()}
	if ix == nil || ix.Identity == nil {
		proof.Verdict = FreshnessUnknown
		return proof
	}
	proof.Recorded = *ix.Identity
	var (
		wg      sync.WaitGroup
		commit  string
		cur     map[string]string
		walkErr error
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		// Git commit is provenance only; the verdict never depends on it.
		if out, err := runGit(root, "rev-parse", "--short", "HEAD"); err == nil {
			commit = out
		}
	}()
	go func() {
		defer wg.Done()
		cur, walkErr = indexableHashes(root, ignore.Load(root))
	}()
	wg.Wait()
	proof.Current = IndexIdentity{BuiltAt: time.Now().UTC(), GitCommit: commit}
	if walkErr != nil {
		// Walk failed: compute the tree OID lazily and trust git when it
		// vouches for the tree (recorded == current OID).
		proof.Current.TreeOID = treeOID(root)
		if proof.Recorded.TreeOID != "" && proof.Recorded.TreeOID == proof.Current.TreeOID {
			proof.Verdict = FreshnessFresh // trust git
			return proof
		}
		proof.Verdict = FreshnessUnknown
		return proof
	}
	proof.Current.ContentRoot = aggregateHash(cur)
	if proof.Current.ContentRoot == proof.Recorded.ContentRoot {
		proof.Verdict = FreshnessFresh
		return proof
	}
	proof.Verdict = FreshnessStale
	return proof
}

// StaleWithProof runs the Stale() decision and returns the freshness proof it
// was based on, so callers that need both the verdict and the proof (e.g.
// `kern guard check`, whose JSON output carries the provenance) make ONE
// observation pass instead of computing the proof twice. The boolean is
// exactly what Stale() returns.
func (ix *Index) StaleWithProof(root string) (bool, FreshnessProof) {
	if ix == nil || len(ix.FileHashes) == 0 {
		return true, FreshnessProof{CheckedAt: time.Now().UTC(), Verdict: FreshnessUnknown}
	}
	if ix.Identity == nil {
		// Same defensive legacy path as Stale(); no proof baseline exists.
		return ix.legacyStale(), FreshnessProof{CheckedAt: time.Now().UTC(), Verdict: FreshnessUnknown}
	}
	proof := ix.FreshnessProof(ix.Root)
	return proof.Stale(), proof
}

// statGateProof re-serves a retained walk's verdict when the tree has not
// changed since (mtime+size+file-set), without any git call or content read.
// Current is left minimal (the gate proves "unchanged since Recorded", not a
// fresh content identity); callers that need git provenance run it themselves.
func (ix *Index) statGateProof(root string) (FreshnessProof, bool) {
	if ix == nil || ix.Identity == nil {
		return FreshnessProof{}, false
	}
	snap := ix.retainedSnapshot()
	if snap == nil || time.Since(snap.VerifiedAt) >= gateTTL() {
		return FreshnessProof{}, false
	}
	if !snapshotStillCurrent(root, ignore.LoadCached(root), snap) {
		return FreshnessProof{}, false
	}
	verdict := FreshnessStale
	if snap.WasFresh {
		verdict = FreshnessFresh
	}
	return FreshnessProof{
		CheckedAt: time.Now().UTC(),
		Recorded:  *ix.Identity,
		Current:   IndexIdentity{BuiltAt: time.Now().UTC()},
		Verdict:   verdict,
	}, true
}

// freshnessBaseline fills the proof's Recorded/Current identities and reports
// whether a baseline exists at all. ok=false (nil index or nil Identity)
// yields an "unknown" proof.
func (ix *Index) freshnessBaseline(root string) (FreshnessProof, bool) {
	proof := FreshnessProof{CheckedAt: time.Now().UTC()}
	if ix == nil || ix.Identity == nil {
		proof.Verdict = FreshnessUnknown
		return proof, false
	}
	proof.Recorded = *ix.Identity
	cur := IndexIdentity{BuiltAt: time.Now().UTC()}
	// Cheap OID only: treeOIDFast is two git queries on a clean tree and ""
	// on a dirty one. The full treeOID's slow path stages the ENTIRE working
	// tree into a throwaway index (git add -A), which costs hundreds of ms —
	// and on a dirty tree the resulting OID differs from the recorded one
	// anyway, so the verdict falls through to the content walk and the staged
	// OID is discarded. That made every staleness check on a dirty tree (the
	// common case while developing) pay the staging for nothing. The decisive
	// OID is computed lazily by finishFreshness only when the walk FAILS and
	// git must be trusted as the fallback (same laziness contract as
	// FreshnessProofStrict).
	cur.TreeOID = treeOIDFast(root)
	if out, err := runGit(root, "rev-parse", "--short", "HEAD"); err == nil {
		cur.GitCommit = out
	}
	proof.Current = cur
	return proof, true
}

// finishFreshness resolves the verdict by recomputing the content root from
// the live tree. On recompute failure it trusts git when git vouches for the
// tree, and returns "unknown" when neither check can decide.
//
// The content walk's per-file observation (hashes + mtimes) is retained on ix
// (retainStaleSnapshot) for a following UpdateWithSnapshot: this is the walk
// the staleness verdict is based on, and its hashes are the ones the update
// must reuse. On failure no observation is retained.
// gateTTL is how long a retained content walk may serve stat-gated
// staleness verdicts before a full re-verification walk is forced. Every
// serve re-validates the observation with a stat-only walk (same file set,
// mtimes, sizes), so a normal edit is caught on the very next check; the
// TTL only bounds how long the walk's VERDICT may be re-served — the
// defense-in-depth window for same-size mtime-preserving edits (content
// changed, mtime+size identical), which stats can never see and only the
// git tree-OID probe / content walk can catch. Agent tool bursts stay
// fully gated either way; the bound is what an idle or slow cadence
// re-pays (~0.6–0.7 s of git+content pass on a dirty tree, measured
// 2026-10-27) per window. 30 s made that cost hit the first query after
// every thinking/editing pause — the common agent cadence — so the window
// is now 5 min: one re-witness per window instead of one per pause.
// KERN_FRESHNESS_GATE_MS overrides (0 disables the gate entirely).
func gateTTL() time.Duration {
	if v := os.Getenv("KERN_FRESHNESS_GATE_MS"); v != "" {
		if ms, err := strconv.Atoi(v); err == nil {
			return time.Duration(ms) * time.Millisecond
		}
	}
	return 5 * time.Minute
}

func (ix *Index) finishFreshness(root string, proof FreshnessProof) FreshnessProof {
	// Stat gate: a recent retained walk re-serves its verdict while a stat-only
	// walk proves nothing changed (same file set, mtimes and sizes). WasFresh
	// picks which verdict: an unchanged tree over a mismatching walk is STALE.
	// Same-size mtime-preserving edits evade it until the TTL; the strict and
	// clean-tree git-OID paths still catch those.
	if snap := ix.retainedSnapshot(); snap != nil && time.Since(snap.VerifiedAt) < gateTTL() && snapshotStillCurrent(root, ignore.LoadCached(root), snap) {
		if snap.WasFresh {
			proof.Verdict = FreshnessFresh
		} else {
			proof.Verdict = FreshnessStale
		}
		return proof
	}
	obs, err := indexableHashesObserved(root, ignore.LoadCached(root))
	if err != nil {
		// The walk failed and freshnessBaseline left Current.TreeOID lazy
		// (empty on dirty/unavailable trees): only now is the expensive
		// full staging form of the OID worth computing, because git is the
		// only remaining witness.
		ix.clearStaleSnapshot()
		if proof.Current.TreeOID == "" {
			proof.Current.TreeOID = treeOID(root)
		}
		if proof.Recorded.TreeOID != "" && proof.Recorded.TreeOID == proof.Current.TreeOID {
			proof.Verdict = FreshnessFresh // trust git
			return proof
		}
		proof.Verdict = FreshnessUnknown
		return proof
	}
	proof.Current.ContentRoot = aggregateHash(obs.hashes)
	ix.retainStaleSnapshot(obs, proof.Current.ContentRoot == proof.Recorded.ContentRoot)
	if proof.Current.ContentRoot == proof.Recorded.ContentRoot {
		proof.Verdict = FreshnessFresh
		return proof
	}
	proof.Verdict = FreshnessStale
	return proof
}
