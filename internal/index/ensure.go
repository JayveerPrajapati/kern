package index

// TreeOIDProbe is the tri-state git tree-OID freshness probe: a git
// tree-OID comparison ONLY, with no content walk — git hashes file content,
// so any content edit (including an mtime-preserving one, e.g. `git apply`)
// changes the tree OID and flips the verdict. Callers can distinguish
// "decisively stale" from "no baseline / git unavailable":
//
//   - fresh=true, decided=true: the recorded TreeOID matches the current
//     working-tree OID → the index is fresh.
//   - fresh=false, decided=true: a recorded TreeOID exists and the current
//     OID differs → DECISIVELY stale → rebuild.
//   - decided=false: no baseline to compare (nil index, nil Identity, or an
//     empty/legacy TreeOID) OR the current OID could not be computed (root
//     is not a git worktree, git unavailable) → inconclusive → the caller
//     must fall back to a content check before deciding to rebuild.
//
// err is reserved for plumbing failures; with the current implementation it
// is always nil (both unprovable cases are reported via decided=false).
func (ix *Index) TreeOIDProbe(root string) (fresh, decided bool, err error) {
	if ix == nil || ix.Identity == nil || ix.Identity.TreeOID == "" {
		return false, false, nil // no baseline: inconclusive
	}
	cur := treeOID(root)
	if cur == "" {
		return false, false, nil // current OID unavailable: inconclusive
	}
	return cur == ix.Identity.TreeOID, true, nil
}

// TreeOIDMatches is the boolean convenience wrapper over TreeOIDProbe: it
// reports fresh only when the probe is BOTH fresh and decided, and false
// (meaning "cannot prove fresh") for the unprovable cases — no baseline to
// compare (nil index, nil Identity, or an empty/legacy TreeOID) or the
// current tree OID cannot be computed (root is not a git worktree, git
// unavailable). Callers that cannot act on "inconclusive" treat that as
// stale and fall through to the rebuild path; the strict re-verify after a
// rebuild is the trust anchor, so a false negative here only costs an
// unnecessary update, never a silent pass on a potentially-misleading index.
func (ix *Index) TreeOIDMatches(root string) (bool, error) {
	fresh, decided, err := ix.TreeOIDProbe(root)
	if err != nil {
		return false, err
	}
	return fresh && decided, nil
}
