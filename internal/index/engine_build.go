package index

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/JayveerPrajapati/kern/internal/cache"
	"github.com/JayveerPrajapati/kern/internal/ignore"
	"github.com/JayveerPrajapati/kern/internal/langparse"
	"github.com/JayveerPrajapati/kern/internal/metrics"
)

// BuildOption customizes BuildWithOptions.
type BuildOption func(*buildConfig)

// buildConfig carries the options resolved for one build run. reused is
// written by build workers (atomically) to count prior-result reuse.
//
// workers and maxBytes are the resource-adaptive tunables. Zero means "use
// the machine-derived default" (see resources.go); a non-zero explicit
// option always wins over the adaptive default.
type buildConfig struct {
	javaCompiler  bool
	javaClasspath string
	goTypes       bool
	prior         *Index
	reused        atomic.Int64
	// workers is the parse/scan worker pool size (0 = adaptive default).
	workers int
	// maxBytes is the largest file the index will read and scan
	// (0 = adaptive default).
	maxBytes int64
	// resolvedWorkers reports the pool size the build actually used, set
	// by buildSerial (always 1) and buildParallel (small-tree bypass = 1,
	// pool = resolved tunables) for the Index.BuildStats report. Zero when
	// neither ran yet.
	resolvedWorkers int
}

func WithGoTypes() BuildOption {
	return func(config *buildConfig) { config.goTypes = true }
}

func WithJavaCompiler(classpath string) BuildOption {
	return func(config *buildConfig) { config.javaCompiler, config.javaClasspath = true, classpath }
}

// WithPriorIndex reuses per-file parse results from a prior index of the
// same root whenever a file's content hash is unchanged. Parsing (not
// hashing or walking) dominates build time on large trees, so unchanged
// files skip the expensive part entirely. The produced index is
// equivalent to a full rebuild — including MaxMtime, which takes the
// fresh stat even for reused files — so freshness proofs and staleness
// detection behave identically.
func WithPriorIndex(prior *Index) BuildOption {
	return func(c *buildConfig) { c.prior = prior }
}

// WithWorkers sets the parse/scan worker pool size for parallel builds. A
// non-positive value (or omitting the option) selects the machine-derived
// default: min(clamp(NumCPU, 1, 32), total RAM / 512 MiB). An explicit
// value always wins over the adaptive default.
func WithWorkers(n int) BuildOption {
	return func(c *buildConfig) { c.workers = n }
}

// WithMaxFileBytes sets the largest file (in bytes) the index will read and
// scan; larger files are skipped. A non-positive value (or omitting the
// option) selects the memory-scaled default (10 MiB floor, 64 MiB cap). An
// explicit value always wins over the adaptive default.
func WithMaxFileBytes(n int64) BuildOption {
	return func(c *buildConfig) { c.maxBytes = n }
}

// Build walks root and produces a full AST index (all files parsed).
func Build(root string) (*Index, error) {
	return BuildWithOptions(root)
}

// BuildWithOptions walks root and produces an AST index, honoring opts
// (e.g. WithPriorIndex for incremental rebuilds).
func BuildWithOptions(root string, opts ...BuildOption) (*Index, error) {
	start := time.Now()
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	// A missing or non-directory root is a caller error, not an empty repo:
	// indexing it used to walk nothing and persist an empty index (and, with
	// no .kern dir, could stall on the build lock). Fail fast instead.
	if info, statErr := os.Stat(abs); statErr != nil {
		return nil, fmt.Errorf("index: root %s: %w", abs, statErr)
	} else if !info.IsDir() {
		return nil, fmt.Errorf("index: root %s is not a directory", abs)
	}
	var cfg buildConfig
	for _, o := range opts {
		o(&cfg)
	}
	if cfg.prior != nil && cfg.prior.Root == abs && cfg.prior.GoTypes {
		cfg.goTypes = true
	}
	if cfg.prior != nil && cfg.prior.Root == abs && cfg.prior.JavaCompiler && !cfg.javaCompiler {
		cfg.javaCompiler, cfg.javaClasspath = true, cfg.prior.JavaClasspath
	}
	// A prior index from a different root never matches a relative path
	// set, but guard explicitly so a mistaken caller cannot silently
	// reuse cross-project results.
	if cfg.prior != nil && cfg.prior.Root != "" && cfg.prior.Root != abs {
		cfg.prior = nil
	}
	var ix *Index
	if os.Getenv("KERN_INDEX_SERIAL") == "1" {
		ix, err = buildSerial(abs, &cfg)
	} else {
		ix, err = buildParallel(abs, &cfg)
	}
	if err != nil {
		return nil, err
	}
	ix.reusedResults = int(cfg.reused.Load())
	ix.BuildStats = BuildStats{
		Kind:    "build",
		Ms:      time.Since(start).Milliseconds(),
		Workers: cfg.resolvedWorkers,
		Reused:  int(cfg.reused.Load()),
	}
	metrics.Default().RecordIndexBuild(time.Since(start))
	return ix, nil
}

// ReusedResults reports how many per-file parse results were reused from
// a prior index (0 for full builds).
func (ix *Index) ReusedResults() int { return ix.reusedResults }

// reuseByMtime returns the prior result for rel when the file's mtime is
// unchanged, skipping the read + hash + parse entirely. This is the same
// trust model Stale() already uses (MaxMtime as the generation gate
// before any hashing): a content edit that does not bump mtime already
// evades staleness detection. The hash path (reuseOrCompute) remains the
// exact check for files whose mtime moved.
func reuseByMtime(prior *Index, rel string, mtime int64) (fileResult, bool) {
	if prior == nil || prior.fileResults == nil {
		return fileResult{}, false
	}
	pr, ok := prior.fileResults[rel]
	if !ok || pr.mtime != mtime {
		return fileResult{}, false
	}
	pr.seq = 0 // caller assigns the merge sequence
	pr.pkg = copyPkg(pr.pkg)
	return pr, true
}

// reuseOrCompute returns the prior index's fileResult for rel when the
// content hash matches (the parse is skipped — the dominant cost), or
// freshly computes one. mtime always comes from the current stat so a
// touched-but-unchanged file keeps MaxMtime equivalent to a full rebuild.
func reuseOrCompute(prior *Index, rel string, src []byte, mtime int64, reused *atomic.Int64) fileResult {
	if prior != nil && prior.fileResults != nil {
		if pr, ok := prior.fileResults[rel]; ok && pr.hash == cache.Hash(src) {
			pr.mtime = mtime
			pr.seq = 0 // caller assigns the merge sequence
			// Copy the pkg again: the new build's package merging mutates
			// the Pkg it stores in ix.Pkgs, and that must never reach the
			// prior index's stored copy (the prior stays live for its own
			// readers and future rebuilds).
			pr.pkg = copyPkg(pr.pkg)
			reused.Add(1)
			return pr
		}
	}
	return computeFileResult(rel, src, mtime)
}

// copyPkg returns a deep copy of p. A Pkg's Files/Imports slices are
// mutated when same-package files merge, so sharing one Pkg between an
// index, its per-file results, and a later incremental build corrupts
// all of them.
func copyPkg(p *Pkg) *Pkg {
	if p == nil {
		return nil
	}
	c := *p
	c.Files = append([]string(nil), p.Files...)
	c.Imports = append([]ImportEdge(nil), p.Imports...)
	c.Exports = append([]ExportEdge(nil), p.Exports...)
	if p.StructFields != nil {
		c.StructFields = make(map[string]string, len(p.StructFields))
		for k, v := range p.StructFields {
			c.StructFields[k] = v
		}
	}
	if p.Constructors != nil {
		c.Constructors = make(map[string]string, len(p.Constructors))
		for k, v := range p.Constructors {
			c.Constructors[k] = v
		}
	}
	return &c
}

// walkIndexable walks root applying the index's file-selection policy —
// ignoreDirs, .gitignore/.kernignore patterns, quickExt, regular-file and
// maxFileBytes caps — and calls fn for every accepted file in lexical walk
// order. It is the single source of truth for which files an index covers,
// shared by buildSerial, buildParallel and Update so the incremental path
// never diverges from a full rebuild on skip rules.
func walkIndexable(root string, ign *ignore.Matcher, maxBytes int64, fn func(rel, path string, mtime int64) error) error {
	// When the tree contains Go modules, a .go file outside every module is
	// not buildable (the classic stray test harness at a repo root above
	// src/go.mod). Indexing it invents a duplicate "." package (QA
	// 2026-10-26) and feeds phantom symbols to search/blast radius. Trees
	// with no go.mod at all keep the historical index-everything behavior.
	var moduleRoots []string
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		// Only a root without its own module can contain stray Go files.
		// Normal module repos skip the extra directory pass entirely.
		moduleRoots = goModuleRoots(root, ign)
	}
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// The root itself failing (missing, not a dir, unreadable) is a
			// real indexing error — abort, never return an empty index.
			if path == root {
				return err
			}
			// A walk error is almost always one unreadable entry or
			// directory (permission denied mid-tree). Skip it and keep
			// indexing the rest — one protected directory must not abort
			// the whole index and every later search (campaign 2026-10-28,
			// ripgrep-style). The operator still hears about it:
			// one line on stderr, so the missing subtree is explainable.
			if d != nil && d.IsDir() {
				fmt.Fprintf(os.Stderr, "[kern] index: skipping unreadable directory %s: %v\n", path, err)
				return filepath.SkipDir
			}
			fmt.Fprintf(os.Stderr, "[kern] index: skipping unreadable entry %s: %v\n", path, err)
			return nil
		}
		if d.IsDir() {
			if path != root && ignoreDirs[d.Name()] {
				return filepath.SkipDir
			}
			// Honor .gitignore/.kernignore directory patterns.
			if path != root {
				if rel, rerr := filepath.Rel(root, path); rerr == nil {
					if ign.Ignored(filepath.ToSlash(rel)) {
						return filepath.SkipDir
					}
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
		// Honor .gitignore/.kernignore file patterns.
		if ign.Ignored(filepath.ToSlash(rel)) {
			return nil
		}
		if !langparse.QuickExt(rel) && filepath.Ext(rel) != "" {
			return nil
		}
		if filepath.Ext(rel) == ".go" && len(moduleRoots) > 0 && !underGoModule(path, moduleRoots) {
			return nil
		}
		// Skip non-regular files (FIFOs, sockets, device nodes) to avoid blocking reads.
		if !d.Type().IsRegular() {
			return nil
		}
		// Skip files larger than maxBytes before reading them so huge
		// generated/bundled files never get loaded into memory or scanned.
		// One d.Info() stat serves both the size cap and the mtime:
		// DirEntry.Info() re-stats on every call (no caching), so the
		// historical second call via fileModTimeNanos doubled the walk's
		// stat cost. A failed stat keeps the historical semantics: the
		// file is accepted with mtime 0 (MaxMtime untouched for it).
		info, ierr := d.Info()
		if ierr == nil && info.Size() > maxBytes {
			return nil
		}
		var mtime int64
		if ierr == nil {
			mtime = info.ModTime().UnixNano()
		}
		return fn(rel, path, mtime)
	})
}

// goModuleRoots returns every directory (absolute) that contains a go.mod,
// skipping ignored directories. Empty means the tree has no Go module.
type moduleImportPrefix struct {
	path string // go.mod "module" declaration
	dir  string // module root relative to the index root ("." or "src")
}

func (p moduleImportPrefix) dirFor(importPath string) (string, bool) {
	if importPath == p.path {
		return p.dir, true
	}
	if rest, ok := strings.CutPrefix(importPath, p.path+"/"); ok {
		return filepath.ToSlash(filepath.Join(p.dir, rest)), true
	}
	return "", false
}

// moduleImportPrefixes returns the module paths declared by every go.mod in
// the tree (root and nested modules) plus the module root's index-relative
// directory. Import paths starting with one of these are in-repo; everything
// else is stdlib/vendor, even when a local directory happens to share the
// qualifier (net/http vs a local http/ package).
func moduleImportPrefixes(root string) []moduleImportPrefix {
	var prefixes []moduleImportPrefix
	for _, modRoot := range goModuleRoots(root, nil) {
		data, err := os.ReadFile(filepath.Join(modRoot, "go.mod"))
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(root, modRoot)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
				if p := strings.TrimSpace(rest); p != "" {
					prefixes = append(prefixes, moduleImportPrefix{path: p, dir: filepath.ToSlash(rel)})
				}
				break
			}
		}
	}
	return prefixes
}

func goModuleRoots(root string, ign *ignore.Matcher) []string {
	var roots []string
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path != root && ignoreDirs[d.Name()] {
				return filepath.SkipDir
			}
			if path != root && ign != nil {
				if rel, rerr := filepath.Rel(root, path); rerr == nil && ign.Ignored(filepath.ToSlash(rel)) {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if d.Name() == "go.mod" {
			if rel, rerr := filepath.Rel(root, path); rerr != nil || ign == nil || !ign.Ignored(filepath.ToSlash(rel)) {
				roots = append(roots, filepath.Dir(path))
			}
		}
		return nil
	})
	return roots
}

func underGoModule(path string, moduleRoots []string) bool {
	path = filepath.Clean(path)
	for _, root := range moduleRoots {
		rel, err := filepath.Rel(root, path)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// buildSerial is the original single-threaded build: it walks root, parses
// every source file in lexical walk order and assembles the index. It is the
// byte-for-byte reference behavior that buildParallel must reproduce (its
// output is what kern's freshness/identity proofs compare against).
func buildSerial(abs string, cfg *buildConfig) (*Index, error) {
	ix := New(abs)
	cfg.resolvedWorkers = 1
	// Kick off the git identity observations (tree OID + commit) BEFORE the
	// walk so the ~0.5s git staging dance overlaps with the file walk instead
	// of running after it; joined once FileHashes are final below.
	gitID := startIdentityGit(abs)
	// Resolve resource-adaptive tunables once per build (workers/maxBytes
	// fall back to machine-derived defaults unless the caller set them).
	t := resolveTunables(cfg, resolveResources())
	// Load .gitignore + .kernignore patterns so gitignored directories
	// (e.g. graphify-out/, dist/, large generated trees) are skipped during
	// the index walk. Without this, the index scans every file on disk
	// regardless of .gitignore, producing huge symbol counts and slow builds.
	ign := ignore.Load(abs)
	err := walkIndexable(abs, ign, t.maxFileBytes, func(rel, path string, mtime int64) error {
		if r, ok := reuseByMtime(cfg.prior, rel, mtime); ok {
			cfg.reused.Add(1)
			return ix.applyFileResult(r)
		}
		src, serr := os.ReadFile(path)
		if serr != nil {
			// Skip unreadable files (e.g. broken symlinks) instead of aborting
			// the whole index build. Matches the sec scanner's behavior.
			return nil
		}
		if !langparse.IsIndexable(rel, src) {
			return nil
		}
		return ix.applyFileResult(reuseOrCompute(cfg.prior, rel, src, mtime, &cfg.reused))
	})
	if err != nil {
		return nil, err
	}
	ix.UpdatedAt = time.Now().UTC()
	ix.buildSymbolIndex()
	if cfg.goTypes {
		if err := ix.bindGoTypes(); err != nil {
			return nil, err
		}
	}
	if cfg.javaCompiler {
		if err := ix.bindJavaCompiler(cfg.javaClasspath); err != nil {
			return nil, err
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
	// Record the edge-precision tier per language so strict call-edge following
	// (kern guard/impact --precision strict) can skip edges whose caller
	// language is not fully resolved instead of guessing at their meaning.
	ix.computePrecisionByLang()
	// Content-addressed identity: the file walk is complete, so FileHashes and
	// MaxMtime are final. The identity is what FreshnessProof later compares
	// the live tree against; persisted by Save(). The git half of the identity
	// was captured concurrently with the walk (startIdentityGit above).
	ix.Identity = gitID.joinIdentity(ix.FileHashes, ix.UpdatedAt)
	return ix, nil
}

// reorderWindow bounds how far ahead of the merge cursor buildParallel's
// workers may claim jobs (B7). Results received out of order must be buffered
// until the head of the run arrives; without the window, a single slow file at
// the head of the walk would make the pending map hold O(files) results
// (roughly 2x index peak memory). With the window, pending stays bounded by
// reorderWindow plus in-flight workers. Output stays byte-identical: the merge
// still applies results strictly in seq order.
const reorderWindow = 1024

// reorderBackoff is how long a worker sleeps when the reorder window is
// full (its claim index runs ahead of the merge cursor, which is waiting
// on a slow head-of-line file). A yield-only spin (runtime.Gosched) burned
// CPU for the whole stall on big trees; the merge loop keeps draining
// results and advances the window in bounded time, and 100µs is far below
// per-file parse latency, so the sleep costs nothing real.
const reorderBackoff = 100 * time.Microsecond

// fileJob is one accepted file discovered by buildParallel's phase-1 walk.
// seq follows the lexical walk order; the merge loop replays results in seq
// order so the merged index matches buildSerial byte for byte.
type fileJob struct {
	seq   int
	rel   string
	path  string
	mtime int64
}

// buildParallel assembles the same index as buildSerial but parallelizes the
// expensive per-file work (ReadFile, hashing, language detection, AST
// extraction) across a resource-adaptive worker pool — by default
// min(clamp(NumCPU, 1, 32), total RAM / 512 MiB) workers, overridable with
// WithWorkers; see resources.go:
//
//  1. Phase 1 walks the tree serially, applying the exact same skip policy as
//     the serial build (ignoreDirs, ignore patterns, quickExt, adaptive
//     maxFileBytes) and counting accepted files without storing them —
//     O(1) job memory. No file contents are read here.
//  2. Phase 2 walks the tree again, streaming one job per accepted file in
//     lexical walk order into a bounded channel consumed by a fixed pool of
//     worker goroutines. Workers never touch the index, and the feeder holds
//     at most reorderWindow jobs ahead of the merge cursor so the reorder
//     buffer stays bounded (B7) — the central safety property of the
//     parallel build. Small trees skip the pool and apply serially.
//  3. Phase 3, in the main goroutine (the only goroutine that mutates ix),
//     reorders the results by seq and folds them in lexical order via
//     applyFileResult, then runs the same finalize passes as buildSerial.
//
// Byte-identical output vs buildSerial is the acceptance criterion: kern's
// freshness/identity proofs compare the persisted index against the live tree,
// so any ordering divergence would defeat them.
func buildParallel(abs string, cfg *buildConfig) (*Index, error) {
	ix := New(abs)
	// Kick off the git identity observations (tree OID + commit) BEFORE the
	// walk so the ~0.5s git staging dance overlaps with the file walk instead
	// of running after it; joined once FileHashes are final below.
	gitID := startIdentityGit(abs)
	// Resolve resource-adaptive tunables once per build. The serial and
	// parallel paths resolve the same profile, so their file-selection
	// policy and merge behavior stay byte-identical.
	t := resolveTunables(cfg, resolveResources())
	// Load .gitignore + .kernignore patterns so gitignored directories
	// (e.g. graphify-out/, dist/, large generated trees) are skipped during
	// the index walk. Without this, the index scans every file on disk
	// regardless of .gitignore, producing huge symbol counts and slow builds.
	ign := ignore.Load(abs)

	// Phase 1: count-only pre-walk. The pool-vs-serial decision needs the
	// total before workers start; counting without storing keeps the job list
	// from ever materializing (O(files) memory -> O(1)). The count applies
	// the exact same skip policy as the serial build via walkIndexable, so
	// the feed pass below selects byte-identical files.
	n := 0
	if err := walkIndexable(abs, ign, t.maxFileBytes, func(rel, path string, mtime int64) error {
		n++
		return nil
	}); err != nil {
		return nil, err
	}

	// Phase 2: worker pool fed by a bounded channel. Workers are pure — they
	// never touch ix; every ix mutation happens only in the main goroutine's
	// merge loop below. Small trees: pool + ordered-merge overhead exceeds
	// the parse cost, so apply jobs serially in lexical order (byte-identical
	// to the pool path).
	if n < t.parallelMin {
		cfg.resolvedWorkers = 1
		seq := 0
		if err := walkIndexable(abs, ign, t.maxFileBytes, func(rel, path string, mtime int64) error {
			j := fileJob{seq: seq, rel: rel, path: path, mtime: mtime}
			seq++
			if r, ok := reuseByMtime(cfg.prior, j.rel, j.mtime); ok {
				cfg.reused.Add(1)
				return ix.applyFileResult(r)
			}
			src, serr := os.ReadFile(j.path)
			if serr != nil {
				return nil // unreadable: same semantics as the serial build
			}
			if !langparse.IsIndexable(j.rel, src) {
				return nil
			}
			return ix.applyFileResult(reuseOrCompute(cfg.prior, j.rel, src, j.mtime, &cfg.reused))
		}); err != nil {
			return nil, err
		}
	} else {
		workers := t.workers
		cfg.resolvedWorkers = workers
		results := make(chan fileResult, t.resultBuf)
		jobs := make(chan fileJob, t.resultBuf)
		var wg sync.WaitGroup
		// applied tracks the merge cursor: the next seq the merge loop will
		// apply. The feeder streams at most reorderWindow jobs ahead of it,
		// so the reorder buffer stays bounded (B7).
		var applied atomic.Int64
		// Feeder: the second walk streams accepted files into the bounded job
		// channel as the pool consumes them, so parsing starts before the walk
		// ends and job memory stays O(1). The window check lives here — a
		// worker cannot return a job to the channel once pulled — and when the
		// in-flight count reaches reorderWindow the feeder backs off; the
		// merge loop keeps draining results and advances the window in bounded
		// time. A walk error is captured, the channel is closed so workers
		// drain, and the error is returned after the merge loop (the index is
		// discarded either way).
		var walkErr error
		go func() {
			defer close(jobs)
			seq := 0
			walkErr = walkIndexable(abs, ign, t.maxFileBytes, func(rel, path string, mtime int64) error {
				for int64(seq)-applied.Load() >= reorderWindow {
					time.Sleep(reorderBackoff)
				}
				jobs <- fileJob{seq: seq, rel: rel, path: path, mtime: mtime}
				seq++
				return nil
			})
		}()
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for job := range jobs {
					if r, ok := reuseByMtime(cfg.prior, job.rel, job.mtime); ok {
						r.seq = job.seq
						cfg.reused.Add(1)
						results <- r
						continue
					}
					src, serr := os.ReadFile(job.path)
					if serr != nil {
						// Skip unreadable files (e.g. broken symlinks) instead of
						// aborting the whole index build, matching the serial path
						// and the sec scanner's behavior.
						results <- fileResult{seq: job.seq, rel: job.rel, readErr: true}
						continue
					}
					if !langparse.IsIndexable(job.rel, src) {
						results <- fileResult{seq: job.seq, rel: job.rel, skip: true}
						continue
					}
					r := reuseOrCompute(cfg.prior, job.rel, src, job.mtime, &cfg.reused)
					r.seq = job.seq
					results <- r
				}
			}()
		}
		go func() {
			wg.Wait()
			close(results)
		}()

		// Phase 3: serial ordered merge in the main goroutine — the ONLY goroutine
		// that mutates ix. Results are replayed in lexical (seq) order, preserving
		// the append order that makes the merged index byte-identical to serial.
		pending := map[int]fileResult{}
		nextSeq := 0
		var firstErr error
		for r := range results {
			pending[r.seq] = r
			for {
				r2, ok := pending[nextSeq]
				if !ok {
					break
				}
				if !r2.readErr && !r2.skip {
					if err := ix.applyFileResult(r2); err != nil && firstErr == nil {
						firstErr = err
					}
				}
				delete(pending, nextSeq)
				nextSeq++
			}
			applied.Store(int64(nextSeq))
		}
		if walkErr != nil {
			return nil, walkErr
		}
		if firstErr != nil {
			return nil, firstErr
		}
	}
	ix.UpdatedAt = time.Now().UTC()
	ix.buildSymbolIndex()
	if cfg.goTypes {
		if err := ix.bindGoTypes(); err != nil {
			return nil, err
		}
	}
	if cfg.javaCompiler {
		if err := ix.bindJavaCompiler(cfg.javaClasspath); err != nil {
			return nil, err
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
	// Record the edge-precision tier per language so strict call-edge following
	// (kern guard/impact --precision strict) can skip edges whose caller
	// language is not fully resolved instead of guessing at their meaning.
	ix.computePrecisionByLang()
	// Content-addressed identity: the file walk is complete, so FileHashes and
	// MaxMtime are final. The identity is what FreshnessProof later compares
	// the live tree against; persisted by Save(). The git half of the identity
	// was captured concurrently with the walk (startIdentityGit above).
	ix.Identity = gitID.joinIdentity(ix.FileHashes, ix.UpdatedAt)
	return ix, nil
}

// computePrecisionByLang records the highest edge-precision tier achieved per
// language present in the index. go/ast and Java resolve cross-file bindings
// ("resolved"): Go via go/ast, Java via per-method local-type tracking +
// callee resolution (java_resolve.go: v.method() -> Type.method() binds
// against symbols cross-file) in BOTH extractor paths — the regex extractor
// (foreign_lang.go) and the tree-sitter build (treesitter_java.go
// resolveJavaCalls, added 2026-09-10). Other foreign languages are "ast"
// under the tree-sitter build and "heuristic" (regex) otherwise.
func (ix *Index) computePrecisionByLang() {
	ix.PrecisionByLang = map[string]string{}
	for _, lang := range ix.Languages() {
		switch lang {
		case "go", "java":
			ix.PrecisionByLang[lang] = "resolved"
		default:
			if treesitterEnabled() {
				ix.PrecisionByLang[lang] = "ast"
			} else {
				ix.PrecisionByLang[lang] = "heuristic"
			}
		}
	}
}

// fileResult carries the outcome of one file's per-file computation from a
// worker to the main goroutine's ordered merge. Workers fill every field
// except seq and never touch the index.
type fileResult struct {
	seq       int
	rel       string
	hash      string
	mtime     int64
	generated bool
	syms      []Symbol
	calls     map[string][]CallEdge
	inherits  map[string][]string
	pkg       *Pkg
	// parseErr marks a Go file whose extraction failed: its hash is still
	// recorded (staleness invariant) but its symbols/edges are dropped,
	// mirroring addFile's early return on parse error.
	parseErr bool
	// readErr marks a file that could not be read (e.g. broken symlink); it is
	// skipped exactly like the serial build skips unreadable files.
	readErr bool
	// skip marks a file that failed the post-read isIndexable check.
	skip bool
}

// computeFileResult does all the expensive per-file work that is a pure
// function of (rel, src, mtime): hashing, generated detection, language
// detection and symbol/edge extraction. It never touches the index, so it is
// safe to run concurrently in buildParallel's worker pool. The content hash is
// always set — even when parsing fails (parseErr) — preserving the staleness
// invariant that FileHashes covers every indexable file regardless of parse
// success.
func computeFileResult(rel string, src []byte, mtime int64) fileResult {
	r := fileResult{
		rel:       rel,
		hash:      cache.Hash(src),
		mtime:     mtime,
		generated: langparse.IsGeneratedPath(rel) || langparse.IsGeneratedContent(src),
	}
	lang := langparse.DetectLang(rel, src)
	if lang == "go" {
		var err error
		r.syms, r.calls, r.inherits, r.pkg, err = extract(rel, src)
		if err != nil {
			r.parseErr = true
		}
	} else {
		r.syms, r.calls, r.inherits, r.pkg, _ = langparse.ExtractForeign(rel, src, lang, tsExtract)
	}
	return r
}

// applyFileResult folds one file's computed result into the index. It is the
// only place (besides the finalize passes) that mutates the index, and it is
// called strictly in lexical file order — from the serial build's walk and
// from the parallel build's ordered merge loop — which is what keeps the
// merged index byte-identical across the two paths.
func (ix *Index) applyFileResult(r fileResult) error {
	// Several same-header inherent impl blocks in one file are legal Rust
	// (`impl Foo {}` twice): give each repeated block a distinct Outer
	// discriminator before the duplicate guard judges them.
	disambiguateSameHeaderImpls(r.syms)
	// Same-identity duplicate guard: the graph keys nodes by FullName, so a
	// same-(name, kind, receiver) duplicate on a different line would
	// silently collapse into one node. A duplicate identity is an extractor
	// anomaly worth hearing about — but refusing to index the WHOLE repo
	// over one file is the wrong failure mode: a legal-language pattern or
	// extractor bug then takes every kern command down with it. Quarantine
	// instead: keep the first declaration per identity, drop the later
	// duplicates, warn on stderr, count the drop on the index (surfaced by
	// `kern index --status`).
	if err := checkFileSymbolConflicts(r.rel, r.syms); err != nil {
		kept, droppedSyms := dropDuplicateIdentities(r.syms)
		ix.DroppedSymbolConflicts += len(droppedSyms)
		parts := make([]string, 0, len(droppedSyms))
		for i, s := range droppedSyms {
			if i == 5 {
				parts = append(parts, fmt.Sprintf("… +%d more", len(droppedSyms)-i))
				break
			}
			parts = append(parts, fmt.Sprintf("%q (%s) at line %d", s.Name, s.Kind, s.Line))
		}
		fmt.Fprintf(os.Stderr, "kern: index: quarantined %d duplicate symbol(s) in %s (first declaration kept): %s\n",
			len(droppedSyms), r.rel, strings.Join(parts, ", "))
		r.syms = kept
	}
	if r.mtime > ix.MaxMtime {
		ix.MaxMtime = r.mtime
	}
	// Record the content hash BEFORE the parse step. FreshnessProof's
	// indexableHashes hashes every indexable file (by extension/content,
	// not parse success), so FileHashes must cover the same set — including
	// files that fail to parse (e.g. broken.go in a test fixture). Recording
	// the hash after the parse-error early-return would exclude unparseable
	// files from FileHashes while indexableHashes includes them, causing a
	// permanent ContentRoot mismatch → false "stale" → ERROR.
	ix.FileHashes[r.rel] = r.hash
	// A file that failed to parse contributes nothing beyond its hash: symbols
	// from a broken file must never pollute the index. Mirrors addFile's early
	// return on parse error.
	if r.parseErr {
		return nil
	}
	if ix.GeneratedFiles == nil {
		ix.GeneratedFiles = map[string]bool{}
	}
	ix.GeneratedFiles[r.rel] = r.generated
	if ix.fileResults == nil {
		ix.fileResults = map[string]fileResult{}
	}
	// Store a deep copy of the pkg: the merge below (and later
	// same-package files) mutates the Pkg in ix.Pkgs in place, and the
	// stored result must stay the pristine per-file extraction so
	// WithPriorIndex reuse reproduces a fresh parse exactly.
	stored := r
	stored.pkg = copyPkg(r.pkg)
	ix.fileResults[r.rel] = stored
	ix.Symbols = append(ix.Symbols, r.syms...)
	for owner, callees := range r.calls {
		for _, edge := range callees {
			edge.SourceFile = r.rel
			ix.Calls[owner] = append(ix.Calls[owner], edge)
		}
	}
	for subtype, bases := range r.inherits {
		ix.Inherits[subtype] = append(ix.Inherits[subtype], bases...)
	}
	if r.pkg != nil {
		if existing, ok := ix.Pkgs[r.pkg.Path]; ok {
			existing.Files = append(existing.Files, r.pkg.Files...)
			// A Go file's package clause is the authoritative name for its
			// directory. Foreign-language extractors fall back to the
			// directory basename, which is "." at the repository root, so
			// when a non-Go file registers the shared root path first the
			// Go package name (e.g. "main") is silently clobbered and
			// package-name consumers like `kern entries` find nothing. Let
			// the Go name win when merging into a fallback-named package.
			if r.pkg.Lang == "go" && existing.Lang != "go" && r.pkg.Name != "" && existing.Name == filepath.Base(filepath.Dir(r.rel)) {
				existing.Name = r.pkg.Name
				existing.Lang = "go"
			}
			// Merge imports from every file of the package, not just the first
			// indexed one. Without this, guard's import-level boundary check
			// only ever sees the first file's imports.
			// Merge imports from every file of the package, not just the first
			// indexed one. Without this, guard's import-level boundary check
			// only ever sees the first file's imports. Dedupe by path so the
			// same import observed in several files keeps its first confidence
			// rather than duplicating the edge.
			for _, imp := range r.pkg.Imports {
				if !slices.ContainsFunc(existing.Imports, func(e ImportEdge) bool { return e.Path == imp.Path }) {
					existing.Imports = append(existing.Imports, imp)
				}
			}
			// Merge struct-field types so the merge-time callee rewrite can
			// complete receiver-field chains whose struct is declared in a
			// different file than the call.
			for k, v := range r.pkg.StructFields {
				if existing.StructFields == nil {
					existing.StructFields = map[string]string{}
				}
				if _, dup := existing.StructFields[k]; !dup {
					existing.StructFields[k] = v
				}
			}
			// Merge constructor return types so the merge-time callee rewrite can
			// complete constructor-assigned receiver chains whose constructor is
			// declared in a different package than the call.
			for k, v := range r.pkg.Constructors {
				if existing.Constructors == nil {
					existing.Constructors = map[string]string{}
				}
				if _, dup := existing.Constructors[k]; !dup {
					existing.Constructors[k] = v
				}
			}
		} else {
			ix.Pkgs[r.pkg.Path] = r.pkg
		}
		// Per-file import attribution: the extractor's pkg carries exactly
		// this file's imports (Go: pkg built from f.Imports; foreign: pkg from
		// the per-file import list). Record the file's own imports, never the
		// package-aggregated ones, so guard can attribute a forbidden import
		// to the file that actually imports it. Files with no imports still
		// get an (empty) entry, so guard can distinguish "indexed file without
		// imports" from "imports_by_file without index format" (old indexes).
		for _, file := range r.pkg.Files {
			// append([]ImportEdge{}, pkg.Imports...) keeps a non-nil empty slice
			// for files with no imports so they serialize as [] (not null) in
			// index.json.
			ix.ImportsByFile[file] = append([]ImportEdge{}, r.pkg.Imports...)
			if len(r.pkg.Exports) > 0 {
				if ix.ExportsByFile == nil {
					ix.ExportsByFile = map[string][]ExportEdge{}
				}
				ix.ExportsByFile[file] = append([]ExportEdge(nil), r.pkg.Exports...)
			}
		}
	}
	return nil
}
