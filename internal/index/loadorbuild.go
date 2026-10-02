package index

import (
	"context"
	"log"
)

// LoadOrBuild returns the project's symbol index: the persisted
// <root>/.kern/index.json when fresh, or a freshly built one (saved back
// for the next caller) when missing or stale. It is the canonical
// load-or-build shared by the CLI (cmd/kern) and the LSP server —
// previously two byte-identical private copies (blueprint duplication
// debt). project.Session.Index wraps the same reuse-while-fresh
// contract with session-level caching, a stale cooldown, and SQLite
// preference on top.
//
// Stale indexes are refreshed incrementally via Update whenever a previous
// index loads cleanly (Update re-parses only changed files); any failure
// falls back to a full Build. The explicit `kern index` command skips a
// rebuild when its fresh-skip check proves the persisted index current and
// only calls Build directly when the index is missing/stale or --force is
// given (cmd_index.go).
//
// The stale/missing path is debounced across processes (M3): concurrent kern
// invocations that detect the same staleness share ONE rebuild instead of
// each paying the full 12-16s cost. The rebuild runs under the exclusive
// cross-process build lock (.kern/locks/index-build.lock); waiters poll for
// the holder to finish, then re-check freshness and reuse the holder's
// index. The fast fresh-load path stays lock-free.
func LoadOrBuild(root string) (*Index, error) {
	return LoadOrBuildContext(context.Background(), root)
}

// LoadOrBuildContext is the context-aware form of LoadOrBuild. The context
// bounds the cross-process rebuild wait: when another process is rebuilding
// the index and our ctx is cancelled or expires, the wait aborts immediately
// with ctx.Err() instead of stalling for the full BuildLockWait budget.
func LoadOrBuildContext(ctx context.Context, root string) (*Index, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if ix, err := Load(root); err == nil && ix != nil {
		if !ix.Stale() {
			return ix, nil
		}
	}
	return debouncedRebuildContext(ctx, root,
		func() (*Index, bool) {
			ix, err := Load(root)
			if err != nil || ix == nil {
				return nil, false
			}
			// Stale() retains the content-walk observation on ix when the
			// verdict came from a content walk (M5a); the rebuild step below
			// reuses it via UpdateWithSnapshot so the tree is hashed once, not
			// once for staleness and again for the update.
			return ix, !ix.Stale()
		},
		func(prev *Index) (*Index, error) {
			// prev is the index the under-lock freshness check just loaded
			// and judged stale — the same walk that decided staleness, so its
			// retained hashes are exactly the ones Update must reuse. When
			// prev is nil (bounded wait exhausted without ever acquiring the
			// lock, so no check ran) load directly like the historical path;
			// that load has no retained snapshot, so UpdateWithSnapshot
			// degrades to a plain Update. Update is content-addressed, so an
			// index another process just built is reused verbatim and an
			// older one is re-parsed only where it differs from the current
			// tree.
			if prev == nil {
				if p, err := Load(root); err == nil && p != nil {
					prev = p
				}
			}
			if prev != nil {
				if ix, err := UpdateWithSnapshot(root, prev); err == nil && ix != nil {
					saveOrWarn(ix)
					return ix, nil
				}
			}
			ix, err := Build(root)
			if err != nil {
				return nil, err
			}
			saveOrWarn(ix)
			return ix, nil
		})
}

// saveOrWarn persists ix, logging loudly on failure: a silent skip means the
// next caller pays a full rebuild for no visible reason. Session's async save
// logs the same way (project.go); LoadOrBuild previously discarded the error
// entirely.
func saveOrWarn(ix *Index) {
	if err := ix.Save(); err != nil {
		log.Printf("kern index: could not persist index for %s (next caller rebuilds): %v", ix.Root, err)
	}
}
