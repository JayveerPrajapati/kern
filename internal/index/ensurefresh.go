package index

import (
	"fmt"
	"log"
	"time"
)

// IndexStatus is a JSON-ready snapshot of a cached index's health, mirroring
// the fields `kern index --status [--json]` reports. The JSON field names are
// a CLI contract (`kern index --status --json` / `ensure-fresh --json`) and
// must not change.
type IndexStatus struct {
	Root              string            `json:"root"`
	SchemaVersion     string            `json:"schema_version"`
	Built             bool              `json:"built"`
	Symbols           int               `json:"symbols"`
	Files             int               `json:"files"`
	Packages          int               `json:"packages"`
	Version           int               `json:"version"`
	Stale             bool              `json:"stale"`
	Languages         []string          `json:"languages"`
	Store             string            `json:"store"`
	PrecisionByLang   map[string]string `json:"precision_by_lang,omitempty"`
	FreshnessProof    FreshnessProof    `json:"freshness_proof,omitempty"`
	IndexIdentity     *IndexIdentity    `json:"index_identity,omitempty"`
	TreeSitterEnabled bool              `json:"tree_sitter_enabled"`
	SQLite            bool              `json:"sqlite_enabled"`
	// CallResolution reports distinct callee targets vs how many fail to
	// resolve (external stdlib/vendor and untyped dynamic-language targets
	// are expected and correct to stay unresolved). Zero on indexes built
	// before the pass existed.
	CallResolution CallResStats `json:"call_resolution,omitempty"`
}

// EnsureFreshResult is the JSON-ready outcome of EnsureFresh: the freshness
// verdict plus the standard IndexStatus-shaped payload (the same shape `kern
// index --status --json` emits, with a top-level "freshness" field added).
// The embedded IndexStatus carries the trust-anchor proof: for "rebuilt" it
// is the genuine post-save observation from the StatusReport call.
type EnsureFreshResult struct {
	Freshness string `json:"freshness"`
	IndexStatus
}

// defaultRoot returns root, defaulting an empty root to ".". This is the
// index-local variant: it does NOT absolutize (unlike mcp/root.ResolveRoot
// and the internal/config variant) because index cannot import internal/mcp
// (layer rules) and its callers already work relative to cwd.
func defaultRoot(root string) string {
	if root == "" {
		return "."
	}
	return root
}

// BuildPersisted builds (or refreshes) the symbol index for root and persists
// it like `kern index` so StatusReport and later loads observe the build in
// other processes. Save is SQLite-primary when the store is compiled in
// (default build) and falls back to the JSON cache under -tags nosqlite or on
// a SQLite write failure, so the store stays in sync either way.
//
// The rebuild is debounced across processes (M3): a concurrent kern process
// that just rebuilt the same root is waited for and its fresh index reused,
// so `kern index` storms converge on one rebuild. A caller that explicitly
// wants a rebuild regardless (--force) still gets a correct, fresh index —
// never a stale one — because the re-check under the lock re-validates
// freshness before reuse.
func BuildPersisted(root string) (*Index, error) {
	root = defaultRoot(root)
	return debouncedRebuild(root,
		func() (*Index, bool) {
			ix, err := Load(root)
			if err != nil || ix == nil {
				return nil, false
			}
			return ix, !ix.Stale()
		},
		func(prev *Index) (*Index, error) {
			ix, err := Build(root)
			if err != nil {
				return nil, err
			}
			// Persist like `kern index` so Status/Load observe the build in
			// other processes; Save writes the SQLite-primary store (JSON
			// fallback).
			if err := ix.Save(); err != nil {
				return nil, err
			}
			return ix, nil
		})
}

// BuildPersistedForce rebuilds and persists the index for root
// UNCONDITIONALLY, bypassing the cross-process debounce. This is the
// `kern index --force` corruption-recovery / schema-bump escape hatch: a
// fresh cached index is NOT reused — the tree is always re-parsed.
func BuildPersistedForce(root string) (*Index, error) {
	root = defaultRoot(root)
	ix, err := Build(root)
	if err != nil {
		return nil, err
	}
	// Persist like `kern index` so Status/Load observe the build in other
	// processes; Save writes the SQLite-primary store (JSON fallback).
	if err := ix.Save(); err != nil {
		return nil, err
	}
	return ix, nil
}

// StatusReport reports the cached index's health for root. It is read-only
// with one narrow exception: when the index is content-fresh but HEAD has
// advanced past the build-time commit, the recorded git_commit provenance
// label is refreshed in the persisted meta (a cheap single-row write, never
// a rebuild) so the label self-heals on any `kern index --status`. strict
// selects a full content re-hash freshness proof over the fast git tree-OID
// compare.
func StatusReport(root string, strict bool) (*IndexStatus, error) {
	root = defaultRoot(root)
	status := &IndexStatus{
		Root:              root,
		SchemaVersion:     "2",
		Built:             false,
		Stale:             true,
		Languages:         []string{},
		PrecisionByLang:   map[string]string{},
		TreeSitterEnabled: TreesitterEnabled(),
		SQLite:            SQLiteEnabled(),
	}
	ix, err := Load(root)
	if err != nil || ix == nil {
		return status, nil // not built yet — not an error, just a fact
	}
	status.Built = true
	status.Symbols = len(ix.Symbols)
	status.Files = len(ix.FileHashes)
	status.Packages = len(ix.Pkgs)
	status.Version = ix.Version
	status.CallResolution = ix.CallResolution
	// Single proof computation. This used to derive the staleness decision
	// up to three times — ix.Stale() computes a FreshnessProof internally,
	// then the proof was computed again, then --strict recomputed it strictly
	// — the same logical freshness observation re-derived 3x, each costing a
	// full tree walk and/or git tree-OID staging (measured ~0.7-1.1s each on
	// a medium repo, dominating `kern index --status` latency). Exactly one
	// proof now, matching the requested strictness; `stale` is derived from
	// it with the documented contract intact: stale is false exactly when the
	// (possibly strict) verdict is "fresh", and an empty index is always
	// stale. The mtime count gate Stale() used to run first is subsumed: any
	// file addition/removal changes the content root, so the proof verdict
	// rejects it too.
	var proof FreshnessProof
	if strict {
		proof = ix.FreshnessProofStrict(root)
	} else {
		proof = ix.FreshnessProof(root)
	}
	// Opportunistic label self-heal (no rebuild): when the content is fresh
	// but HEAD has advanced past the build-time commit (a seal-only commit),
	// refresh the recorded git_commit label in the persisted meta so every
	// surface shows the current commit while the content proof still says
	// fresh. This is provenance, not a staleness decision, and it reuses the
	// proof computed above — no second tree walk. A stale index is left for
	// the normal rebuild path.
	if !proof.Stale() {
		if changed, err := refreshCommitLabelIfFresh(root, ix, true); err == nil && changed {
			// proof.Recorded was copied from ix.Identity before the refresh;
			// keep the emitted proof consistent with the refreshed label.
			proof.Recorded.GitCommit = ix.Identity.GitCommit
		}
	}
	status.FreshnessProof = proof
	status.Stale = len(ix.FileHashes) == 0 || proof.Stale()
	status.Languages = ix.Languages()
	status.PrecisionByLang = ix.PrecisionByLang
	if status.SQLite {
		status.Store = SQLitePath(root)
	} else {
		status.Store = StorePath(root)
	}
	if ix.Identity != nil {
		status.IndexIdentity = ix.Identity
	}
	return status, nil
}

// EnsureFresh consolidates the probe → update → re-verify freshness sequence
// into ONE call: it loads the cached index, cheaply checks staleness with a
// tri-state git tree-OID probe (no content walk), and returns "fresh" when
// the probe is decisively fresh. When the probe is inconclusive (legacy index
// without a recorded tree OID, or git unavailable) it runs the LOOSE content
// proof first and returns "fresh" when that matches, so a rebuild is only
// issued when the tree is decisively stale or the content proof disagrees.
// On rebuild it updates (or full-builds when no index loads), then strictly
// re-verifies from disk. Freshness is "fresh" (no rebuild), "rebuilt"
// (converged), or "stale" (fail-closed: the index did not converge after a
// rebuild).
//
// The rebuild path is debounced across processes (M3): a concurrent
// ensure-fresh invocation whose rebuild finished while we waited is detected
// by the under-lock re-probe and reused — its index was already re-verified
// before its own save — so concurrent invocations share one rebuild.
func EnsureFresh(root string) (*EnsureFreshResult, error) {
	root = defaultRoot(root)

	// freshProbe mirrors steps 1-2 (below): load the cached index and run the
	// tri-state freshness probe, returning "fresh" when the persisted index
	// is current. It is the under-lock re-check too, so a rebuild completed
	// by another process while we waited is reused, never redone.
	freshProbe := func() (*EnsureFreshResult, bool) {
		// 1. Load the cached index. An unloadable or missing index is not an
		// error — the full-build fallback below handles it (mirrors `kern
		// index --update`).
		prev, lerr := Load(root)

		// 2. Cheap git tree-OID fast path (no content walk). Git hashes
		// content, so an mtime-preserving edit (git apply) flips the OID
		// too.
		//   - fresh=true → the recorded TreeOID matches the current tree:
		//     return "fresh" immediately — no walk, no rebuild.
		//   - fresh=false (decided or not) → the tree OID differs or is
		//     unprovable. The tree-OID probe is a fast path, NOT a verdict:
		//     a commit that only seals already-indexed content (or touches
		//     a .kernignore'd / non-indexed file) flips the git tree OID
		//     without changing any indexed file's content. The LOOSE
		//     content proof is authoritative — it re-hashes the indexed
		//     files and only reports stale when the content root moved. A
		//     content match proves the index is current, so it returns
		//     "fresh" without rebuilding. Only a loose "stale" falls
		//     through to the rebuild path below.
		if lerr == nil && prev != nil {
			fresh, _, _ := prev.TreeOIDProbe(root)
			if fresh {
				// A seal-only commit can advance HEAD without changing any
				// indexed file's content (the recorded tree OID still matches
				// the current tree): refresh the recorded git_commit label so
				// this surface names the current commit too. Provenance only
				// — no rebuild, and the no-op case is a single git query.
				if _, err := RefreshCommitLabel(root, prev); err != nil {
					log.Printf("kern index: refresh git_commit label for %s: %v", root, err)
				}
				st := indexStatusFromIndex(root, prev, FreshnessProof{
					Verdict:   FreshnessFresh,
					Recorded:  *prev.Identity,
					CheckedAt: time.Now().UTC(),
				})
				return &EnsureFreshResult{Freshness: "fresh", IndexStatus: *st}, true
			}
			// The tree probe is not fresh — a decisive mismatch, or an
			// inconclusive one (legacy index without a TreeOID, non-git
			// worktree, git unavailable). The loose content proof decides,
			// exactly as `kern index --status` and the disk view do, so all
			// surfaces agree after a seal-only commit. Costs one walk, not
			// a rebuild.
			if st, err := StatusReport(root, false); err == nil && st.Built && st.FreshnessProof.Verdict == FreshnessFresh {
				return &EnsureFreshResult{Freshness: "fresh", IndexStatus: *st}, true
			}
			// Stale content proof (or no loadable status): fall through to
			// the rebuild path.
		}
		return nil, false
	}

	// Fast path: a fresh index is returned without the lock — the read-only
	// path stays lock-free (M3).
	if res, fresh := freshProbe(); fresh {
		return res, nil
	}

	// Stale (or unprovable): rebuild under the exclusive cross-process build
	// lock, debounced so concurrent invocations share one rebuild.
	return debouncedRebuild(root, freshProbe, func(prev *EnsureFreshResult) (*EnsureFreshResult, error) {
		// 3. Stale (or unprovable): run the incremental update over the
		// previous index — the same update-over-build pattern `kern index
		// --update` uses — falling back to a full build (which persists)
		// when no index loads or Update fails. Save persists via the
		// SQLite-primary store (default build), with the JSON cache as the
		// fallback for -tags nosqlite builds and SQLite write failures.
		var ix *Index
		if prev, lerr := Load(root); lerr == nil && prev != nil {
			if uix, uerr := Update(root, prev); uerr == nil && uix != nil {
				ix = uix
			}
		}
		if ix == nil {
			// No loadable previous index, or Update failed: full build. Save
			// directly rather than via BuildPersisted — that helper is
			// itself debounced and would wait on the lock we already hold.
			nix, berr := Build(root)
			if berr != nil {
				return nil, fmt.Errorf("ensure-fresh: build: %w", berr)
			}
			if serr := nix.Save(); serr != nil {
				return nil, fmt.Errorf("ensure-fresh: persist built index: %w", serr)
			}
		} else {
			if serr := ix.Save(); serr != nil {
				return nil, fmt.Errorf("ensure-fresh: persist updated index: %w", serr)
			}
		}

		// 4. Strict re-verify: a GENUINE fresh observation AFTER the save,
		// loaded from disk (StatusReport re-reads the persisted index). This
		// is the trust anchor — the pre-update probe's strictness was
		// deliberately redundant.
		if st, err := StatusReport(root, true); err == nil && st.Built && st.FreshnessProof.Verdict == FreshnessFresh {
			return &EnsureFreshResult{Freshness: "rebuilt", IndexStatus: *st}, nil
		}
		// 5. Loose re-verify: strict recomputes content_root over EVERY file
		// on disk, while a build records content for the parseable source
		// set only — a repo containing an unparseable file (e.g. a
		// compile-breaking fixture) never converges under strict. The loose
		// verdict is anchored to the git tree, which is exactly the content
		// the guard check evaluates.
		if st, err := StatusReport(root, false); err == nil && st.Built && st.FreshnessProof.Verdict == FreshnessFresh {
			return &EnsureFreshResult{Freshness: "rebuilt", IndexStatus: *st}, nil
		}

		// 6. Non-convergence: fail closed. The index is stale and did not
		// converge after a rebuild; callers must refuse to trust it.
		st, err := StatusReport(root, false)
		if err != nil {
			st = &IndexStatus{Root: root, SchemaVersion: "2", Built: false, Stale: true}
		}
		return &EnsureFreshResult{Freshness: "stale", IndexStatus: *st}, nil
	})
}

// indexStatusFromIndex populates the standard IndexStatus-shaped payload from
// an already-loaded index and a freshness proof. Used by EnsureFresh's
// no-rebuild "fresh" path; StatusReport re-loads the index itself so callers
// always observe disk state.
func indexStatusFromIndex(root string, ix *Index, proof FreshnessProof) *IndexStatus {
	status := &IndexStatus{
		Root:              root,
		SchemaVersion:     "2",
		Built:             true,
		Symbols:           len(ix.Symbols),
		Files:             len(ix.FileHashes),
		Packages:          len(ix.Pkgs),
		Version:           ix.Version,
		Stale:             proof.Stale(),
		Languages:         ix.Languages(),
		PrecisionByLang:   ix.PrecisionByLang,
		FreshnessProof:    proof,
		IndexIdentity:     ix.Identity,
		TreeSitterEnabled: TreesitterEnabled(),
		SQLite:            SQLiteEnabled(),
	}
	if status.SQLite {
		status.Store = SQLitePath(root)
	} else {
		status.Store = StorePath(root)
	}
	return status
}
