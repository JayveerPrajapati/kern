package index

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
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
		gitPath := filepath.Join(dir, ".git")
		if _, err := os.Stat(gitPath); err == nil {
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
		h.Write([]byte(p))
		h.Write([]byte("="))
		h.Write([]byte(fileHashes[p]))
		h.Write([]byte("\n"))
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

	// Stage the whole working tree into the throwaway index. --ignore-errors
	// tolerates unreadable/locked files; git's ignore rules are honored
	// exactly as the index's own ignore policy honors them for gitignored
	// paths. .blueprint is excluded by an explicit pathspec (it is NOT
	// gitignored); .kern is excluded by git's own ignore rules instead — a
	// pathspec naming it makes `git add` exit 1, aborting the entire slow
	// path (see the doc comment above for the full explanation).
	add := exec.CommandContext(ctx, "git", "-C", root, "add", "-A", "--ignore-errors", "--", ".", ":(exclude).blueprint")
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
// does not exist; P0.2 keeps this package-local).
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
func (ix *Index) FreshnessProof(root string) FreshnessProof {
	proof, ok := ix.freshnessBaseline(root)
	if !ok {
		return proof // unknown
	}
	// Fast path: git's working-tree OID is unchanged, so no indexed file
	// changed — done without a content re-walk. This catches mtime-preserving
	// edits (git apply) that the stat gate cannot.
	if proof.Recorded.TreeOID != "" && proof.Recorded.TreeOID == proof.Current.TreeOID {
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
func (ix *Index) finishFreshness(root string, proof FreshnessProof) FreshnessProof {
	cur, err := indexableHashes(root, ignore.Load(root))
	if err != nil {
		// The walk failed and freshnessBaseline left Current.TreeOID lazy
		// (empty on dirty/unavailable trees): only now is the expensive
		// full staging form of the OID worth computing, because git is the
		// only remaining witness.
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
	proof.Current.ContentRoot = aggregateHash(cur)
	if proof.Current.ContentRoot == proof.Recorded.ContentRoot {
		proof.Verdict = FreshnessFresh
		return proof
	}
	proof.Verdict = FreshnessStale
	return proof
}
