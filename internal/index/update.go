package index

import (
	"fmt"
	"github.com/JayveerPrajapati/kern/internal/calleefilter"
	"github.com/JayveerPrajapati/kern/internal/langparse"
	"path/filepath"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/cache"
	"github.com/JayveerPrajapati/kern/internal/ignore"
)

// Update incrementally refreshes an existing index for root without
// re-parsing unchanged files. prev is the previously built index (typically
// loaded from disk, but any valid *Index works). The walk applies the exact
// same file-selection policy as Build (shared walkIndexable), then:
//
//   - unchanged files (content hash matches prev.FileHashes) have their
//     symbols and edges copied verbatim from prev — no re-parse;
//   - changed and new files go through the same per-file extraction path
//     Build uses (computeFileResult);
//   - deleted files (in prev, absent on disk) drop their symbols and every
//     edge sourced from them.
//
// The per-file work is parallelized with buildParallel's collect → pool →
// ordered-replay structure: a phase-1 walk collects jobs, a worker pool
// computes each file's result, and a single goroutine replays them in lexical
// order — byte-identical to the serial path (small trees bypass the pool with
// the same parallelMin logic).
// After the merge, call edges whose target symbol no longer exists (a local
// callee deleted by this update) are dropped, matching raw callee endpoints
// against raw symbol names and qualified aliases — never resolved through
// graph node IDs. Finally the same finalize passes as Build run
// (computeCallers, addDispatchEdges, resolveEntries, ...) so the result is
// equivalent to a full rebuild, including FileHashes, MaxMtime and the
// content-addressed Identity.
//
// Any internal error returns an error so callers can fall back to a full
// Build; the returned index is nil on error.
func Update(root string, prev *Index) (*Index, error) {
	return update(root, prev, nil)
}

// UpdateWithSnapshot is Update reusing the content walk a staleness check
// already did on prev: prev.staleSnapshot (retained by the most recent
// Stale()/StaleWithProof() that ran a content walk) carries each file's hash
// and the mtime observed in that same walk. A file whose CURRENT mtime (from
// the update's own phase-1 stat walk) still matches the snapshot's is
// provably unmodified since the staleness decision, so its recorded hash is
// used to decide reuse against prev.FileHashes without re-reading the file —
// eliminating the second full tree walk+hash on the stale path. Every other
// file (edited between the two walks, added, missing from the snapshot)
// falls through to Update's exact read+hash path, so the merged index
// reflects the live tree exactly as Update would; the optimization only
// ever skips reads for files whose mtime proves them unchanged (the same
// trust model reuseByMtime already relies on). When prev has no retained
// observation (nil snapshot — no staleness check ran, or the last check
// decided without a content walk), UpdateWithSnapshot behaves exactly like
// Update.
func UpdateWithSnapshot(root string, prev *Index) (*Index, error) {
	if prev == nil {
		return update(root, prev, nil)
	}
	return update(root, prev, prev.retainedSnapshot())
}

func hasNoiseEdges(ix *Index) bool {
	for _, edges := range ix.Calls {
		for _, ce := range edges {
			if calleefilter.IsNoiseCalleeFor(ce.Target, false, ce.SourceFile) {
				return true
			}
		}
	}
	return false
}

func update(root string, prev *Index, snap *FreshnessSnapshot) (*Index, error) {
	if prev == nil {
		return nil, fmt.Errorf("index.Update: nil previous index")
	}
	start := time.Now()
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if prev.Root != "" && prev.Root != abs {
		return nil, fmt.Errorf("index.Update: previous index rooted at %q, want %q", prev.Root, abs)
	}
	prev.initMaps()
	prev.reindexByFile() // guarantee SymbolsByFile for attribution

	// Resolve the same resource-adaptive profile as Build (defaults: the
	// caller cannot tune an Update — the adaptive file-size cap keeps the
	// walk's file selection byte-identical to a full rebuild).
	t := resolveTunables(&buildConfig{}, resolveResources())
	ign := ignore.Load(abs)

	ix := New(abs)
	ix.Version = indexVersion

	// Decompose prev's merged edge maps back into per-file contributions
	// (see splitPrevEdges), then record the verbatim-copied edge set the
	// dangling cleanup needs.
	_, _, dispatch, callsByFile, inheritsByFile, unattributable, unattributableInherits := splitPrevEdges(prev)
	// copied records every edge copied verbatim from prev (unchanged files),
	// so dangling-edge cleanup can target only copied edges — freshly
	// extracted edges always reflect the current source.
	copied := map[string]bool{}
	recordCopied := func(calls map[string][]CallEdge) {
		for owner, callees := range calls {
			for _, ce := range callees {
				copied[owner+"->"+ce.Target] = true
			}
		}
	}

	// Kick off the git identity observations (tree OID + commit) BEFORE the
	// walk so the ~0.5s git staging dance overlaps with the change-detection
	// walk instead of running after it; joined once FileHashes are final.
	gitID := startIdentityGit(abs)

	// Phase 1: serial walk collecting jobs. walkIndexable stays the single
	// source of truth for file selection — the callback only captures the
	// (rel, path, mtime, seq) tuple, mirroring buildParallel; no file contents
	// are read here.
	var jobs []fileJob
	err = walkIndexable(abs, ign, t.maxFileBytes, func(rel, path string, mtime int64) error {
		jobs = append(jobs, fileJob{seq: len(jobs), rel: rel, path: path, mtime: mtime})
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Phases 2-3: per-file change detection in a worker pool with a serial
	// ordered merge (see mergeUpdateJobs). Returns the reused-file count for
	// BuildStats and the first apply error, if any.
	reusedCount, err := mergeUpdateJobs(ix, prev, snap, jobs, callsByFile, inheritsByFile, t, recordCopied)
	if err != nil {
		return nil, err
	}

	// No-op fast path (identical content): skip finalize and the rewrite.
	if int(reusedCount) == len(jobs) && sameHashes(prev.FileHashes, ix.FileHashes) && !hasNoiseEdges(prev) {
		clone := *prev
		clone.skipPersist = true
		clone.reusedResults = int(reusedCount)
		clone.BuildStats = BuildStats{Kind: "update", Ms: time.Since(start).Milliseconds(), Workers: 1, Reused: int(reusedCount)}
		return &clone, nil
	}

	// Phase 4: unattributable-owner merge, dangling-edge cleanup and the
	// Build-equivalent finalize passes (see finalizeUpdate).
	if err := finalizeUpdate(ix, prev, gitID, dispatch, copied, unattributable, unattributableInherits, reusedCount, t, jobs, start); err != nil {
		return nil, err
	}
	return ix, nil
}

// updateResult carries one file's change-detection outcome: the computed
// fileResult plus whether it was reused verbatim from prev. Only reused
// results are added to the copied-edge set for dangling cleanup — freshly
// extracted edges always reflect the current source.
type updateResult struct {
	r      fileResult
	reused bool
}

// updateComputeFile does the per-file change-detection work Update's walk
// used to do inline: the reuseByMtime fast path, the content-hash match vs
// prev.FileHashes (verbatim reuse via prev.fileResults, or reconstruction
// from prev's merged maps for a loaded prior), or a fresh computeFileResult.
// It is a pure function of (prev, job, callsByFile, inheritsByFile) — it
// never touches ix and never writes to prev — so it is safe to run
// concurrently in the worker pool. prev.FileHashes and prev.fileResults are
// only read here; nothing writes them until replay completes.
func updateComputeFile(prev *Index, j fileJob, snap *FreshnessSnapshot, callsByFile map[string]map[string][]CallEdge, inheritsByFile map[string]map[string][]string) updateResult {
	// Fast path: in-memory prior with an unchanged mtime — skip the read +
	// hash entirely (same trust model as Build's reuseByMtime).
	if r, ok := reuseByMtime(prev, j.rel, j.mtime); ok {
		r.seq = j.seq
		return updateResult{r: r, reused: true}
	}
	// Staleness-walk reuse (M5a): the freshness check that put the caller on
	// the update path already read and hashed this file (snap, retained by
	// Stale()/StaleWithProof()), and the file's mtime is unchanged since that
	// walk — so the recorded hash is provably still the current content hash.
	// Skip the read+hash and go straight to the prev.FileHashes comparison.
	// A file edited between the staleness walk and this walk has a different
	// mtime and falls through to the read path below, so the merge always
	// reflects the live tree (same trust model as reuseByMtime: an edit that
	// does not bump the mtime is invisible, exactly as it already is to
	// staleness detection and Build's incremental reuse).
	if snap != nil && snap.Hashes != nil {
		if h, ok := snap.Hashes[j.rel]; ok {
			if m, ok := snap.Mtimes[j.rel]; ok && m == j.mtime {
				if ph, ok := prev.FileHashes[j.rel]; ok && ph == h {
					// Unchanged since prev was built: reuse its contribution
					// verbatim instead of re-parsing (identical to the
					// hash-match branch below).
					if r, ok := prev.fileResults[j.rel]; ok {
						r.mtime = j.mtime
						r.seq = j.seq
						r.pkg = copyPkg(r.pkg)
						return updateResult{r: r, reused: true}
					}
					r := reconstructFileResult(prev, j.rel, ph, j.mtime, callsByFile, inheritsByFile)
					r.seq = j.seq
					return updateResult{r: r, reused: true}
				}
				// Changed vs prev (snapshot hash differs): extraction needs the
				// file contents anyway — fall through to the read path.
			}
		}
	}
	src, serr := readFile(j.path)
	if serr != nil {
		// Skip unreadable files (e.g. broken symlinks) instead of aborting
		// the whole update; the replay skips readErr results exactly like
		// buildParallel's merge loop.
		return updateResult{r: fileResult{seq: j.seq, rel: j.rel, readErr: true}}
	}
	if !langparse.IsIndexable(j.rel, src) {
		return updateResult{r: fileResult{seq: j.seq, rel: j.rel, skip: true}}
	}
	if ph, ok := prev.FileHashes[j.rel]; ok && ph == cache.Hash(src) {
		// Unchanged since prev was built: reuse its contribution verbatim
		// instead of re-parsing.
		if r, ok := prev.fileResults[j.rel]; ok {
			r.mtime = j.mtime
			r.seq = j.seq
			r.pkg = copyPkg(r.pkg)
			return updateResult{r: r, reused: true}
		}
		// Loaded from disk (no per-file parse results survive serialization):
		// reconstruct the per-file contribution from prev's merged maps.
		r := reconstructFileResult(prev, j.rel, ph, j.mtime, callsByFile, inheritsByFile)
		r.seq = j.seq
		return updateResult{r: r, reused: true}
	}
	// Changed or new file: the same per-file extraction path Build uses.
	r := computeFileResult(j.rel, src, j.mtime)
	r.seq = j.seq
	return updateResult{r: r}
}

// reconstructFileResult rebuilds the per-file contribution of an unchanged
// file from a loaded previous index's merged maps. Symbols are copied
// verbatim from prev's per-file attribution; call/inheritance edges are
// copied from the pre-attributed per-file maps (already stripped of prev's
// dispatch edges); the package record is rebuilt from ImportsByFile so the
// package merge reproduces a fresh parse. Files whose hash is recorded but
// that produced no parse evidence in prev (no symbols, no generated marker,
// no imports) failed to parse then — same content yields the same result, so
// the parse-failure is carried over.
func reconstructFileResult(prev *Index, rel, hash string, mtime int64, callsByFile map[string]map[string][]CallEdge, inheritsByFile map[string]map[string][]string) fileResult {
	r := fileResult{
		rel:   rel,
		hash:  hash,
		mtime: mtime,
		syms:  append([]Symbol(nil), prev.SymbolsByFile[rel]...),
	}
	generated, parsed := prev.GeneratedFiles[rel]
	r.generated = generated
	imports, hasImports := prev.ImportsByFile[rel]
	if !parsed && !hasImports && len(r.syms) == 0 {
		r.parseErr = true
		return r
	}
	r.calls = callsByFile[rel]
	r.inherits = inheritsByFile[rel]
	if hasImports {
		pkg := prev.Pkgs[filepath.Dir(rel)]
		r.pkg = &Pkg{
			Name:    "",
			Path:    filepath.Dir(rel),
			Files:   []string{rel},
			Imports: append([]ImportEdge(nil), imports...),
			Exports: append([]ExportEdge(nil), prev.ExportsByFile[rel]...),
		}
		if pkg != nil {
			r.pkg.Name = pkg.Name
			r.pkg.Lang = pkg.Lang
			// Per-file struct-field attribution is not serialized, so an
			// unchanged file's struct fields survive only through the
			// package-merged map. Carry it forward, or every incremental
			// update on a disk-loaded prior silently strips StructFields
			// (the receiver-field callee rewrite then degrades and the dead
			// lens re-flags live field-access calls).
			if pkg.StructFields != nil {
				r.pkg.StructFields = make(map[string]string, len(pkg.StructFields))
				for k, v := range pkg.StructFields {
					r.pkg.StructFields[k] = v
				}
			}
			// Same carry-forward for constructor return types: without it an
			// incremental update on a disk-loaded prior silently strips
			// Constructors and the cross-package constructor rewrite degrades.
			if pkg.Constructors != nil {
				r.pkg.Constructors = make(map[string]string, len(pkg.Constructors))
				for k, v := range pkg.Constructors {
					r.pkg.Constructors[k] = v
				}
			}
		}
	}
	return r
}

// dispatchEdgeSet returns the virtual call edges addDispatchEdges would add
// for the given index state, keyed "caller->virtualCallee". It mirrors the
// edge-addition loop of addDispatchEdges deterministically so Update can
// subtract a prior index's dispatch edges from copied call lists and recover
// pristine per-file extraction. (addDispatchEdges itself is unchanged; it
// dedupes and sorts the whole Calls map afterwards, which keeps the final
// merged index byte-identical regardless of this helper.)
func dispatchEdgeSet(ix *Index) map[string]bool {
	pairs := map[string]bool{}
	if ix == nil || len(ix.InheritedBy) == 0 {
		return pairs
	}
	symSet := map[string]bool{}
	for _, s := range ix.Symbols {
		symSet[s.FullName()] = true
	}
	added := map[string]bool{}
	for caller, callees := range ix.Calls {
		for _, ce := range callees {
			c := ce.Target
			dot := strings.LastIndex(c, ".")
			if dot < 0 || dot == 0 {
				continue
			}
			receiver, method := c[:dot], c[dot+1:]
			if receiver == "" || method == "" {
				continue
			}
			for _, impl := range ix.InheritedBy[receiver] {
				virtualCallee := impl + "." + method
				if !symSet[virtualCallee] {
					continue
				}
				key := caller + "->" + virtualCallee
				if added[key] {
					continue
				}
				added[key] = true
				pairs[key] = true
			}
		}
	}
	return pairs
}

// dropDanglingCalls removes copied call edges whose target symbol no longer
// exists in the merged index. Only edges copied verbatim from prev are
// candidates: a freshly extracted edge always reflects the current source and
// is never touched. A target is "no longer present" only when it matched a
// prev symbol and matches no merged symbol — matching the RAW callee endpoint
// against raw symbol names and qualified aliases, never resolving through
// graph node IDs (cross-package callees are recorded qualified, e.g. "db.Do",
// while node IDs are bare, e.g. "Do"). Foreign targets ("fmt.Println") never
// matched a local symbol, so they survive the cleanup.
func dropDanglingCalls(ix, prev *Index, copied map[string]bool) {
	if len(copied) == 0 {
		return
	}
	prevSet := symbolNameSet(prev)
	curSet := symbolNameSet(ix)
	for caller, callees := range ix.Calls {
		var kept []CallEdge
		for _, ce := range callees {
			c := ce.Target
			if copied[caller+"->"+c] && targetExists(prevSet, c) && !targetExists(curSet, c) {
				continue
			}
			kept = append(kept, ce)
		}
		ix.Calls[caller] = kept
	}
}

// symbolNameSet maps every symbol's raw name and full name ("Type.Method")
// to true, for target-existence checks on raw edge endpoints.
func symbolNameSet(ix *Index) map[string]bool {
	set := make(map[string]bool, len(ix.Symbols)*2)
	for _, s := range ix.Symbols {
		set[s.Name] = true
		set[s.FullName()] = true
	}
	return set
}

// targetExists reports whether an edge endpoint names a symbol: the exact
// endpoint text, or — for a qualified endpoint like "db.Do" — its bare
// "Do" alias (the graph node ID). The package qualifier is deliberately not
// resolved through node IDs; a bare-name hit is the contract the graph uses.
func targetExists(set map[string]bool, c string) bool {
	if set[c] {
		return true
	}
	if i := strings.LastIndexByte(c, '.'); i > 0 && i+1 < len(c) {
		return set[c[i+1:]]
	}
	return false
}
