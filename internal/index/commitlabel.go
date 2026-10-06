package index

import (
	"encoding/json"
	"log"
)

// RefreshCommitLabel opportunistically refreshes the recorded git_commit
// provenance label on a content-fresh index when HEAD has advanced past the
// build-time commit — e.g. a seal-only commit that moved HEAD without
// changing any indexed file's content (the V7 scenario: the content proof
// says fresh, but the recorded label still names the build-time commit).
//
// The label is provenance, NOT a staleness verdict: tree_oid, content_root
// and built_at are deliberately left at their build-time values (built_at
// keeps meaning "when the content was indexed"), and only the git_commit
// string is rewritten. The write is a cheap meta update — never a rebuild,
// never a re-walk of the tree. A genuinely stale index is left untouched so
// the normal rebuild path owns it (a rebuild records the new commit at build
// time anyway).
//
// The in-memory ix.Identity is updated too, so the same process sees the new
// label immediately. Returns whether the persisted label changed.
func RefreshCommitLabel(root string, ix *Index) (bool, error) {
	if ix == nil || ix.Identity == nil {
		return false, nil // nothing built (or no identity) — nothing to refresh
	}
	head, err := runGit(root, "rev-parse", "--short", "HEAD")
	if err != nil || head == "" {
		return false, nil // not a git worktree (or git unavailable) — nothing to do
	}
	if ix.Identity.GitCommit == head {
		return false, nil // label already current — idempotent no-op
	}
	// Only refresh when the content is fresh: a stale index must fall
	// through to the normal rebuild path untouched.
	if ix.FreshnessProof(root).Verdict != FreshnessFresh {
		return false, nil
	}
	ix.Identity.GitCommit = head
	if err := ix.persistIdentityLabel(); err != nil {
		return false, err
	}
	return true, nil
}

// refreshCommitLabelIfFresh is the verdict-sharing variant for callers that
// already computed a freshness proof (StatusReport, DiskIndexView): the
// caller's single proof decides, so no second tree walk happens. fresh must
// come from a proof computed on the same loaded ix.
func refreshCommitLabelIfFresh(root string, ix *Index, fresh bool) (bool, error) {
	if !fresh || ix == nil || ix.Identity == nil {
		return false, nil
	}
	head, err := runGit(root, "rev-parse", "--short", "HEAD")
	if err != nil || head == "" {
		return false, nil
	}
	if ix.Identity.GitCommit == head {
		return false, nil // label already current — idempotent no-op
	}
	ix.Identity.GitCommit = head
	if err := ix.persistIdentityLabel(); err != nil {
		return false, err
	}
	return true, nil
}

// persistIdentityLabel writes the in-memory identity (with the refreshed
// git_commit label) to the persisted store WITHOUT re-indexing. SQLite
// (default build): a single upsert on the meta table — the same key Save
// writes. JSON fallback (nosqlite build, or a SQLite write failure): the
// identity is embedded in index.json, so the same in-memory index is
// re-persisted via Save — a serialization of unchanged content, never a
// rebuild.
func (ix *Index) persistIdentityLabel() error {
	if SQLiteEnabled() {
		s, err := OpenSQLite(ix.Root)
		if err != nil {
			return err
		}
		defer func() { _ = s.Close() }()
		data, err := json.Marshal(ix.Identity)
		if err != nil {
			return err
		}
		if err := s.SetMeta("identity", string(data)); err == nil {
			return nil
		} else {
			// Fall back to the JSON cache exactly like Save does.
			log.Printf("kern index: identity meta update failed for %s, falling back to full persist: %v", ix.Root, err)
		}
	}
	// JSON (or SQLite-failure) fallback: re-persist the same in-memory
	// index — the label is refreshed, nothing is rebuilt.
	return ix.Save()
}

// SetMeta upserts a single meta row without touching symbols — the cheap
// meta-only write path used to refresh the identity's git_commit label
// without a rebuild. SQLite serialises writers under WAL, so this is safe
// against concurrent Save calls.
func (s *SQLiteStore) SetMeta(key, value string) error {
	_, err := s.db.Exec(
		"INSERT INTO meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value",
		key, value)
	return err
}
