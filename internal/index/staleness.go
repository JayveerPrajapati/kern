package index

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/cache"
)

// StalenessBanner spot-checks the files cited by a response against the
// content hashes recorded at build time and returns a warning banner when any
// of them changed since the index was built (CG-P0-2). It is the cheap
// per-response staleness gate: only the cited files are re-read and re-hashed
// — never the whole tree — so the cost is a few file reads regardless of repo
// size. Files the index does not cover (non-indexable, generated, ignored,
// or a nil/identity-less index) are treated as fresh: the index never claimed
// them, so they cannot be stale relative to it. A cited file that has
// disappeared since indexing is stale — it cannot be fresh.
//
// The returned banner is empty when every cited file is fresh. Callers
// prepend it verbatim to their rendered response; it is deliberately one
// line, so the token cost of the verdict stays near zero.
func (ix *Index) StalenessBanner(files []string) string {
	if ix == nil || len(ix.FileHashes) == 0 || len(files) == 0 {
		return ""
	}
	seen := map[string]bool{}
	var stale []string
	for _, f := range files {
		rel := ix.relPath(f)
		if rel == "" || seen[rel] {
			continue
		}
		seen[rel] = true
		if ix.fileStale(rel) {
			stale = append(stale, rel)
		}
	}
	if len(stale) == 0 {
		return ""
	}
	sort.Strings(stale)
	return fmt.Sprintf("⚠️ %d file(s) changed since index — kern health", len(stale))
}

// fileStale reports whether one relative file's current content hash differs
// from the hash recorded when the index was built. Files absent from
// FileHashes are never stale (the index made no claim about them); an
// unreadable or vanished file IS stale.
func (ix *Index) fileStale(rel string) bool {
	recorded, ok := ix.FileHashes[rel]
	if !ok {
		return false
	}
	data, err := readFile(filepath.Join(ix.Root, rel))
	if err != nil {
		return true
	}
	return cache.Hash(data) != recorded
}

// relPath normalizes a cited path (relative, or absolute under Root) to the
// slash-separated relative form FileHashes is keyed by. Returns "" for paths
// that cannot be expressed relative to the root (outside the tree, or a
// relative path escaping it).
func (ix *Index) relPath(f string) string {
	if f == "" {
		return ""
	}
	if filepath.IsAbs(f) {
		root, err := filepath.Abs(ix.Root)
		if err != nil {
			return ""
		}
		rel, err := filepath.Rel(root, filepath.Clean(f))
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return ""
		}
		return filepath.ToSlash(rel)
	}
	rel := filepath.ToSlash(filepath.Clean(f))
	if rel == ".." || strings.HasPrefix(rel, "../") {
		return ""
	}
	return rel
}
