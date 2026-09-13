package service

import (
	"context"
	"fmt"
	"time"

	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/project"
)

// IndexService centralizes symbol-index operations: building, loading,
// inspecting status, and watching for changes. It is the single entry point
// for every delivery mechanism that needs the code index.
type IndexService interface {
	// Build creates or refreshes the symbol index for root and returns it.
	Build(ctx context.Context, root string) (*index.Index, error)
	// Load reads the cached index for root without rebuilding. It returns the
	// index even when stale; callers decide whether freshness matters.
	Load(ctx context.Context, root string) (*index.Index, error)
	// LoadOrBuild returns the cached index when it is fresh, otherwise
	// rebuilds it. This is the behavior `kern graph`/`kern search` use.
	LoadOrBuild(ctx context.Context, root string) (*index.Index, error)
	// Status reports the cached index's health without mutating anything
	// (read-only, CI-safe). strict selects a full content re-hash freshness
	// proof over the fast git tree-OID compare.
	Status(ctx context.Context, root string, strict bool) (*IndexStatus, error)
	// EnsureFresh consolidates the probe → update → re-verify freshness
	// sequence into ONE call: it loads the cached index, cheaply checks
	// staleness with a tri-state git tree-OID probe (no content walk), and
	// returns "fresh" when the probe is decisively fresh. When the probe is
	// inconclusive (legacy index without a recorded tree OID, or git
	// unavailable) it runs the LOOSE content proof first and returns "fresh"
	// when that matches, so a rebuild is only issued when the tree is
	// decisively stale or the content proof disagrees. On rebuild it updates
	// (or full-builds when no index loads), then strictly re-verifies from
	// disk. Freshness is "fresh" (no rebuild), "rebuilt" (converged), or
	// "stale" (fail-closed: the index did not converge after a rebuild).
	EnsureFresh(ctx context.Context, root string) (*EnsureFreshResult, error)
	// Watch monitors root and re-indexes on change, invoking onChange with
	// each refresh. It blocks until ctx is cancelled; onError (when non-nil)
	// receives non-fatal watch errors.
	Watch(ctx context.Context, root string, interval time.Duration, onChange func(WatchEvent), onError func(error)) error
}

// IndexStatus is a JSON-ready snapshot of a cached index's health, mirroring
// the fields `kern index --status [--json]` reports.
type IndexStatus struct {
	Root              string               `json:"root"`
	SchemaVersion     string               `json:"schema_version"`
	Built             bool                 `json:"built"`
	Symbols           int                  `json:"symbols"`
	Files             int                  `json:"files"`
	Packages          int                  `json:"packages"`
	Version           int                  `json:"version"`
	Stale             bool                 `json:"stale"`
	Languages         []string             `json:"languages"`
	Store             string               `json:"store"`
	PrecisionByLang   map[string]string    `json:"precision_by_lang,omitempty"`
	FreshnessProof    index.FreshnessProof `json:"freshness_proof,omitempty"`
	IndexIdentity     *index.IndexIdentity `json:"index_identity,omitempty"`
	TreeSitterEnabled bool                 `json:"tree_sitter_enabled"`
	SQLite            bool                 `json:"sqlite_enabled"`
	// CallResolution reports distinct callee targets vs how many fail to
	// resolve (external stdlib/vendor and untyped dynamic-language targets
	// are expected and correct to stay unresolved). Zero on indexes built
	// before the pass existed.
	CallResolution index.CallResStats `json:"call_resolution,omitempty"`
}

// WatchEvent carries one re-index result from IndexService.Watch.
type WatchEvent struct {
	Changes []index.Change
	Index   *index.Index
}

// EnsureFreshResult is the JSON-ready outcome of EnsureFresh: the freshness
// verdict plus the standard IndexStatus-shaped payload (the same shape `kern
// index --status --json` emits, with a top-level "freshness" field added).
// The embedded IndexStatus carries the trust-anchor proof: for "rebuilt" it
// is the genuine post-save observation from the service Status call.
type EnsureFreshResult struct {
	Freshness string `json:"freshness"`
	IndexStatus
}

// indexService is the default IndexService implementation: a thin facade over
// the internal/index and internal/project engines.
type indexService struct{}

func newIndexService() *indexService { return &indexService{} }

func resolveRoot(root string) string {
	if root == "" {
		return "."
	}
	return root
}

func (s *indexService) Build(ctx context.Context, root string) (*index.Index, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root = resolveRoot(root)
	ix, err := index.Build(root)
	if err != nil {
		return nil, err
	}
	// Persist like `kern index` so Status/Load observe the build in other
	// processes and the SQLite store stays in sync when enabled.
	if err := ix.Save(); err != nil {
		return nil, err
	}
	if index.SQLiteEnabled() {
		if err := index.SaveSQLite(root, ix); err != nil {
			return nil, err
		}
	}
	return ix, nil
}

func (s *indexService) Load(ctx context.Context, root string) (*index.Index, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return index.Load(resolveRoot(root))
}

func (s *indexService) LoadOrBuild(ctx context.Context, root string) (*index.Index, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root = resolveRoot(root)
	// Connect-time catch-up: a cached index is never thrown away wholesale.
	// One hash-diff decides the cheapest reconciliation — serve the cache
	// when it already matches the tree, apply an incremental Update when the
	// change set is small, and only fall back to a full Build for a large
	// change set (where Update would re-parse nearly everything) or when no
	// cache exists at all.
	if ix, err := index.Load(root); err == nil && ix != nil {
		cur, herr := index.FileHashes(root)
		if herr != nil {
			// Unprovable freshness (scan error): fail closed with a full
			// rebuild rather than serving a possibly stale cache.
			cur = nil
		}
		if cur != nil {
			changes := len(index.Diff(ix.FileHashes, cur))
			if changes == 0 {
				// The cached index already matches the current tree: serve it
				// untouched.
				return ix, nil
			}
			if changes <= index.CatchUpMaxChanges {
				// Small change set: incremental catch-up re-parses only the
				// changed files. Persist like `kern index` so Status/Load observe
				// the refresh in other processes.
				if uix, uerr := index.Update(root, ix); uerr == nil && uix != nil {
					_ = uix.Save()
					return uix, nil
				}
				// Update failed (unloadable prev, parse error): fall through to
				// the full Build below.
			}
		}
	}
	// No loadable previous index, an unprovable scan, or a change set too
	// large for a cheap catch-up: full build.
	ix, err := s.Build(ctx, root)
	if err != nil {
		return nil, err
	}
	return ix, nil
}

func (s *indexService) Status(ctx context.Context, root string, strict bool) (*IndexStatus, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root = resolveRoot(root)
	status := &IndexStatus{
		Root:              root,
		SchemaVersion:     "2",
		Built:             false,
		Stale:             true,
		Languages:         []string{},
		PrecisionByLang:   map[string]string{},
		TreeSitterEnabled: index.TreesitterEnabled(),
		SQLite:            index.SQLiteEnabled(),
	}
	ix, err := index.Load(root)
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
	var proof index.FreshnessProof
	if strict {
		proof = ix.FreshnessProofStrict(root)
	} else {
		proof = ix.FreshnessProof(root)
	}
	status.FreshnessProof = proof
	status.Stale = len(ix.FileHashes) == 0 || proof.Stale()
	status.Languages = ix.Languages()
	status.PrecisionByLang = ix.PrecisionByLang
	if status.SQLite {
		status.Store = index.SQLitePath(root)
	} else {
		status.Store = index.StorePath(root)
	}
	if ix.Identity != nil {
		status.IndexIdentity = ix.Identity
	}
	return status, nil
}

func (s *indexService) Watch(ctx context.Context, root string, interval time.Duration, onChange func(WatchEvent), onError func(error)) error {
	root = resolveRoot(root)
	if interval <= 0 {
		interval = 5 * time.Second
	}
	return project.Watch(ctx, root, interval, func(changes []index.Change, ix *index.Index) {
		if onChange != nil {
			onChange(WatchEvent{Changes: changes, Index: ix})
		}
	}, onError)
}

// EnsureFresh implements IndexService.EnsureFresh. See the interface
// docstring for the contract; the orchestration mirrors the former
// probe → update → re-verify subprocess sequence exactly, but inside ONE
// call (Phase 2 consolidation: one subprocess instead of three/four).
func (s *indexService) EnsureFresh(ctx context.Context, root string) (*EnsureFreshResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root = resolveRoot(root)

	// 1. Load the cached index. An unloadable or missing index is not an
	// error — the full-build fallback below handles it (mirrors `kern index
	// --update`).
	prev, lerr := index.Load(root)

	// 2. Tri-state git tree-OID probe (no content walk). Git hashes content,
	// so an mtime-preserving edit (git apply) flips the OID too.
	//   - fresh=true, decided=true → the recorded TreeOID matches: return
	//     "fresh" immediately — no walk, no rebuild.
	//   - fresh=false, decided=true → recorded TreeOID exists and differs:
	//     DECISIVELY stale → straight to the rebuild path below (the loose
	//     check is NOT run here — it would add a redundant walk to the cold
	//     path).
	//   - decided=false → no baseline (nil index/Identity, empty/legacy
	//     TreeOID) or current OID unavailable (non-git worktree, git
	//     unavailable): inconclusive. Run the LOOSE content proof first; a
	//     content match proves the index is current, so it returns "fresh"
	//     without rebuilding. Only a loose "stale" falls through to rebuild.
	if lerr == nil && prev != nil {
		fresh, decided, _ := prev.TreeOIDProbe(root)
		if fresh {
			st := indexStatusFromIndex(root, prev, index.FreshnessProof{
				Verdict:   index.FreshnessFresh,
				Recorded:  *prev.Identity,
				CheckedAt: time.Now().UTC(),
			})
			return &EnsureFreshResult{Freshness: "fresh", IndexStatus: *st}, nil
		}
		if !decided {
			// Inconclusive → the loose content proof decides. This restores
			// the warm path for legacy indexes (empty recorded TreeOID) and
			// repos where git cannot compute the OID: it costs one walk, not
			// a rebuild.
			if st, err := s.Status(ctx, root, false); err == nil && st.Built && st.FreshnessProof.Verdict == index.FreshnessFresh {
				return &EnsureFreshResult{Freshness: "fresh", IndexStatus: *st}, nil
			}
		}
		// Decisively stale (or inconclusive with a stale loose proof): fall
		// through to the rebuild path.
	}

	// 3. Stale (or unprovable): run the incremental update over the previous
	// index — the same update-over-build pattern `kern index --update` uses —
	// falling back to a full build (which persists) when no index loads or
	// Update fails, and syncing the SQLite store when enabled.
	var ix *index.Index
	if lerr == nil && prev != nil {
		if uix, uerr := index.Update(root, prev); uerr == nil && uix != nil {
			ix = uix
		}
	}
	if ix == nil {
		// No loadable previous index, or Update failed: full build.
		var berr error
		ix, berr = s.Build(ctx, root)
		if berr != nil {
			return nil, fmt.Errorf("ensure-fresh: build: %w", berr)
		}
	} else {
		if serr := ix.Save(); serr != nil {
			return nil, fmt.Errorf("ensure-fresh: persist updated index: %w", serr)
		}
		if index.SQLiteEnabled() {
			_ = index.SaveSQLite(root, ix)
		}
	}

	// 4. Strict re-verify: a GENUINE fresh observation AFTER the save, loaded
	// from disk (Status re-reads the persisted index). This is the trust
	// anchor — the pre-update probe's strictness was deliberately redundant.
	if st, err := s.Status(ctx, root, true); err == nil && st.Built && st.FreshnessProof.Verdict == index.FreshnessFresh {
		return &EnsureFreshResult{Freshness: "rebuilt", IndexStatus: *st}, nil
	}
	// 5. Loose re-verify: strict recomputes content_root over EVERY file on
	// disk, while a build records content for the parseable source set only —
	// a repo containing an unparseable file (e.g. a compile-breaking fixture)
	// never converges under strict. The loose verdict is anchored to the git
	// tree, which is exactly the content the guard check evaluates.
	if st, err := s.Status(ctx, root, false); err == nil && st.Built && st.FreshnessProof.Verdict == index.FreshnessFresh {
		return &EnsureFreshResult{Freshness: "rebuilt", IndexStatus: *st}, nil
	}

	// 6. Non-convergence: fail closed. The index is stale and did not
	// converge after a rebuild; callers must refuse to trust it.
	st, err := s.Status(ctx, root, false)
	if err != nil {
		st = &IndexStatus{Root: root, SchemaVersion: "2", Built: false, Stale: true}
	}
	return &EnsureFreshResult{Freshness: "stale", IndexStatus: *st}, nil
}

// indexStatusFromIndex populates the standard IndexStatus-shaped payload from
// an already-loaded index and a freshness proof. Used by EnsureFresh's
// no-rebuild "fresh" path; Status re-loads the index itself so callers always
// observe disk state.
func indexStatusFromIndex(root string, ix *index.Index, proof index.FreshnessProof) *IndexStatus {
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
		TreeSitterEnabled: index.TreesitterEnabled(),
		SQLite:            index.SQLiteEnabled(),
	}
	if status.SQLite {
		status.Store = index.SQLitePath(root)
	} else {
		status.Store = index.StorePath(root)
	}
	return status
}
