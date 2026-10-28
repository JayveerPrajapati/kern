package index

// update_merge.go holds the three post-walk stages of update() extracted
// from update.go: splitPrevEdges (decomposing prev's merged maps back into
// per-file buckets), mergeUpdateJobs (the collect → pool → ordered-replay
// change-detection merge shared with buildParallel), and finalizeUpdate (the
// unattributable-owner merge, dangling-edge cleanup and the Build-equivalent
// finalize passes). Each was moved verbatim from the update() function body;
// the function itself now orchestrates them in sequence.

import (
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/JayveerPrajapati/kern/internal/calleefilter"
	"github.com/JayveerPrajapati/kern/internal/metrics"
)

// splitPrevEdges decomposes prev's merged symbol/call/inheritance maps back
// into per-file contributions for update()'s change detection. It returns
// the symbol→defining-files map (with a soleDefiningFile helper), prev's
// dispatch-edge set (subtracted from copied call lists so the merge is
// pristine per-file extraction), the per-file attribution buckets, and the
// lists of owners/subtypes not attributable to exactly one file (kept
// verbatim so no single file's change drops the other definers' edges).
func splitPrevEdges(prev *Index) (map[string]map[string]bool, func(string) string, map[string]bool, map[string]map[string][]CallEdge, map[string]map[string][]string, []string, []string) {
	// symbol -> defining files, to map prev's merged edge maps back to
	// per-file contributions (edges sourced from a file = edges whose owner
	// is a symbol defined there). A name defined in MORE than one file — a
	// Go function colliding with a prose heading, or same-named functions
	// across packages — must never be attributed to a single file: when that
	// file changed, replacing its bucket would silently delete the other
	// files' edges. Multi-defined owners join the unattributable
	// keep-verbatim path instead.
	symFiles := make(map[string]map[string]bool, len(prev.Symbols))
	for _, s := range prev.Symbols {
		if symFiles[s.FullName()] == nil {
			symFiles[s.FullName()] = map[string]bool{}
		}
		symFiles[s.FullName()][s.File] = true
	}
	// soleDefiningFile returns the single file defining name, or "" when
	// the name is undefined or defined in multiple files.
	soleDefiningFile := func(name string) string {
		files := symFiles[name]
		if len(files) != 1 {
			return ""
		}
		for f := range files {
			return f
		}
		return ""
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
	var unattributable []string                        // owners not attributable to exactly one file (kept verbatim)
	var unattributableInherits []string                // subtypes defined in multiple files (kept verbatim)
	for owner, callees := range prev.Calls {
		// Copied edges bypass the record-time noise filter (L5-1): stale
		// builtin and test-harness edges survived every update — filter
		// here. Chains stay: call-vs-field shape needs the AST.
		unowned := false
		for _, ce := range callees {
			file := ce.SourceFile
			if file == "" {
				file = soleDefiningFile(owner)
			}
			if file == "" {
				unowned = true
				continue
			}
			if ce.Synth == "dispatch" || (ce.SourceFile == "" && ce.Synth == "" && dispatch[owner+"->"+ce.Target]) ||
				calleefilter.IsNoiseCalleeFor(ce.Target, false, file) {
				continue
			}
			if callsByFile[file] == nil {
				callsByFile[file] = map[string][]CallEdge{}
			}
			callsByFile[file][owner] = append(callsByFile[file][owner], ce)
		}
		if unowned {
			unattributable = append(unattributable, owner)
		}
	}
	for subtype, bases := range prev.Inherits {
		if file := soleDefiningFile(subtype); file != "" {
			if inheritsByFile[file] == nil {
				inheritsByFile[file] = map[string][]string{}
			}
			inheritsByFile[file][subtype] = bases
		} else if symFiles[subtype] != nil {
			// Multi-defined subtype: same collision rule as call owners —
			// no single file's change may drop the other definers' edges.
			// Zero-file subtypes keep the historical drop (nothing to
			// re-attach them to).
			unattributableInherits = append(unattributableInherits, subtype)
		}
	}
	return symFiles, soleDefiningFile, dispatch, callsByFile, inheritsByFile, unattributable, unattributableInherits
}

// mergeUpdateJobs runs update()'s per-file change detection: the same
// collect → pool → ordered-replay structure as buildParallel. Workers are
// pure: updateComputeFile never touches ix and only reads prev (prev's maps
// are not written until replay completes), so the expensive read/hash/parse
// overlaps across cores. Small trees bypass the pool with buildParallel's
// parallelMin logic. recordCopied marks every edge copied verbatim from prev
// (unchanged files) so the caller's dangling-edge cleanup can target only
// copied edges. It returns the number of reused files (for BuildStats) and
// the first apply error, if any.
func mergeUpdateJobs(ix, prev *Index, snap *FreshnessSnapshot, jobs []fileJob, callsByFile map[string]map[string][]CallEdge, inheritsByFile map[string]map[string][]string, t tunables, recordCopied func(map[string][]CallEdge)) (int64, error) {
	var reusedCount atomic.Int64
	if len(jobs) < t.parallelMin {
		// Small tree: apply jobs serially in lexical order — byte-identical
		// to the pool path, exactly like buildParallel's bypass.
		for _, j := range jobs {
			ur := updateComputeFile(prev, j, snap, callsByFile, inheritsByFile)
			if ur.r.readErr || ur.r.skip {
				continue
			}
			if ur.reused {
				recordCopied(ur.r.calls)
				reusedCount.Add(1)
			}
			if err := ix.applyFileResult(ur.r); err != nil {
				return 0, err
			}
		}
		return reusedCount.Load(), nil
	}
	workers := t.workers
	results := make(chan updateResult, t.resultBuf)
	var wg sync.WaitGroup
	var next atomic.Int64
	// applied tracks the merge cursor: the next seq the merge loop will
	// apply. Workers may claim jobs at most reorderWindow ahead of it, so
	// the reorder buffer stays bounded (B7).
	var applied atomic.Int64
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				claim := next.Load()
				if claim >= int64(len(jobs)) {
					return
				}
				if claim >= applied.Load()+reorderWindow {
					// The reorder window is full — the merge cursor is
					// waiting on a slow head-of-line file. Back off; the
					// merge loop keeps draining results and will advance
					// the window once the head arrives.
					time.Sleep(reorderBackoff)
					continue
				}
				idx := next.Add(1) - 1
				if idx >= int64(len(jobs)) {
					return
				}
				results <- updateComputeFile(prev, jobs[idx], snap, callsByFile, inheritsByFile)
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
	var firstErr error
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
				if err := ix.applyFileResult(ur2.r); err != nil && firstErr == nil {
					firstErr = err
				}
			}
			delete(pending, nextSeq)
			nextSeq++
		}
		applied.Store(int64(nextSeq))
	}
	if firstErr != nil {
		return 0, firstErr
	}
	return reusedCount.Load(), nil
}

// finalizeUpdate runs update()'s post-merge passes: the unattributable
// (keep-verbatim) owner/subtype edge merge, the dangling-call cleanup, the
// conditional language binders, and the Build-equivalent finalize chain
// (callers, dispatch edges, resolution, prose vocab, precision), finishing
// with the content-addressed identity and build stats.
func finalizeUpdate(ix, prev *Index, gitID *identityGit, dispatch map[string]bool, copied map[string]bool, unattributable, unattributableInherits []string, reusedCount int64, t tunables, jobs []fileJob, start time.Time) error {
	// Unowned legacy edges whose owner is defined in several files cannot be
	// attributed safely; keep only those.
	for _, owner := range unattributable {
		for _, ce := range prev.Calls[owner] {
			if ce.SourceFile != "" || dispatch[owner+"->"+ce.Target] ||
				calleefilter.IsNoiseCalleeFor(ce.Target, false, "") {
				continue
			}
			copied[owner+"->"+ce.Target] = true
			ix.Calls[owner] = append(ix.Calls[owner], ce)
		}
	}

	// Subtypes defined in multiple files (the same name-collision rule as
	// call owners): their inheritance edges are kept verbatim so no single
	// file's change can drop another definer's edges. Sorted and deduped so
	// map-iteration order never leaks into the serialized index.
	sort.Strings(unattributableInherits)
	for _, subtype := range unattributableInherits {
		ix.Inherits[subtype] = append(ix.Inherits[subtype], dedupeSorted(prev.Inherits[subtype])...)
	}

	ix.UpdatedAt = time.Now().UTC()
	ix.buildSymbolIndex()
	// Dangling-edge cleanup must run before promoteLowEdges and
	// computeCallers: it prunes the Calls map, and the reconciliation pass
	// and Callers/AliasCallers are derived from it afterwards.
	dropDanglingCalls(ix, prev, copied)
	if prev.GoTypes {
		if err := ix.bindGoTypes(); err != nil {
			return err
		}
	}
	if prev.JavaCompiler {
		if err := ix.bindJavaCompiler(prev.JavaClasspath); err != nil {
			return err
		}
	}
	ix.promoteLowEdges()
	ix.addFrameworkDIEdges()
	ix.computeCallers()
	ix.addDispatchEdges()
	ix.measureCallResolution()
	ix.resolveEntries()
	ix.reindexByFile()
	// build the prose→symbol inverted vocab after the symbol table is
	// final so LookupProse can serve miss-chain candidates without re-walking it.
	ix.buildProseVocab()
	ix.computePrecisionByLang()
	// Content-addressed identity: FileHashes and MaxMtime are final, so the
	// freshness proofs compare identically to a full rebuild. The git half of
	// the identity was captured concurrently with the walk (startIdentityGit
	// below).
	ix.Identity = gitID.joinIdentity(ix.FileHashes, ix.UpdatedAt)
	ix.reusedResults = int(reusedCount)
	workers := 1
	if len(jobs) >= t.parallelMin {
		workers = t.workers
	}
	// Record what changed for the persistence layer: only these files/owners
	// need rewriting (Save falls back to a full write on any delta error).
	changed, removed := diffHashes(prev.FileHashes, ix.FileHashes)
	ix.delta = &deltaPlan{changed: changed, removed: removed, owners: diffOwners(prev.Calls, ix.Calls)}

	ix.BuildStats = BuildStats{
		Kind:    "update",
		Ms:      time.Since(start).Milliseconds(),
		Workers: workers,
		Reused:  int(reusedCount),
	}
	metrics.Default().RecordIndexBuild(time.Since(start))
	return nil
}
