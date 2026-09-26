package index

import (
	"os"
	"time"
)

// DiskIndexView summarizes the persisted index for a root, or nil when none
// exists yet (a normal first-run state). Unreadable or schema-mismatched
// indexes are reported as "rebuild required" — never as silent zeroes. The
// freshness verdict uses the same decision `kern index --status` makes: the
// cheap git tree-OID probe when decisive, and the loose content proof
// otherwise (non-git worktree, legacy index without a tree OID). It is the
// authoritative `index` block of `kern health`, shared by the CLI (kern
// health) and the MCP health handler so both surfaces report the persisted
// state instead of an empty in-memory session cache.
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
	verdict := "unknown"
	if fresh, decided, _ := ix.TreeOIDProbe(root); decided {
		if fresh {
			verdict = "fresh"
		} else {
			verdict = "stale"
		}
	} else {
		switch ix.FreshnessProof(root).Verdict {
		case FreshnessFresh:
			verdict = "fresh"
		case FreshnessStale:
			verdict = "stale"
		}
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
