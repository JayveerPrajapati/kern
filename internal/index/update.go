package index

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/JayveerPrajapati/kern/internal/cache"
	"github.com/JayveerPrajapati/kern/internal/ignore"
	"github.com/JayveerPrajapati/kern/internal/metrics"
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

	// symbol -> defining file, to map prev's merged edge maps back to
	// per-file contributions (edges sourced from a file = edges whose owner
	// is a symbol defined there).
	symFile := make(map[string]string, len(prev.Symbols))
	for _, s := range prev.Symbols {
		if _, ok := symFile[s.FullName()]; !ok {
			symFile[s.FullName()] = s.File
		}
	}

	// prev's virtual dispatch edges (added by addDispatchEdges during prev's
	// build) are subtracted from copied call lists so the merged Calls map is
	// pristine per-file extraction; the finalize pass re-adds exactly the
	// dispatch edges valid for the current tree. Without this, a dispatch
	// edge copied from prev whose interface was deleted would survive the
	// merge even though a full rebuild would not produce it.
	dispatch := dispatchEdgeSet(prev)

	callsByFile := map[string]map[string][]CallEdge{}  // file -> owner -> callees
	inheritsByFile := map[string]map[string][]string{} // file -> subtype -> bases
	var unattributable []string                        // owners with no defining symbol (kept verbatim)
	for owner, callees := range prev.Calls {
		file := symFile[owner]
		if file == "" {
			unattributable = append(unattributable, owner)
			continue
		}
		if callsByFile[file] == nil {
			callsByFile[file] = map[string][]CallEdge{}
		}
		if len(dispatch) > 0 {
			kept := make([]CallEdge, 0, len(callees))
			for _, ce := range callees {
				if dispatch[owner+"->"+ce.Target] {
					continue
				}
				kept = append(kept, ce)
			}
			callsByFile[file][owner] = kept
		} else {
			// No dispatch edges in prev: copy verbatim, preserving prev's
			// per-file merge order exactly.
			callsByFile[file][owner] = callees
		}
	}
	for subtype, bases := range prev.Inherits {
		if file := symFile[subtype]; file != "" {
			if inheritsByFile[file] == nil {
				inheritsByFile[file] = map[string][]string{}
			}
			inheritsByFile[file][subtype] = bases
		}
	}

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

	// Phase 2: per-file change detection in a worker pool — the same
	// collect → pool → ordered-replay structure as buildParallel. Workers are
	// pure: updateComputeFile never touches ix and only reads prev (prev's
	// maps are not written until replay completes), so the expensive
	// read/hash/parse overlaps across cores. Small trees bypass the pool with
	// buildParallel's parallelMin logic.
	var reusedCount atomic.Int64
	if len(jobs) < t.parallelMin {
		// Small tree: apply jobs serially in lexical order — byte-identical
		// to the pool path, exactly like buildParallel's bypass.
		for _, j := range jobs {
			ur := updateComputeFile(prev, j, callsByFile, inheritsByFile)
			if ur.r.readErr || ur.r.skip {
				continue
			}
			if ur.reused {
				recordCopied(ur.r.calls)
				reusedCount.Add(1)
			}
			ix.applyFileResult(ur.r)
		}
	} else {
		workers := t.workers
		results := make(chan updateResult, t.resultBuf)
		var wg sync.WaitGroup
		var next int64
		// applied tracks the merge cursor: the next seq the merge loop will
		// apply. Workers may claim jobs at most reorderWindow ahead of it, so
		// the reorder buffer stays bounded (B7).
		var applied atomic.Int64
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					claim := atomic.LoadInt64(&next)
					if claim >= int64(len(jobs)) {
						return
					}
					if claim >= applied.Load()+reorderWindow {
						// The reorder window is full — the merge cursor is
						// waiting on a slow head-of-line file. Back off; the
						// merge loop keeps draining results and will advance
						// the window once the head arrives.
						runtime.Gosched()
						continue
					}
					idx := atomic.AddInt64(&next, 1) - 1
					if idx >= int64(len(jobs)) {
						return
					}
					results <- updateComputeFile(prev, jobs[idx], callsByFile, inheritsByFile)
				}
			}()
		}
		go func() {
			wg.Wait()
			close(results)
		}()

		// Phase 3: serial ordered merge in the main goroutine — the ONLY
		// goroutine that mutates ix. Results are replayed in lexical (seq)
		// order, preserving the apply order that keeps the merged index
		// byte-identical to the serial path.
		pending := map[int]updateResult{}
		nextSeq := 0
		for ur := range results {
			pending[ur.r.seq] = ur
			for {
				ur2, ok := pending[nextSeq]
				if !ok {
					break
				}
				if !ur2.r.readErr && !ur2.r.skip {
					if ur2.reused {
						recordCopied(ur2.r.calls)
						reusedCount.Add(1)
					}
					ix.applyFileResult(ur2.r)
				}
				delete(pending, nextSeq)
				nextSeq++
			}
			applied.Store(int64(nextSeq))
		}
	}

	// Owners with no defining symbol are pathological (a Build-consistent
	// index keys every call edge by a symbol's full name); keep their edges
	// verbatim rather than dropping them.
	for _, owner := range unattributable {
		callees := prev.Calls[owner]
		if len(dispatch) > 0 {
			kept := make([]CallEdge, 0, len(callees))
			for _, ce := range callees {
				if dispatch[owner+"->"+ce.Target] {
					continue
				}
				kept = append(kept, ce)
			}
			callees = kept
		}
		for _, ce := range callees {
			copied[owner+"->"+ce.Target] = true
		}
		ix.Calls[owner] = append(ix.Calls[owner], callees...)
	}

	ix.UpdatedAt = time.Now().UTC()
	ix.buildSymbolIndex()
	// Dangling-edge cleanup must run before promoteLowEdges and
	// computeCallers: it prunes the Calls map, and the reconciliation pass
	// and Callers/AliasCallers are derived from it afterwards.
	dropDanglingCalls(ix, prev, copied)
	ix.promoteLowEdges()
	ix.addFrameworkDIEdges()
	ix.computeCallers()
	ix.addDispatchEdges()
	ix.measureCallResolution()
	ix.resolveEntries()
	ix.reindexByFile()
	// CG-P1-9: build the prose→symbol inverted vocab after the symbol table is
	// final so LookupProse can serve miss-chain candidates without re-walking it.
	ix.buildProseVocab()
	ix.computePrecisionByLang()
	// Content-addressed identity: FileHashes and MaxMtime are final, so the
	// freshness proofs compare identically to a full rebuild. The git half of
	// the identity was captured concurrently with the walk (startIdentityGit
	// below).
	ix.Identity = gitID.joinIdentity(ix.FileHashes, ix.UpdatedAt)
	ix.reusedResults = int(reusedCount.Load())
	metrics.Default().RecordIndexBuild(time.Since(start))
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
func updateComputeFile(prev *Index, j fileJob, callsByFile map[string]map[string][]CallEdge, inheritsByFile map[string]map[string][]string) updateResult {
	// Fast path: in-memory prior with an unchanged mtime — skip the read +
	// hash entirely (same trust model as Build's reuseByMtime).
	if r, ok := reuseByMtime(prev, j.rel, j.mtime); ok {
		r.seq = j.seq
		return updateResult{r: r, reused: true}
	}
	src, serr := os.ReadFile(j.path)
	if serr != nil {
		// Skip unreadable files (e.g. broken symlinks) instead of aborting
		// the whole update; the replay skips readErr results exactly like
		// buildParallel's merge loop.
		return updateResult{r: fileResult{seq: j.seq, rel: j.rel, readErr: true}}
	}
	if !isIndexable(j.rel, src) {
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
