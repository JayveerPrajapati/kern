// Package indexwatch carries the index's file-watching and hashing family:
// the content walk (path → SHA-256 with per-file mtime/size observation),
// the stat-only max-mtime gate, change-set detection (Diff/Change) and the
// watcher's adaptive poll-interval policy. Extracted from internal/index
// (watch.go) so the hashing walks are independently governed; internal/index
// imports it — never the reverse (the tunables-dependent maxBytes cap is
// threaded in as a parameter so this package has no dependency on index).
package indexwatch

import (
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"time"

	"github.com/JayveerPrajapati/kern/internal/cache"
	"github.com/JayveerPrajapati/kern/internal/ignore"
	"github.com/JayveerPrajapati/kern/internal/langparse"
)

// ReadFile is a var so tests can swap in a counting wrapper to prove how many
// times file contents are read (staleness walks and update change-detection
// both read through it; stat-only walks do not). Exported because index-side
// tests count reads across both packages' seams.
var ReadFile = func(path string) ([]byte, error) {
	return os.ReadFile(path)
}

// IgnoreDirs is the walk-skip policy shared by the index's build walk and the
// freshness/hash walks in this package: directories never traversed because
// they hold VCS state, build output, tool state or machine-specific wiring —
// never project source.
var IgnoreDirs = map[string]bool{
	".git": true, ".hg": true, ".svn": true, "node_modules": true,
	"vendor": true, "dist": true, "build": true, "out": true, "target": true,
	".next": true, "__pycache__": true, ".venv": true, "venv": true,
	"__pypackages__": true, ".cache": true,
	".idea": true, "bin": true, ".mvn": true, "coverage": true, "tmp": true,
	".kern": true,
	// .blueprint holds blueprint's tool state (audit logs, approval requests,
	// metrics.json) which is rewritten on every check invocation. It is never
	// project source; leaving it indexable made every blueprint check flip the
	// index's content root (metrics.json is a .json source-extension file) and
	// forced a full index rebuild per invocation.
	".blueprint": true,
	// Agent/tooling config dirs: generated wiring (MCP endpoints, hooks,
	// rules) that is machine-specific and never project source.
	".opencode": true, ".claude": true, ".cursor": true, ".gemini": true,
	".kiro": true, ".codex": true, ".copilot": true, ".codeium": true,
	".qwen": true, ".qoder": true,
	// Additional surfaces `kern setup` writes into projects: .agents rules,
	// .continue/.windsurf configs and .vscode/mcp.json (MCP registry with
	// machine-local paths).
	".agents": true, ".continue": true, ".windsurf": true, ".vscode": true,
	// Generated graph/artifact dumps from the graphify skill and similar
	// tools: multi-MB JSON/HTML that is never project source and can hang
	// the foreign-language parser on large graphs (51MB+ graph.json files).
	"graphify-out": true,
	// Agent-arena scratch worktrees (the arena skill's run dirs): throwaway
	// benchmark/escape fixtures that are never project source — excluding
	// them keeps the index content root stable and the sandbox snapshot cap
	// un-tripped (same class as graphify-out).
	".arena": true,
}

// IndexableMaxMtime walks root without reading file contents and returns the
// newest modification time (Unix nanos) among indexable files plus their count.
// The stat-only selection matches the content walk (WalkQuickExt): extension
// is a proxy for indexability, but extension-less shebang scripts are
// indexable via content sniffing, so they count here too — a stat walk that
// dropped them would report a MaxMtime lower than the newest indexed file and
// let a stale index slip past the mtime gate. A walk error is returned so
// callers can decide to rebuild instead of serving a stale index. The ignore
// matcher mirrors Build's file-selection policy so gitignored files never
// influence the staleness decision.
func IndexableMaxMtime(root string, ign *ignore.Matcher) (int64, int, error) {
	// The stat walk shares the content walk's admission rules but applies no
	// size cap (the mtime gate counts every indexable file), so MaxInt64 is
	// the neutral cap. Parallelizing the stats preserves count and maxMtime.
	entries, err := collectEntries(root, ign, math.MaxInt64)
	if err != nil {
		return 0, 0, err
	}
	var maxMtime int64
	for _, e := range entries {
		if mt := e.info.ModTime().UnixNano(); mt > maxMtime {
			maxMtime = mt
		}
	}
	return maxMtime, len(entries), nil
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
			if path != root && IgnoreDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil || !langparse.QuickExt(rel) {
			return nil
		}
		data, derr := ReadFile(path)
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

// IndexableHashes walks root and returns the content hash of every indexable
// file (path -> SHA-256), the same file selection Build uses. File reads and
// hashes are distributed over a worker pool: a few thousand small files cost
// hundreds of milliseconds sequentially, and the result is a map, so
// parallelism changes nothing observable. Directory traversal and stats run
// on a parallel walk (ignore.Matcher is concurrency-safe) that hands files to
// this pool in deterministic order; workers only read and hash accepted
// files. Any read error aborts the hash set (first error wins,
// same contract as the sequential version). A walk or read error is returned
// rather than silently producing a partial map, which could wrongly mark an
// edited tree as unchanged. maxBytes is the adaptive per-file size cap the
// build resolved (the same tunables value), threaded in so this package does
// not depend on index's tunables machinery.
func IndexableHashes(root string, ign *ignore.Matcher, maxBytes int64) (map[string]string, error) {
	obs, err := IndexableHashesObserved(root, ign, maxBytes)
	return obs.Hashes, err
}

// WalkObservation is what one indexable-walk learned about the tree.
type WalkObservation struct {
	Hashes    map[string]string // content-indexable files → SHA-256
	Mtimes    map[string]int64  // same files → mtime (Unix nanos)
	Sizes     map[string]int64  // same files → size in bytes
	WalkCount int               // files the walk VISITED (quickExt+ignore filter)
}

// IndexableHashesObserved is IndexableHashes plus the per-file mtime and
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
// re-reads. The maps are nil on error; WalkCount is valid only on nil err.
func IndexableHashesObserved(root string, ign *ignore.Matcher, maxBytes int64) (WalkObservation, error) {
	obs := WalkObservation{
		Hashes: map[string]string{},
		Mtimes: map[string]int64{},
		Sizes:  map[string]int64{},
	}
	var (
		mu   sync.Mutex
		ferr error
	)
	workers := walkWorkers()
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
				data, derr := ReadFile(j.path)
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
				obs.Hashes[j.rel] = h
				obs.Mtimes[j.rel] = j.mtime
				obs.Sizes[j.rel] = j.size
				mu.Unlock()
			}
		}()
	}
	err := WalkQuickExt(root, ign, maxBytes, func(rel, path string, info fs.FileInfo) error {
		obs.WalkCount++
		jobs <- job{path: path, rel: rel, mtime: info.ModTime().UnixNano(), size: info.Size()}
		return nil
	})
	close(jobs)
	wg.Wait()
	if err != nil {
		return WalkObservation{}, err
	}
	if ferr != nil {
		return WalkObservation{}, ferr
	}
	return obs, nil
}

// WalkQuickExt walks root with exactly the filter set the content walk
// (IndexableHashesObserved) uses — IgnoreDirs, .gitignore/.kernignore,
// quickExt — and reports each surviving file's FileInfo. Shared by the
// hashing walk and the stat-gate walk so the two counts are comparable.
// fn runs sequentially on the caller's goroutine in sorted rel-path order,
// so callers keep single-threaded callback semantics; a non-nil return
// aborts the remaining dispatch.
//
// The per-file selection must be BYTE-FOR-BYTE the build walk's
// (walkIndexable): both accept extension-less files (shebang scripts) that
// langparse.QuickExt alone would drop, both skip non-regular files, and both
// apply the same maxBytes size cap. A divergence makes a file the build
// recorded invisible to the proof walk (or vice versa) — a permanent
// false-STALE verdict that sends every freshness check into an
// ensure-fresh rebuild loop. maxBytes is the adaptive default the build
// resolves (resolveTunables), so both sides pick the same file set.
func WalkQuickExt(root string, ign *ignore.Matcher, maxBytes int64, fn func(rel, path string, info fs.FileInfo) error) error {
	entries, err := collectEntries(root, ign, maxBytes)
	if err != nil {
		return err
	}
	// Callbacks run one at a time, in sorted rel-path order, over the entries
	// the parallel I/O phase collected. A callback error (e.g.
	// snapshotStillCurrent's mismatch sentinel) stops dispatch immediately.
	for _, e := range entries {
		if err := fn(e.rel, e.path, e.info); err != nil {
			return err
		}
	}
	return nil
}

// walkEntry is one admitted file discovered by the parallel walk.
type walkEntry struct {
	rel, path string
	info      fs.FileInfo
}

// walkWorkers bounds walk I/O concurrency: min(NumCPU, 8) overlaps directory
// reads and stats on a cold freshness check without swamping the filesystem.
func walkWorkers() int {
	n := runtime.NumCPU()
	if n > 8 {
		n = 8
	}
	if n < 1 {
		n = 1
	}
	return n
}

// collectEntries applies WalkQuickExt's admission rules over a worker pool
// and returns the surviving files sorted by rel path. Workers read
// directories and stat files; the sort makes both the admitted set and the
// eventual callback order independent of goroutine scheduling. Any I/O error
// discards the whole walk (a partial set is never trusted); the
// lexicographically smallest failing directory is reported so the error is
// deterministic too.
func collectEntries(root string, ign *ignore.Matcher, maxBytes int64) ([]walkEntry, error) {
	var (
		mu       sync.Mutex
		cond     sync.Cond
		queue    = []string{root}
		active   int
		entries  []walkEntry
		firstErr error
		errPath  string
	)
	cond.L = &mu
	workers := walkWorkers()
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			for {
				mu.Lock()
				// Wait while other workers still hold directories in flight;
				// when active hits zero there is no work left to announce.
				for len(queue) == 0 && active > 0 {
					cond.Wait()
				}
				if len(queue) == 0 {
					mu.Unlock()
					return
				}
				dir := queue[len(queue)-1]
				queue = queue[:len(queue)-1]
				active++
				mu.Unlock()

				subs, found, err := scanDir(root, dir, ign, maxBytes)

				mu.Lock()
				if err != nil && (firstErr == nil || dir < errPath) {
					firstErr, errPath = err, dir
				}
				entries = append(entries, found...)
				queue = append(queue, subs...)
				active--
				cond.Broadcast()
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].rel < entries[j].rel })
	return entries, nil
}

// scanDir applies WalkQuickExt's per-entry admission to one directory and
// returns the subdirectories to visit plus the admitted files. It mirrors
// filepath.WalkDir's callback: IgnoreDirs and ignore patterns prune
// directories and files, extension-less files are kept for content sniffing,
// non-regular files are dropped, and the maxBytes cap matches the build walk.
func scanDir(root, dir string, ign *ignore.Matcher, maxBytes int64) ([]string, []walkEntry, error) {
	des, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, err
	}
	var (
		subs  []string
		files []walkEntry
	)
	for _, d := range des {
		path := filepath.Join(dir, d.Name())
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			continue
		}
		if d.IsDir() {
			if IgnoreDirs[d.Name()] {
				continue
			}
			// Honor .gitignore/.kernignore directory patterns, as Build does.
			if ign != nil && ign.Ignored(filepath.ToSlash(rel)) {
				continue
			}
			subs = append(subs, path)
			continue
		}
		if d.Name() == ".git" || d.Name() == ".kern" {
			continue
		}
		// Honor .gitignore/.kernignore file patterns, as Build does.
		if ign != nil && ign.Ignored(filepath.ToSlash(rel)) {
			continue
		}
		// Same acceptance rule as walkIndexable: an extension-less file
		// (shebang script) is indexable via content sniffing even though
		// QuickExt (extension-based) would drop it.
		if !langparse.QuickExt(rel) && filepath.Ext(rel) != "" {
			continue
		}
		// Skip non-regular files (FIFOs, sockets, device nodes) — a FIFO
		// named like a source file would otherwise block the read.
		if !d.Type().IsRegular() {
			continue
		}
		info, ierr := d.Info()
		if ierr != nil {
			return nil, nil, ierr
		}
		// Same size cap as the build walk: a file over maxBytes is skipped at
		// build time, so the proof walk must skip it too or the aggregate
		// hash can never match (permanent false-STALE).
		if info.Size() > maxBytes {
			continue
		}
		files = append(files, walkEntry{rel: rel, path: path, info: info})
	}
	return subs, files, nil
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

// Adaptive poll-interval thresholds for the watcher. Small change sets
// (fewer than adaptSmall files) poll at the base interval so follow-up edits
// are picked up quickly; larger change sets back off by one scale unit per
// hundred changed files (adaptScalePerHundred), capped at adaptMaxScale,
// because the rebuild that follows a big change set is itself expensive; an
// idle tree decays back toward the base interval. All thresholds are integer
// counts of changed files per poll cycle.
const (
	adaptSmall           = 20
	adaptScalePerHundred = 1
	adaptMaxScale        = 8
)

// AdaptiveInterval returns the interval for the NEXT watcher poll cycle given
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
// the polling cadence is fully predictable from the change history.
func AdaptiveInterval(base time.Duration, lastChanges int, lastInterval time.Duration) time.Duration {
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

// diff computes the change set (adds, modifies, removes) between two hash
// maps, sorted by file path for deterministic output.
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
// is returned rather than a silent partial result. maxBytes is the adaptive
// per-file size cap the build resolves, threaded in by the index wrapper.
func FileHashes(root string, maxBytes int64) (map[string]string, error) {
	return IndexableHashes(root, ignore.Load(root), maxBytes)
}

// Diff reports the change set (adds, modifies, removes) between two hash maps
// as produced by FileHashes. Exported for watcher implementations.
func Diff(prev, cur map[string]string) []Change {
	return diff(prev, cur)
}
