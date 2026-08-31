package index

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"sort"
	"strings"
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

// buildIdentity captures the content identity of an index at build time.
// ContentRoot is always populated; TreeOID/GitCommit are best-effort and
// empty when root is not a git worktree or git is unavailable.
func buildIdentity(root string, fileHashes map[string]string, builtAt time.Time) *IndexIdentity {
	id := &IndexIdentity{
		ContentRoot: aggregateHash(fileHashes),
		BuiltAt:     builtAt,
	}
	id.TreeOID = treeOID(root)
	if out, err := runGit(root, "rev-parse", "--short", "HEAD"); err == nil {
		id.GitCommit = out
	}
	return id
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
// A plain `git write-tree` only reflects the staged index, so an unstaged
// edit — exactly what `git apply` produces, mtime included — would be
// invisible to it, and the mtime-preserving-edit regression this identity
// exists to catch would sail through as "fresh". Instead we point
// GIT_INDEX_FILE at a throwaway index, stage the working tree into it, and
// write-tree from that. The repo's real index is never touched; the only side
// effect is a dangling tree/blob in the object database, which git gc reaps.
func treeOID(root string) string {
	tmp, err := os.CreateTemp("", "kern-treeoid-*")
	if err != nil {
		return ""
	}
	idxPath := tmp.Name()
	tmp.Close()
	// Remove the (empty) placeholder: git treats a pre-existing 0-byte index
	// file as corrupt ("index file smaller than expected") instead of as an
	// empty index, so let git create the file itself.
	if err := os.Remove(idxPath); err != nil {
		return ""
	}
	defer os.Remove(idxPath)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	env := append(os.Environ(), "GIT_INDEX_FILE="+idxPath)

	// Stage the whole working tree into the throwaway index. --ignore-errors
	// tolerates unreadable/locked files; .gitignore is honored exactly as the
	// index's own ignore policy honors it for gitignored paths. The .kern
	// directory is excluded explicitly: it is never indexed (ignoreDirs), and
	// because Save() writes .kern/index.json AFTER Build has captured the
	// identity, leaving it in the tree would make the check-time OID differ
	// from the build-time OID on every fresh repo, permanently defeating the
	// TreeOID fast path.
	add := exec.CommandContext(ctx, "git", "-C", root, "add", "-A", "--ignore-errors", "--", ".", ":(exclude).kern")
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
func (ix *Index) FreshnessProofStrict(root string) FreshnessProof {
	proof, ok := ix.freshnessBaseline(root)
	if !ok {
		return proof // unknown
	}
	return ix.finishFreshness(root, proof)
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
	cur.TreeOID = treeOID(root)
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
