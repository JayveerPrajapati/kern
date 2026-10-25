package index

import (
	"context"
	"errors"
	"fmt"
	"github.com/JayveerPrajapati/kern/internal/langparse"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"time"

	"github.com/JayveerPrajapati/kern/internal/cache"
	"github.com/JayveerPrajapati/kern/internal/ignore"
)

// readFile is a var so tests can swap in a counting wrapper to prove how many
// times file contents are read (staleness walks and update change-detection
// both read through it; stat-only walks do not).
var readFile = func(path string) ([]byte, error) {
	return os.ReadFile(path)
}

// indexableMaxMtime walks root without reading file contents and returns the
// newest modification time (Unix nanos) among indexable files plus their count.
// The stat-only selection matches the content walk (walkQuickExt): extension
// is a proxy for indexability, but extension-less shebang scripts are
// indexable via content sniffing, so they count here too — a stat walk that
// dropped them would report a MaxMtime lower than the newest indexed file and
// let a stale index slip past the mtime gate. A walk error is returned so
// callers can decide to rebuild instead of serving a stale index. The ignore
// matcher mirrors Build's file-selection policy so gitignored files never
// influence the staleness decision.
func indexableMaxMtime(root string, ign *ignore.Matcher) (int64, int, error) {
	var maxMtime int64
	var count int
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && ignoreDirs[d.Name()] {
				return filepath.SkipDir
			}
			// Honor .gitignore/.kernignore directory patterns, as Build does.
			if path != root && ign != nil {
				if rel, rerr := filepath.Rel(root, path); rerr == nil && ign.Ignored(filepath.ToSlash(rel)) {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if d.Name() == ".git" || d.Name() == ".kern" {
			return nil
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return nil
		}
		// Honor .gitignore/.kernignore file patterns, as Build does.
		if ign != nil && ign.Ignored(filepath.ToSlash(rel)) {
			return nil
		}
		// Same acceptance rule as the content walk: extension-less files are
		// indexable via content sniffing, so they count toward the mtime
		// gate (a stat walk dropping them could mask a stale index).
		if !langparse.QuickExt(rel) && filepath.Ext(rel) != "" {
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return ierr
		}
		count++
		if mt := info.ModTime().UnixNano(); mt > maxMtime {
			maxMtime = mt
		}
		return nil
	})
	if err != nil {
		return 0, 0, err
	}
	return maxMtime, count, nil
}

// HasIndexableSources reports whether the tree under root contains at least one
// indexable source file, walking only until the first match. Used by health
// checks that must not pay for a full build (e.g. doctor).
func HasIndexableSources(root string) bool {
	found := false
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || found {
			return nil
		}
		if d.IsDir() {
			if path != root && ignoreDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil || !langparse.QuickExt(rel) {
			return nil
		}
		data, derr := readFile(path)
		if derr != nil {
			return nil
		}
		if langparse.IsIndexable(rel, data) {
			found = true
		}
		return nil
	})
	return found
}

// indexableHashes walks root and returns a map of relative file path to
// content hash for every indexable source file. Used by the watcher to detect
// changes and by Load/Stale to decide whether a cached index is out of date.
// A walk or read error is returned rather than silently producing a partial
// map, which could wrongly mark an edited tree as unchanged. The ignore
// matcher mirrors Build's file-selection policy so gitignored files never
// appear in the manifest.
// indexableHashes walks root and returns the content hash of every indexable
// file (path -> SHA-256), the same file selection Build uses. File reads and
// hashes are distributed over a worker pool: a few thousand small files cost
// hundreds of milliseconds sequentially, and the result is a map, so
// parallelism changes nothing observable. Directory traversal, extension and
// ignore filtering stay on the walking goroutine so the ignore.Matcher and
// the deterministic walk order are single-threaded; workers only read and
// hash accepted files. Any read error aborts the hash set (first error wins,
// same contract as the sequential version).
func indexableHashes(root string, ign *ignore.Matcher) (map[string]string, error) {
	obs, err := indexableHashesObserved(root, ign)
	return obs.hashes, err
}

// walkObservation is what one indexable-walk learned about the tree.
type walkObservation struct {
	hashes    map[string]string // content-indexable files → SHA-256
	mtimes    map[string]int64  // same files → mtime (Unix nanos)
	sizes     map[string]int64  // same files → size in bytes
	walkCount int               // files the walk VISITED (quickExt+ignore filter)
}

// indexableHashesObserved is indexableHashes plus the per-file mtime and
// size observed in the SAME walk that produced each hash, plus the count of
// files the walk visited. The mtimes/sizes let a later consumer
// (UpdateWithSnapshot, and the staleness stat-gate) re-validate the
// recorded hashes against the live tree with a cheap stat instead of
// re-reading file contents: a file whose mtime AND size are unchanged
// since this walk provably (same trust model as reuseByMtime, plus the
// size leg to catch mtime-preserving edits like git apply) still has the
// recorded content. The mtime is captured from the walk's DirEntry BEFORE
// the worker reads the file, so an edit landing between the stat and the
// read bumps the on-disk mtime past the recorded one and the consumer
// re-reads. The maps are nil on error; walkCount is valid only on nil err.
func indexableHashesObserved(root string, ign *ignore.Matcher) (walkObservation, error) {
	obs := walkObservation{
		hashes: map[string]string{},
		mtimes: map[string]int64{},
		sizes:  map[string]int64{},
	}
	var (
		mu   sync.Mutex
		ferr error
	)
	workers := runtime.NumCPU()
	if workers < 1 {
		workers = 1
	}
	if workers > 8 {
		workers = 8
	}
	type job struct {
		path, rel string
		mtime     int64
		size      int64
	}
	jobs := make(chan job)
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			for j := range jobs {
				data, derr := readFile(j.path)
				if derr != nil {
					mu.Lock()
					if ferr == nil {
						ferr = derr
					}
					mu.Unlock()
					continue
				}
				if !langparse.IsIndexable(j.rel, data) {
					continue
				}
				h := cache.Hash(data)
				mu.Lock()
				obs.hashes[j.rel] = h
				obs.mtimes[j.rel] = j.mtime
				obs.sizes[j.rel] = j.size
				mu.Unlock()
			}
		}()
	}
	err := walkQuickExt(root, ign, proofWalkMaxBytes(), func(rel, path string, info fs.FileInfo) error {
		obs.walkCount++
		jobs <- job{path: path, rel: rel, mtime: info.ModTime().UnixNano(), size: info.Size()}
		return nil
	})
	close(jobs)
	wg.Wait()
	if err != nil {
		return walkObservation{}, err
	}
	if ferr != nil {
		return walkObservation{}, ferr
	}
	return obs, nil
}

// errSnapshotMismatch signals snapshotStillCurrent's walk that a recorded
// file's stat no longer matches; any non-nil error means "gate failed".
var errSnapshotMismatch = errors.New("snapshot stat mismatch")

// snapshotStillCurrent re-validates a retained observation with a STAT-ONLY
// walk — no file content is read. Every file the snapshot recorded must
// still exist with an identical mtime AND size, the snapshot's files must
// all be covered, and the total walked count must match the count observed
// by the walk that produced the snapshot (a new file forces the full
// walk). This is the hot-path guard for repeated staleness checks on a
// dirty tree (where the git tree-OID fast path cannot vouch): the first
// check pays the full read+hash walk, every subsequent check pays only
// stats as long as nothing changed. Trust model: mtime+size (never mtime
// alone — a size-changing mtime-preserving edit, e.g. git apply, is caught
// by the size leg; a same-size mtime-preserving edit evades the gate only
// until gateTTL expires, which FreshnessProofStrict and the clean-tree git
// OID path keep catching regardless).
func snapshotStillCurrent(root string, ign *ignore.Matcher, snap *FreshnessSnapshot) bool {
	if snap == nil || len(snap.Mtimes) == 0 {
		return false
	}
	walked, seen := 0, 0
	err := walkQuickExt(root, ign, proofWalkMaxBytes(), func(rel, path string, info fs.FileInfo) error {
		walked++
		wantM, ok := snap.Mtimes[rel]
		if !ok {
			return nil
		}
		seen++
		if wantM != info.ModTime().UnixNano() || snap.Sizes[rel] != info.Size() {
			return errSnapshotMismatch
		}
		return nil
	})
	return err == nil && seen == len(snap.Mtimes) && walked == snap.WalkCount
}

// walkQuickExt walks root with exactly the filter set the content walk
// (indexableHashesObserved) uses — ignoreDirs, .gitignore/.kernignore,
// quickExt — and reports each surviving file's FileInfo. Shared by the
// hashing walk and the stat-gate walk so the two counts are comparable.
// fn runs on the walk goroutine: it must only enqueue work.
//
// The per-file selection must be BYTE-FOR-BYTE the build walk's
// (walkIndexable): both accept extension-less files (shebang scripts) that
// langparse.QuickExt alone would drop, both skip non-regular files, and both
// apply the same maxBytes size cap. A divergence makes a file the build
// recorded invisible to the proof walk (or vice versa) — a permanent
// false-STALE verdict that sends every freshness check into an
// ensure-fresh rebuild loop. maxBytes is the adaptive default the build
// resolves (resolveTunables), so both sides pick the same file set.
func walkQuickExt(root string, ign *ignore.Matcher, maxBytes int64, fn func(rel, path string, info fs.FileInfo) error) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && ignoreDirs[d.Name()] {
				return filepath.SkipDir
			}
			// Honor .gitignore/.kernignore directory patterns, as Build does.
			if path != root && ign != nil {
				if rel, rerr := filepath.Rel(root, path); rerr == nil && ign.Ignored(filepath.ToSlash(rel)) {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if d.Name() == ".git" || d.Name() == ".kern" {
			return nil
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return nil
		}
		// Honor .gitignore/.kernignore file patterns, as Build does.
		if ign != nil && ign.Ignored(filepath.ToSlash(rel)) {
			return nil
		}
		// Same acceptance rule as walkIndexable: an extension-less file
		// (shebang script) is indexable via content sniffing even though
		// QuickExt (extension-based) would drop it.
		if !langparse.QuickExt(rel) && filepath.Ext(rel) != "" {
			return nil
		}
		// Skip non-regular files (FIFOs, sockets, device nodes) — a FIFO
		// named like a source file would otherwise block the read.
		if !d.Type().IsRegular() {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return ierr
		}
		// Same size cap as the build walk: a file over maxBytes is skipped at
		// build time, so the proof walk must skip it too or the aggregate
		// hash can never match (permanent false-STALE).
		if info.Size() > maxBytes {
			return nil
		}
		return fn(rel, path, info)
	})
}

// proofWalkMaxBytes returns the adaptive per-file size cap the freshness
// walks apply — the same resolveTunables value the build walks use — so the
// proof and build walks select the same file set. An explicit
// WithMaxFileBytes build is a rare operator-option case the persisted index
// does not record; the adaptive default is the deterministic per-machine
// baseline both sides share.
func proofWalkMaxBytes() int64 {
	return resolveTunables(&buildConfig{}, resolveResources()).maxFileBytes
}

// ChangeKind describes a file change detected by the watcher.
type ChangeKind string

const (
	ChangeAdded    ChangeKind = "added"
	ChangeModified ChangeKind = "modified"
	ChangeRemoved  ChangeKind = "removed"
)

// Change is one file change detected by the watcher.
type Change struct {
	Kind ChangeKind `json:"kind"`
	File string     `json:"file"`
}

// Adaptive poll-interval thresholds for Watch. Small change sets (fewer than
// adaptSmall files) poll at the base interval so follow-up edits are picked up
// quickly; larger change sets back off by one scale unit per hundred changed
// files (adaptScalePerHundred), capped at adaptMaxScale, because the rebuild
// that follows a big change set is itself expensive; an idle tree decays back
// toward the base interval. All thresholds are integer counts of changed files
// per poll cycle.
const (
	adaptSmall           = 20
	adaptScalePerHundred = 1
	adaptMaxScale        = 8
)

// adaptiveInterval returns the interval for the NEXT Watch poll cycle given
// the base interval, the number of changes detected in the last cycle
// (lastChanges), and the interval that last cycle used (lastInterval).
//
//   - no changes (lastChanges == 0): decay toward base — if lastInterval is
//     above base because a large change set backed off, halve it toward base;
//     once at (or below) base, poll at base.
//   - small change sets (0 < lastChanges <= adaptSmall): poll at base, a fast
//     follow-up after small edits.
//   - large change sets (lastChanges > adaptSmall): back off to
//     base * scale where scale = 1 + lastChanges/adaptScalePerHundred, capped
//     at adaptMaxScale.
//
// Pure and deterministic: the same inputs always yield the same interval, so
// Watch's polling cadence is fully predictable from the change history.
func adaptiveInterval(base time.Duration, lastChanges int, lastInterval time.Duration) time.Duration {
	if lastChanges <= 0 {
		if lastInterval > base {
			half := lastInterval / 2
			if half < base {
				return base
			}
			return half
		}
		return base
	}
	if lastChanges <= adaptSmall {
		return base
	}
	// One scale unit per hundred changed files: 100 changes -> scale 2,
	// 1000 -> 11, capped at adaptMaxScale.
	scale := 1 + (lastChanges/100)*adaptScalePerHundred
	if scale > adaptMaxScale {
		scale = adaptMaxScale
	}
	return base * time.Duration(scale)
}

// Watch polls root every interval and rebuilds + saves the index whenever the
// set of Go files or their content changes. onChange is called with the
// detected changes and the fresh index. onError receives every non-fatal
// failure (scan, build, or save) so a long-running watcher can surface
// problems instead of silently dropping them.
//
// The poll interval is adaptive (see adaptiveInterval): it stays at the base
// interval after no or small change sets and backs off — up to
// adaptMaxScale × base — after large change sets, decaying back toward base
// as the tree settles.
func Watch(ctx context.Context, root string, interval time.Duration, onChange func(changes []Change, ix *Index), onError func(err error)) error {
	abs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	root = abs
	prev := map[string]string{}
	if ix, err := Load(root); err == nil && ix != nil {
		prev = ix.FileHashes
	}
	// wait tracks the interval the previous poll cycle used, starting at the
	// base interval and recomputed per cycle by adaptiveInterval so the
	// polling cadence reacts to the change history.
	wait := interval
	for {
		cur, err := indexableHashes(root, ignore.Load(root))
		if err != nil {
			if onError != nil {
				onError(err)
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(wait):
			}
			continue
		}
		changes := diff(prev, cur)
		if len(changes) > 0 {
			ix, err := Build(root)
			if err != nil {
				if onError != nil {
					onError(fmt.Errorf("rebuild after %d change(s): %w", len(changes), err))
				}
			} else if err := ix.Save(); err != nil {
				if onError != nil {
					onError(fmt.Errorf("save after %d change(s): %w", len(changes), err))
				}
			} else {
				if onChange != nil {
					onChange(changes, ix)
				}
				prev = cur
			}
		}
		wait = adaptiveInterval(interval, len(changes), wait)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
}

func diff(prev, cur map[string]string) []Change {
	var changes []Change
	for f, h := range cur {
		ph, ok := prev[f]
		if !ok {
			changes = append(changes, Change{Kind: ChangeAdded, File: f})
		} else if ph != h {
			changes = append(changes, Change{Kind: ChangeModified, File: f})
		}
	}
	for f := range prev {
		if _, ok := cur[f]; !ok {
			changes = append(changes, Change{Kind: ChangeRemoved, File: f})
		}
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].File < changes[j].File })
	return changes
}

// CatchUpMaxChanges is the incremental-vs-full reconcile policy: diffs at or
// below this many changed files are applied incrementally (index.Update);
// larger diffs rebuild (index.Build). Shared by service.LoadOrBuild and
// project.Session.rebuildIndex.
const CatchUpMaxChanges = 200

// FileHashes returns a map of relative file path to content hash for every
// indexable source file under root. Exported for watcher implementations that
// need to compute change sets without rebuilding the whole index. A scan error
// is returned rather than a silent partial result.
func FileHashes(root string) (map[string]string, error) {
	return indexableHashes(root, ignore.Load(root))
}

// Diff reports the change set (adds, modifies, removes) between two hash maps
// as produced by FileHashes. Exported for watcher implementations.
func Diff(prev, cur map[string]string) []Change {
	return diff(prev, cur)
}
