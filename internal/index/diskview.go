package index

import (
	"log"
	"os"
	"time"
)

// DiskIndexView summarizes the persisted index for a root, or nil when none
// exists yet (a normal first-run state). Unreadable or schema-mismatched
// indexes are reported as "rebuild required" — never as silent zeroes. The
// freshness verdict is the content-aware loose proof — the same decision
// `kern index --status` makes. FreshnessProof takes the cheap git tree-OID
// fast path when the recorded and current OIDs match, and falls through to a
// content re-hash of the indexed files when the tree moved. The raw
// TreeOIDProbe verdict is deliberately NOT the final word: a commit that only
// seals already-indexed content (or touches a .kernignore'd / non-indexed
// file) flips the git tree OID without changing any indexed file's content,
// and must not flag the index stale. It is the authoritative `index` block of
// `kern health`, shared by the CLI (kern health) and the MCP health handler
// so both surfaces report the persisted state instead of an empty in-memory
// session cache.
func DiskIndexView(root string) map[string]any {
	// The primary store is SQLite in the default build; the JSON cache is
	// the fallback (nosqlite build, or a legacy repo not yet migrated).
	store := StorePath(root)
	if SQLiteEnabled() {
		store = SQLitePath(root)
	}
	if _, err := os.Stat(store); err != nil {
		if SQLiteEnabled() {
			// SQLite-primary build still serves a legacy JSON-only cache;
			// fall back to the JSON path before declaring "nothing persisted".
			if _, jerr := os.Stat(StorePath(root)); jerr != nil {
				return nil
			}
			store = StorePath(root)
		} else {
			return nil // nothing persisted yet
		}
	}
	ix, err := Load(root)
	if err != nil {
		return map[string]any{"root": root, "version": 0, "built": false, "fresh": false, "stale": true, "rebuild_required": err.Error()}
	}
	if ix == nil {
		return nil
	}
	// Single source of truth: the content-aware loose proof (the same
	// decision `kern index --status` makes). FreshnessProof internally runs
	// the cheap git tree-OID fast path when the recorded and current OIDs
	// match, and re-hashes the indexed files only when the tree moved — so a
	// commit that changes the git tree OID without changing any indexed
	// file's content is still judged fresh. An unknown/inconclusive proof
	// still renders as stale (fail-closed), matching the previous "decided"
	// semantics.
	verdict := "unknown"
	proof := ix.FreshnessProof(root)
	// Opportunistic label self-heal: when the content is fresh but HEAD has
	// advanced past the build-time commit, refresh the recorded git_commit
	// label (provenance, no rebuild). Best-effort — a failure never changes
	// the verdict. Reuses the proof above, so no second tree walk.
	if proof.Verdict == FreshnessFresh {
		if _, err := refreshCommitLabelIfFresh(root, ix, true); err != nil {
			log.Printf("kern health: refresh git_commit label for %s: %v", root, err)
		}
	}
	switch proof.Verdict {
	case FreshnessFresh:
		verdict = "fresh"
	case FreshnessStale:
		verdict = "stale"
	}
	return map[string]any{
		"root":       root,
		"built":      true,
		"fresh":      verdict == "fresh",
		"stale":      verdict != "fresh",
		"verdict":    verdict,
		"version":    ix.Version,
		"symbols":    len(ix.Symbols),
		"files":      len(ix.FileHashes),
		"packages":   len(ix.Pkgs),
		"languages":  ix.Languages(),
		"store":      store,
		"updated_at": ix.UpdatedAt.Format(time.RFC3339),
	}
}
