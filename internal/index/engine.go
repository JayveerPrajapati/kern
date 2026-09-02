package index

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/JayveerPrajapati/kern/internal/cache"
	"github.com/JayveerPrajapati/kern/internal/ignore"
	"github.com/JayveerPrajapati/kern/internal/metrics"
)

// indexVersion is bumped whenever the persisted index schema changes, so
// stale caches are rebuilt automatically instead of serving zero-value fields.
const indexVersion = 11

// maxFileBytes is the largest file the index will read into memory and scan.
// Larger files (e.g. generated .json, bundled .min.js) are skipped to avoid
// loading huge blobs into RAM and regex-parsing them.
const maxFileBytes = 10 * 1024 * 1024

// parallelMinJobs is the smallest job count worth a worker pool. Below it,
// pool + ordered-merge overhead exceeds the per-file parse cost, so
// buildParallel applies jobs serially instead (byte-identical result).
const parallelMinJobs = 256

// Index is the in-memory representation of a project's AST index.
type Index struct {
	Root    string              `json:"root"`
	Version int                 `json:"version"`
	Symbols []Symbol            `json:"symbols"`
	Calls   map[string][]string `json:"calls"`
	Callers map[string][]string `json:"callers"`
	// AliasCallers maps a bare name to callers of dotted callees with that bare
	// name ("Println" -> callers of "fmt.Println"); it never contributes callers
	// to a resolved local symbol.
	AliasCallers map[string][]string `json:"alias_callers,omitempty"`
	// Inherits maps a subtype's full name to its bases, each tagged with the
	// edge kind ("extends:Animal", "implements:Pet", "embeds:Base"). InheritedBy
	// is the reverse map (base -> subtypes) for find-implementations queries.
	Inherits    map[string][]string `json:"inherits,omitempty"`
	InheritedBy map[string][]string `json:"inherited_by,omitempty"`
	Pkgs        map[string]*Pkg     `json:"packages"`
	// ImportsByFile maps a source file (relative path) to the imports of that
	// exact file. Unlike Pkgs[dir].Imports (package-aggregated), attribution is
	// per file, so guard's import-level boundary check can tell which changed
	// file actually imports a forbidden package. Populated by Build/extract;
	// absent in indexes written by older kern (guard then no-ops the
	// import-level check per file, fail-open).
	ImportsByFile  map[string][]string `json:"imports_by_file,omitempty"`
	FileHashes     map[string]string   `json:"file_hashes"`
	GeneratedFiles map[string]bool     `json:"generated_files,omitempty"`
	// Communities maps a symbol full name to its community label, populated by
	// the SQLite store's Load and by CommunityLabels on demand.
	Communities map[string]string `json:"communities,omitempty"`
	// PrecisionByLang records the highest edge-precision tier achieved per
	// language in this index. Values: "resolved" (cross-file binding
	// resolution via go/ast), "ast" (per-file AST/tree-sitter extraction,
	// name-heuristic cross-file), "heuristic" (regex). Drives --precision strict.
	PrecisionByLang map[string]string   `json:"precision_by_lang,omitempty"`
	SymbolsByFile   map[string][]Symbol `json:"-"`
	UpdatedAt       time.Time           `json:"updated_at"`
	// MaxMtime is the largest file modification time (Unix nanos) at build time.
	// Stale() uses it as a cheap generation gate before the exact hash check.
	MaxMtime int64 `json:"max_mtime,omitempty"`
	// Identity records the content-addressed build-time identity (content
	// root hash + best-effort git tree/commit). FreshnessProof/Stale compare
	// the live tree against it. Populated by Build; nil for indexes built by
	// older kern or hand-constructed in-memory indexes.
	Identity *IndexIdentity `json:"identity,omitempty"`
	// fileResults retains each file's computed result so a later
	// BuildWithOptions(WithPriorIndex) can skip re-parsing unchanged files.
	// Unexported: never serialized; a loaded index simply has none (reuse
	// falls back to a full parse).
	fileResults map[string]fileResult
	// reusedResults counts per-file results reused from a prior index in the
	// build that produced this one (0 for full builds). Exposed via
	// ReusedResults for diagnostics.
	reusedResults int
	// symbolIdx is the precomputed name -> symbols lookup for symbolsFor;
	// nil means symbolsFor falls back to the linear scan.
	symbolIdx map[string][]Symbol
}

// New returns an empty index rooted at root.
func New(root string) *Index {
	ix := &Index{
		Root:            root,
		Version:         indexVersion,
		Calls:           map[string][]string{},
		Callers:         map[string][]string{},
		Inherits:        map[string][]string{},
		InheritedBy:     map[string][]string{},
		Pkgs:            map[string]*Pkg{},
		ImportsByFile:   map[string][]string{},
		FileHashes:      map[string]string{},
		GeneratedFiles:  map[string]bool{},
		Communities:     map[string]string{},
		PrecisionByLang: map[string]string{},
		SymbolsByFile:   map[string][]Symbol{},
	}
	ix.initMaps()
	return ix
}

// initMaps ensures all map fields are non-nil. A corrupt or hand-edited
// index.json can leave maps nil after json.Unmarshal, causing panics when
// downstream code writes to them. Safe to call on an already-initialized index.
func (ix *Index) initMaps() {
	if ix.Calls == nil {
		ix.Calls = map[string][]string{}
	}
	if ix.Callers == nil {
		ix.Callers = map[string][]string{}
	}
	if ix.Inherits == nil {
		ix.Inherits = map[string][]string{}
	}
	if ix.InheritedBy == nil {
		ix.InheritedBy = map[string][]string{}
	}
	if ix.Pkgs == nil {
		ix.Pkgs = map[string]*Pkg{}
	}
	if ix.ImportsByFile == nil {
		ix.ImportsByFile = map[string][]string{}
	}
	if ix.FileHashes == nil {
		ix.FileHashes = map[string]string{}
	}
	if ix.GeneratedFiles == nil {
		ix.GeneratedFiles = map[string]bool{}
	}
	if ix.Communities == nil {
		ix.Communities = map[string]string{}
	}
	if ix.PrecisionByLang == nil {
		ix.PrecisionByLang = map[string]string{}
	}
	if ix.SymbolsByFile == nil {
		ix.SymbolsByFile = map[string][]Symbol{}
	}
	if ix.fileResults == nil {
		ix.fileResults = map[string]fileResult{}
	}
}

// StorePath returns the on-disk location for the index of root.
// The index lives per-project under <root>/.kern/ so it is portable,
// self-contained, and never pollutes a global cache.
func StorePath(root string) string {
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = root
	}
	return filepath.Join(abs, ".kern", "index.json")
}

// Save persists the index. The write is atomic (temp file + rename) so a
// concurrent reader never observes a partially-written index. The temp file
// is uniquely named (not a fixed .tmp path) so concurrent writers (watch
// daemon + CLI) don't race on the same temp file and corrupt the index.
func (ix *Index) Save() error {
	data, err := json.Marshal(ix)
	if err != nil {
		return err
	}
	p := StorePath(ix.Root)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	// Unique temp file avoids the race where two processes both write to
	// p + ".tmp" and one truncates the other's bytes before rename.
	f, err := os.CreateTemp(filepath.Dir(p), ".kern-index-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := f.Name()
	defer os.Remove(tmpPath) // no-op if rename succeeded
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, p)
}

// Load reads the index for root. Returns nil if absent.
func Load(root string) (*Index, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = root
	}
	data, err := os.ReadFile(StorePath(abs))
	if err != nil {
		metrics.Default().RecordCacheMiss()
		return nil, err
	}
	ix := &Index{}
	if err := json.Unmarshal(data, ix); err != nil {
		metrics.Default().RecordCacheMiss()
		return nil, err
	}
	if ix.Version != indexVersion {
		metrics.Default().RecordCacheMiss()
		return nil, fmt.Errorf("index version %d (want %d): rebuild required", ix.Version, indexVersion)
	}
	ix.initMaps()
	ix.reindexByFile()
	metrics.Default().RecordCacheHit()
	return ix, nil
}

// Stale reports whether a source file was added, removed, or edited since the
// index was built, so intel never serves out-of-date call graphs. The
// authoritative verdict comes from FreshnessProof (git tree OID, falling back
// to a content re-hash); the stat gate is kept only to reject cheaply on a
// file-count change.
func (ix *Index) Stale() bool {
	if ix == nil || len(ix.FileHashes) == 0 {
		return true
	}
	// No recorded identity (in-memory test index, or hand-built Index struct):
	// fall back to the pre-identity content-hash comparison.
	if ix.Identity == nil {
		return ix.legacyStale()
	}
	// Load ignore patterns so gitignored files are excluded from the staleness
	// decision, matching the file set Build indexed. Without this, a gitignored
	// file would keep the gate/hash counts different from FileHashes and Stale
	// would report true forever, defeating the cache.
	ign := ignore.Load(ix.Root)
	// Cheap rejection: a different indexable-file count proves files were
	// added or removed — no git round-trip needed. An mtime mismatch alone is
	// deliberately NOT rejected: touching a file changes its mtime without
	// changing its content, so only the git/content check below can decide
	// those cases (and an mtime-preserving edit must never be served fresh).
	if ix.MaxMtime > 0 {
		if _, count, err := indexableMaxMtime(ix.Root, ign); err == nil && count != len(ix.FileHashes) {
			return true
		}
	}
	return ix.FreshnessProof(ix.Root).Stale()
}

// legacyStale is the pre-identity staleness check: the mtime fast gate plus
// an exact content-hash walk. Retained for indexes with a nil Identity (e.g.
// in-memory test indexes constructed without a Build) where there is no
// FreshnessProof baseline to compare against. Note the gate here returns
// "not stale" on a match, so mtime-preserving edits evade it — acceptable for
// the defensive path only, never for persisted indexes.
func (ix *Index) legacyStale() bool {
	ign := ignore.Load(ix.Root)
	// Fast gate: identical file count and newest mtime mean nothing changed, so
	// skip re-hashing. An mtime-preserving edit (rare) evades the gate.
	if ix.MaxMtime > 0 {
		if maxMtime, count, err := indexableMaxMtime(ix.Root, ign); err == nil && count == len(ix.FileHashes) && maxMtime == ix.MaxMtime {
			return false
		}
	}
	cur, err := indexableHashes(ix.Root, ign)
	if err != nil {
		return true
	}
	if len(cur) != len(ix.FileHashes) {
		return true
	}
	for f, h := range cur {
		if ph, ok := ix.FileHashes[f]; !ok || ph != h {
			return true
		}
	}
	return false
}

// LoadFile reads an index directly from a store path (used for
// cross-project search across the cache directory).
func LoadFile(path string) (*Index, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	ix := &Index{}
	if err := json.Unmarshal(data, ix); err != nil {
		return nil, err
	}
	if ix.Version != indexVersion {
		return nil, fmt.Errorf("index version %d (want %d): rebuild required", ix.Version, indexVersion)
	}
	ix.initMaps()
	ix.reindexByFile()
	return ix, nil
}

func (ix *Index) reindexByFile() {
	ix.SymbolsByFile = map[string][]Symbol{}
	for _, s := range ix.Symbols {
		ix.SymbolsByFile[s.File] = append(ix.SymbolsByFile[s.File], s)
	}
}

// Languages returns the distinct languages present in the index, sorted.
func (ix *Index) Languages() []string {
	set := map[string]bool{}
	for _, s := range ix.Symbols {
		if s.Lang != "" {
			set[s.Lang] = true
		}
	}
	var out []string
	for l := range set {
		out = append(out, l)
	}
	sort.Strings(out)
	return out
}

var ignoreDirs = map[string]bool{
	".git": true, ".hg": true, ".svn": true, "node_modules": true,
	"vendor": true, "dist": true, "build": true, "out": true, "target": true,
	".next": true, "__pycache__": true, ".venv": true, ".cache": true,
	".idea": true, "bin": true, ".mvn": true, "coverage": true, "tmp": true,
	".kern": true,
	// Agent/tooling config dirs: generated wiring (MCP endpoints, hooks,
	// rules) that is machine-specific and never project source.
	".opencode": true, ".claude": true, ".cursor": true, ".gemini": true,
	".kiro": true, ".codex": true, ".copilot": true, ".codeium": true,
	".qwen": true, ".qoder": true,
	// Generated graph/artifact dumps from the graphify skill and similar
	// tools: multi-MB JSON/HTML that is never project source and can hang
	// the foreign-language parser on large graphs (51MB+ graph.json files).
	"graphify-out": true,
}

// IgnoredDir reports whether a directory name is skipped during index walks
// (node_modules, vendor, build artifacts, ...). Exported for downstream
// scanners that mirror the index's file-selection policy.
func IgnoredDir(name string) bool { return ignoreDirs[name] }

// Build walks root, parses every source file and assembles the index. On
// error it returns a nil index so a half-built index is never mistaken for
// a usable one.
// Build walks root, parses every source file and assembles the index. On
// error it returns a nil index so a half-built index is never mistaken for
// a usable one.
//
// The expensive per-file work (ReadFile, hashing, language detection, AST
// extraction) is parallelized across CPU cores by default (buildParallel):
// workers compute each file's result independently and the main goroutine
// folds results back in lexical file order, so the merged index is
// byte-identical to the serial build. Set KERN_INDEX_SERIAL=1 to force the
// original single-threaded path (buildSerial) for A/B testing and ops
// diagnostics.
// BuildOption customizes BuildWithOptions.
type BuildOption func(*buildConfig)

// buildConfig carries the options resolved for one build run. reused is
// written by build workers (atomically) to count prior-result reuse.
type buildConfig struct {
	prior  *Index
	reused atomic.Int64
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
	var cfg buildConfig
	for _, o := range opts {
		o(&cfg)
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
	c.Imports = append([]string(nil), p.Imports...)
	return &c
}

// fileModTimeNanos returns the file's modification time in Unix nanoseconds,
// or 0 when the stat fails (mirroring the serial build, where a failed stat
// simply leaves MaxMtime untouched for that file).
func fileModTimeNanos(d fs.DirEntry) int64 {
	if info, ierr := d.Info(); ierr == nil {
		return info.ModTime().UnixNano()
	}
	return 0
}

// buildSerial is the original single-threaded build: it walks root, parses
// every source file in lexical walk order and assembles the index. It is the
// byte-for-byte reference behavior that buildParallel must reproduce (its
// output is what kern's freshness/identity proofs compare against).
func buildSerial(abs string, cfg *buildConfig) (*Index, error) {
	ix := New(abs)
	// Load .gitignore + .kernignore patterns so gitignored directories
	// (e.g. graphify-out/, dist/, large generated trees) are skipped during
	// the index walk. Without this, the index scans every file on disk
	// regardless of .gitignore, producing huge symbol counts and slow builds.
	ign := ignore.Load(abs)
	err := filepath.WalkDir(abs, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != abs && ignoreDirs[d.Name()] {
				return filepath.SkipDir
			}
			// Honor .gitignore/.kernignore directory patterns.
			if path != abs {
				if rel, rerr := filepath.Rel(abs, path); rerr == nil {
					if ign.Ignored(filepath.ToSlash(rel)) {
						return filepath.SkipDir
					}
				}
			}
			return nil
		}
		rel, rerr := filepath.Rel(abs, path)
		if rerr != nil {
			return err
		}
		// Honor .gitignore/.kernignore file patterns.
		if ign.Ignored(filepath.ToSlash(rel)) {
			return nil
		}
		if !quickExt(rel) && filepath.Ext(rel) != "" {
			return nil
		}
		// Skip files larger than maxFileBytes before reading them so huge
		// generated/bundled files never get loaded into memory or scanned.
		if info, ierr := d.Info(); ierr == nil && info.Size() > maxFileBytes {
			return nil
		}
		if r, ok := reuseByMtime(cfg.prior, rel, fileModTimeNanos(d)); ok {
			cfg.reused.Add(1)
			ix.applyFileResult(r)
			return nil
		}
		src, serr := os.ReadFile(path)
		if serr != nil {
			// Skip unreadable files (e.g. broken symlinks) instead of aborting
			// the whole index build. Matches the sec scanner's behavior.
			return nil
		}
		if !isIndexable(rel, src) {
			return nil
		}
		ix.applyFileResult(reuseOrCompute(cfg.prior, rel, src, fileModTimeNanos(d), &cfg.reused))
		return nil
	})
	if err != nil {
		return nil, err
	}
	ix.UpdatedAt = time.Now().UTC()
	ix.buildSymbolIndex()
	ix.computeCallers()
	ix.addDispatchEdges()
	ix.resolveEntries()
	ix.reindexByFile()
	// Record the edge-precision tier per language so strict call-edge following
	// (kern guard/impact --precision strict) can skip edges whose caller
	// language is not fully resolved instead of guessing at their meaning.
	ix.computePrecisionByLang()
	// Content-addressed identity: the file walk is complete, so FileHashes and
	// MaxMtime are final. The identity is what FreshnessProof later compares
	// the live tree against; persisted by Save().
	ix.Identity = buildIdentity(abs, ix.FileHashes, ix.UpdatedAt)
	return ix, nil
}

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
// extraction) across runtime.GOMAXPROCS(0) workers:
//
//  1. Phase 1 walks the tree serially, applying the exact same skip policy as
//     the serial build (ignoreDirs, ignore patterns, quickExt, maxFileBytes)
//     and collecting one job per accepted file in lexical walk order. No file
//     contents are read here.
//  2. Phase 2 runs a fixed pool of worker goroutines. Workers only claim job
//     indices via an atomic counter and produce fileResults — they never touch
//     the index. This is the central safety property of the parallel build.
//  3. Phase 3, in the main goroutine (the only goroutine that mutates ix),
//     reorders the results by seq and folds them in lexical order via
//     applyFileResult, then runs the same finalize passes as buildSerial.
//
// Byte-identical output vs buildSerial is the acceptance criterion: kern's
// freshness/identity proofs compare the persisted index against the live tree,
// so any ordering divergence would defeat them.
func buildParallel(abs string, cfg *buildConfig) (*Index, error) {
	ix := New(abs)
	// Load .gitignore + .kernignore patterns so gitignored directories
	// (e.g. graphify-out/, dist/, large generated trees) are skipped during
	// the index walk. Without this, the index scans every file on disk
	// regardless of .gitignore, producing huge symbol counts and slow builds.
	ign := ignore.Load(abs)

	// Phase 1: serial walk collecting jobs.
	var jobs []fileJob
	err := filepath.WalkDir(abs, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != abs && ignoreDirs[d.Name()] {
				return filepath.SkipDir
			}
			// Honor .gitignore/.kernignore directory patterns.
			if path != abs {
				if rel, rerr := filepath.Rel(abs, path); rerr == nil {
					if ign.Ignored(filepath.ToSlash(rel)) {
						return filepath.SkipDir
					}
				}
			}
			return nil
		}
		rel, rerr := filepath.Rel(abs, path)
		if rerr != nil {
			return nil
		}
		// Honor .gitignore/.kernignore file patterns.
		if ign.Ignored(filepath.ToSlash(rel)) {
			return nil
		}
		if !quickExt(rel) && filepath.Ext(rel) != "" {
			return nil
		}
		// Skip files larger than maxFileBytes before reading them so huge
		// generated/bundled files never get loaded into memory or scanned.
		if info, ierr := d.Info(); ierr == nil && info.Size() > maxFileBytes {
			return nil
		}
		jobs = append(jobs, fileJob{seq: len(jobs), rel: rel, path: path, mtime: fileModTimeNanos(d)})
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Phase 2: worker pool. Workers are pure — they never touch ix; every ix
	// mutation happens only in the main goroutine's merge loop below.
	// Small trees: pool + ordered-merge overhead exceeds the parse cost, so
	// apply jobs serially in lexical order (byte-identical to the pool path).
	if len(jobs) < parallelMinJobs {
		for _, j := range jobs {
			if r, ok := reuseByMtime(cfg.prior, j.rel, j.mtime); ok {
				cfg.reused.Add(1)
				ix.applyFileResult(r)
				continue
			}
			src, serr := os.ReadFile(j.path)
			if serr != nil {
				continue // unreadable: same semantics as the serial build
			}
			if !isIndexable(j.rel, src) {
				continue
			}
			ix.applyFileResult(reuseOrCompute(cfg.prior, j.rel, src, j.mtime, &cfg.reused))
		}
	} else {
		workers := runtime.GOMAXPROCS(0)
		if workers < 1 {
			workers = 1
		}
		results := make(chan fileResult, workers)
		var wg sync.WaitGroup
		var next int64
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					idx := atomic.AddInt64(&next, 1) - 1
					if idx >= int64(len(jobs)) {
						return
					}
					job := jobs[idx]
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
					if !isIndexable(job.rel, src) {
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
		for r := range results {
			pending[r.seq] = r
			for {
				r2, ok := pending[nextSeq]
				if !ok {
					break
				}
				if !r2.readErr && !r2.skip {
					ix.applyFileResult(r2)
				}
				delete(pending, nextSeq)
				nextSeq++
			}
		}
	}
	ix.UpdatedAt = time.Now().UTC()
	ix.buildSymbolIndex()
	ix.computeCallers()
	ix.addDispatchEdges()
	ix.resolveEntries()
	ix.reindexByFile()
	// Record the edge-precision tier per language so strict call-edge following
	// (kern guard/impact --precision strict) can skip edges whose caller
	// language is not fully resolved instead of guessing at their meaning.
	ix.computePrecisionByLang()
	// Content-addressed identity: the file walk is complete, so FileHashes and
	// MaxMtime are final. The identity is what FreshnessProof later compares
	// the live tree against; persisted by Save().
	ix.Identity = buildIdentity(abs, ix.FileHashes, ix.UpdatedAt)
	return ix, nil
}

// computePrecisionByLang records the highest edge-precision tier achieved per
// language present in the index. go/ast and Java resolve cross-file bindings
// ("resolved"): Go via go/ast, Java via per-method local-type tracking +
// callee resolution (java_resolve.go: v.method() -> Type.method() binds
// against symbols cross-file). Java's regex extractor reaches "resolved"; under
// the tree-sitter build call edges are still receiver-var heuristics, so the
// tier stays "ast" there to keep strict precision honest. Other foreign
// languages are "ast" under the tree-sitter build and "heuristic" (regex)
// otherwise.
func (ix *Index) computePrecisionByLang() {
	ix.PrecisionByLang = map[string]string{}
	for _, lang := range ix.Languages() {
		switch lang {
		case "go":
			ix.PrecisionByLang[lang] = "resolved"
		case "java":
			if treesitterEnabled() {
				ix.PrecisionByLang[lang] = "ast"
			} else {
				ix.PrecisionByLang[lang] = "resolved"
			}
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
	calls     map[string][]string
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
		generated: IsGeneratedPath(rel) || isGeneratedContent(src),
	}
	lang := detectLang(rel, src)
	if lang == "go" {
		var err error
		r.syms, r.calls, r.inherits, r.pkg, err = extract(rel, src)
		if err != nil {
			r.parseErr = true
		}
	} else {
		r.syms, r.calls, r.inherits, r.pkg, _ = extractForeign(rel, src, lang)
	}
	return r
}

// applyFileResult folds one file's computed result into the index. It is the
// only place (besides the finalize passes) that mutates the index, and it is
// called strictly in lexical file order — from the serial build's walk and
// from the parallel build's ordered merge loop — which is what keeps the
// merged index byte-identical across the two paths.
func (ix *Index) applyFileResult(r fileResult) {
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
		return
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
		ix.Calls[owner] = append(ix.Calls[owner], callees...)
	}
	for subtype, bases := range r.inherits {
		ix.Inherits[subtype] = append(ix.Inherits[subtype], bases...)
	}
	if r.pkg != nil {
		if existing, ok := ix.Pkgs[r.pkg.Path]; ok {
			existing.Files = append(existing.Files, r.pkg.Files...)
			// Merge imports from every file of the package, not just the first
			// indexed one. Without this, guard's import-level boundary check
			// only ever sees the first file's imports.
			for _, imp := range r.pkg.Imports {
				if !slices.Contains(existing.Imports, imp) {
					existing.Imports = append(existing.Imports, imp)
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
			// append([]string{}, pkg.Imports...) keeps a non-nil empty slice for
			// files with no imports so they serialize as [] (not null) in index.json.
			ix.ImportsByFile[file] = append([]string{}, r.pkg.Imports...)
		}
	}
}

func (ix *Index) computeCallers() {
	ix.Callers = map[string][]string{}
	ix.AliasCallers = map[string][]string{}
	for caller, callees := range ix.Calls {
		for _, c := range callees {
			if c == caller {
				continue
			}
			// Canonical edge: recorded under the exact callee key.
			ix.Callers[c] = append(ix.Callers[c], caller)
			// Merge package-qualified local calls ("db.Open") onto the local
			// symbol when the qualifier names its package dir; foreign and
			// unresolved targets never merge (avoids forging callers).
			if d, ok := qualifiedCalleeSymbol(ix, c); ok && d.FullName() != c {
				ix.Callers[d.FullName()] = append(ix.Callers[d.FullName()], caller)
			}
			// Also record a dotted callee under its bare name so simple-name
			// lookups find foreign or unresolved targets. Aliases stay in a
			// separate map, never merged into a local symbol's callers, and a
			// caller is never aliased to itself.
			if simple := simpleKey(c); simple != c && simple != caller {
				ix.AliasCallers[simple] = append(ix.AliasCallers[simple], caller)
			}
		}
	}
	for k := range ix.Callers {
		ix.Callers[k] = dedupeSorted(ix.Callers[k])
	}
	for k := range ix.AliasCallers {
		ix.AliasCallers[k] = dedupeSorted(ix.AliasCallers[k])
	}
	// Reverse inheritance map: base name (and bare name) -> subtypes.
	ix.InheritedBy = map[string][]string{}
	for subtype, taggedBases := range ix.Inherits {
		for _, tb := range taggedBases {
			base := strings.TrimPrefix(tb, "extends:")
			base = strings.TrimPrefix(base, "implements:")
			base = strings.TrimPrefix(base, "embeds:")
			ix.InheritedBy[base] = append(ix.InheritedBy[base], subtype)
		}
	}
	for k := range ix.InheritedBy {
		ix.InheritedBy[k] = dedupeSorted(ix.InheritedBy[k])
	}
}

// addDispatchEdges adds virtual call edges from a method call on an interface
// or abstract type to every concrete implementation of that method, so DI call
// sites reach the code that actually runs.
func (ix *Index) addDispatchEdges() {
	if len(ix.InheritedBy) == 0 {
		return
	}

	// Build a set of all symbol full names for quick membership checks.
	symSet := map[string]bool{}
	methodsByReceiver := map[string]map[string]bool{} // receiver -> set of method names
	for _, s := range ix.Symbols {
		fn := s.FullName()
		symSet[fn] = true
		if s.Receiver != "" {
			if methodsByReceiver[s.Receiver] == nil {
				methodsByReceiver[s.Receiver] = map[string]bool{}
			}
			methodsByReceiver[s.Receiver][s.Name] = true
		}
	}

	// For each interface/abstract type that has implementers, add virtual edges
	// to each implementer's method of the same name.
	added := map[string]bool{} // dedupe key "caller->virtualCallee"
	for caller, callees := range ix.Calls {
		for _, c := range callees {
			// Parse "Receiver.method" to find the receiver and method name.
			dot := strings.LastIndex(c, ".")
			if dot < 0 || dot == 0 {
				continue
			}
			receiver := c[:dot]
			method := c[dot+1:]
			if receiver == "" || method == "" {
				continue
			}

			implementers := ix.InheritedBy[receiver]
			if len(implementers) == 0 {
				continue
			}

			for _, impl := range implementers {
				virtualCallee := impl + "." + method
				if !symSet[virtualCallee] {
					continue
				}
				key := caller + "->" + virtualCallee
				if added[key] {
					continue
				}
				added[key] = true
				ix.Calls[caller] = append(ix.Calls[caller], virtualCallee)
				ix.Callers[virtualCallee] = append(ix.Callers[virtualCallee], caller)
			}
		}
	}

	// Dedupe after adding virtual edges.
	for k := range ix.Calls {
		ix.Calls[k] = dedupeSorted(ix.Calls[k])
	}
	for k := range ix.Callers {
		ix.Callers[k] = dedupeSorted(ix.Callers[k])
	}
}

func dedupeSorted(in []string) []string {
	sort.Strings(in)
	out := in[:0]
	for i, s := range in {
		if i == 0 || s != in[i-1] {
			out = append(out, s)
		}
	}
	return out
}

// symbolsFor returns every symbol whose bare or full name matches. Build
// paths precompute symbolIdx, making this O(1); without it (hand-built or
// older in-memory indexes) it degrades to the original linear scan.
// Callers only iterate the result — the cached slices are shared.
func (ix *Index) symbolsFor(name string) []Symbol {
	if ix.symbolIdx != nil {
		return ix.symbolIdx[name]
	}
	var out []Symbol
	for _, s := range ix.Symbols {
		if s.Name == name || s.FullName() == name {
			out = append(out, s)
		}
	}
	return out
}

// buildSymbolIndex precomputes the name -> symbols map that turns
// symbolsFor from a full-index linear scan into a map lookup. Called by
// the build paths before the finalize passes (computeCallers and
// addDispatchEdges call symbolsFor per call edge, which made finalize
// O(edges x symbols) — the dominant build cost on symbol-heavy repos).
// Per-name slice order matches the linear scan's (append in Symbols
// order), so results are identical.
func (ix *Index) buildSymbolIndex() {
	ix.symbolIdx = make(map[string][]Symbol, len(ix.Symbols)*2)
	for _, s := range ix.Symbols {
		ix.symbolIdx[s.Name] = append(ix.symbolIdx[s.Name], s)
		if fn := s.FullName(); fn != s.Name {
			ix.symbolIdx[fn] = append(ix.symbolIdx[fn], s)
		}
	}
}

// FindSymbol returns the first symbol matching name, exact on Name or
// FullName ("Type.Method"). ok is false when nothing matches.
func (ix *Index) FindSymbol(name string) (Symbol, bool) {
	if defs := ix.symbolsFor(name); len(defs) > 0 {
		return defs[0], true
	}
	return Symbol{}, false
}

// IsGenerated reports whether the file at root-relative path was marked as
// tool-generated at index time (path convention or a "Code generated" banner
// in its head). It is a ranking hint, not a hard claim.
func (ix *Index) IsGenerated(rel string) bool {
	return ix.GeneratedFiles != nil && ix.GeneratedFiles[rel]
}

// ResolveName finds a definition for a call target. Exact matches win; a
// package-qualified target like "index.Build" falls back to the bare name
// ("Build") so call sites still resolve to real definitions.
func (ix *Index) ResolveName(name string) (Symbol, bool) {
	return resolveName(ix, name)
}

// Search matches symbols by pattern. Patterns support "*" wildcards and the
// prefixes "func ", "type ", "struct ", "method ", "const ", "var ", "call ".
func (ix *Index) Search(pattern string, limit int) []Symbol {
	if limit <= 0 {
		limit = 50
	}
	re, kind := symbolRegex(pattern)
	var out []Symbol
	for _, s := range ix.Symbols {
		if symbolMatches(s, kind, re) {
			out = append(out, s)
			if len(out) >= limit {
				break
			}
		}
	}
	return out
}

func symbolRegex(pattern string) (*regexp.Regexp, string) {
	p := pattern
	kind := ""
	if i := strings.IndexByte(p, ' '); i > 0 {
		prefix := p[:i]
		switch prefix {
		case "func", "method", "struct", "interface", "type", "const", "var",
			"class", "enum", "trait", "module", "union", "impl", "prop", "heading", "entry":
			kind = prefix
			p = p[i+1:]
		}
	}
	expr := "^" + strings.ReplaceAll(regexp.QuoteMeta(p), `\*`, `.*`) + "$"
	return regexp.MustCompile(expr), kind
}

func symbolMatches(s Symbol, kind string, re *regexp.Regexp) bool {
	if kind == "entry" {
		return s.Entry && (re.MatchString(s.Name) || (s.Receiver != "" && re.MatchString(s.Receiver+"."+s.Name)) ||
			(s.Route != "" && re.MatchString(s.Route)))
	}
	if kind == "type" {
		// "type" is a super-category matching class, interface, enum, record,
		// struct, trait, and union kinds, not just the Go-specific "type" kind.
		if !searchTypeKinds[s.Kind] {
			return false
		}
	} else if kind != "" && s.Kind != kind {
		return false
	}
	return re.MatchString(s.Name) || (s.Receiver != "" && re.MatchString(s.Receiver+"."+s.Name)) ||
		(s.Route != "" && re.MatchString(s.Route))
}

// searchTypeKinds is the set of symbol kinds matched by the "type" search
// prefix. It is a superset of the parser's typeKinds (which is used for call
// graph construction and must not include "type" or "record").
var searchTypeKinds = map[string]bool{
	"type": true, "class": true, "interface": true, "struct": true,
	"enum": true, "record": true, "trait": true, "union": true,
}

// CallersOf returns the functions that call a given symbol name.
func (ix *Index) CallersOf(symbol string) []string {
	return ix.Callers[symbol]
}

// CallSites returns the call edges of a symbol (what it calls).
func (ix *Index) CallSites(symbol string) []string {
	return ix.Calls[symbol]
}

// simpleKey returns the part of a recorded callee key after the last '.'
// ("" for a plain name).
func simpleKey(c string) string {
	if i := strings.LastIndexByte(c, '.'); i >= 0 {
		return c[i+1:]
	}
	return c
}

// qualifiedCalleeSymbol resolves a dotted callee key ("db.Open") to the local
// symbol whose package directory matches the qualifier. It returns ok=false
// for foreign targets ("fmt.Println") and unresolved receiver calls ("v.M"),
// whose callers must never be attributed to a local symbol of the same name.
func qualifiedCalleeSymbol(ix *Index, c string) (Symbol, bool) {
	i := strings.LastIndexByte(c, '.')
	if i <= 0 || i+1 >= len(c) {
		return Symbol{}, false
	}
	qualifier, bare := c[:i], c[i+1:]
	for _, d := range ix.symbolsFor(bare) {
		if filepath.Base(filepath.Dir(d.File)) == qualifier {
			return d, true
		}
	}
	return Symbol{}, false
}

// CallersFor returns deduplicated callers attributable to a local symbol. Only
// the exact key ("Type.Method" for methods, the plain name otherwise) is
// consulted: bare-name aliases are never merged in, since a simple name can
// name many symbols (Alpha.Save vs Beta.Save) or a foreign target.
func (ix *Index) CallersFor(s Symbol) []string {
	return dedupeSorted(ix.Callers[s.FullName()])
}

// CallersOfName returns callers for a possibly-unknown name (a foreign target
// like "fmt.Println", or an unresolved receiver call like "v.M"). Exact
// entries win; simple-name aliases are only consulted when the name matches
// no local symbol, so a local Println never inherits callers of fmt.Println.
func (ix *Index) CallersOfName(name string) []string {
	if len(ix.symbolsFor(name)) > 0 {
		return dedupeSorted(ix.Callers[name])
	}
	if exact := ix.Callers[name]; len(exact) > 0 {
		return dedupeSorted(exact)
	}
	return dedupeSorted(ix.AliasCallers[name])
}

// CallsFor returns deduplicated callees recorded under the exact key of s.
func (ix *Index) CallsFor(s Symbol) []string {
	return dedupeSorted(ix.Calls[s.FullName()])
}

// edgeKeys returns the map keys under which a symbol's inheritance edges may
// be recorded: the bare name and, for methods, the "Type.Method" form.
// Inheritance keys only ever store type names (FullName == Name), so both
// forms coincide.
func edgeKeys(s Symbol) []string {
	if fn := s.FullName(); fn != s.Name {
		return []string{s.Name, fn}
	}
	return []string{s.Name}
}

// SupertypesOf returns the inheritance/implementation bases of a symbol as
// tagged edges ("extends:Animal", "implements:Pet", "embeds:Base"), deduped
// under any key form of s.
func (ix *Index) SupertypesOf(s Symbol) []string {
	var out []string
	for _, k := range edgeKeys(s) {
		out = append(out, ix.Inherits[k]...)
	}
	return dedupeSorted(out)
}

// SubtypesOf returns the symbols that extend/implement a base, deduped under
// any key form of the base.
func (ix *Index) SubtypesOf(s Symbol) []string {
	var out []string
	for _, k := range edgeKeys(s) {
		out = append(out, ix.InheritedBy[k]...)
	}
	return dedupeSorted(out)
}

// Graph renders the neighbourhood of a symbol: definition, callers, and what
// it calls.
func (ix *Index) Graph(symbol string) string {
	var b strings.Builder
	defs := ix.symbolsFor(symbol)
	if len(defs) == 0 {
		if d, ok := resolveName(ix, symbol); ok {
			defs = []Symbol{d}
		} else {
			b.WriteString("no symbol found: " + symbol)
			return b.String()
		}
	}
	root := defs[0]
	for _, d := range defs {
		b.WriteString("def ")
		b.WriteString(d.Kind)
		b.WriteString(" ")
		b.WriteString(d.FullName())
		b.WriteString(" ")
		b.WriteString(d.File)
		b.WriteString(":")
		b.WriteString(strconv.Itoa(d.Line))
		b.WriteString("\n")
	}
	callers := ix.CallersFor(root)
	if len(callers) > 0 {
		b.WriteString("callers:\n")
		for _, c := range callers {
			b.WriteString("  ")
			b.WriteString(c)
			if _, ok := resolveName(ix, c); !ok {
				b.WriteString("  (unresolved)")
			}
			b.WriteString("\n")
		}
	}
	callees := ix.CallsFor(root)
	if len(callees) > 0 {
		b.WriteString("calls:\n")
		for _, c := range callees {
			b.WriteString("  ")
			b.WriteString(c)
			if _, ok := resolveName(ix, c); !ok {
				b.WriteString("  (unresolved)")
			}
			b.WriteString("\n")
		}
	}
	result := strings.TrimSuffix(b.String(), "\n")
	stats := ix.TokenSavingsForGraph(root.File, result)
	if summary := stats.Summary(); summary != "" {
		result += "\n\n" + summary
	}
	return result
}

// Context returns the minimal slice of source an agent needs about a symbol:
// its definition source, its callers, and what it calls.
func (ix *Index) Context(symbol string, linesAround int) string {
	if linesAround <= 0 {
		linesAround = 12
	}
	defs := ix.symbolsFor(symbol)
	if len(defs) == 0 {
		if d, ok := resolveName(ix, symbol); ok {
			defs = []Symbol{d}
		} else {
			return ""
		}
	}
	var b strings.Builder
	d := defs[0]
	src, err := os.ReadFile(filepath.Join(ix.Root, d.File))
	if err != nil {
		return ""
	}
	all := strings.Split(string(src), "\n")
	start := d.Line - linesAround
	if start < 1 {
		start = 1
	}
	end := d.Line + linesAround
	if end > len(all) {
		end = len(all)
	}
	for i := start; i <= end; i++ {
		b.WriteString(strconv.Itoa(i))
		b.WriteString(": ")
		b.WriteString(all[i-1])
		b.WriteString("\n")
	}
	callers := ix.CallersFor(d)
	if len(callers) > 0 {
		b.WriteString("\ncallers: ")
		b.WriteString(strings.Join(callers, ", "))
		b.WriteString("\n")
	}
	if callees := ix.CallsFor(d); len(callees) > 0 {
		b.WriteString("calls: ")
		b.WriteString(strings.Join(callees, ", "))
		b.WriteString("\n")
	}
	result := strings.TrimSuffix(b.String(), "\n")
	stats := ix.TokenSavingsForContext(d.File, result)
	if summary := stats.Summary(); summary != "" {
		result += "\n\n" + summary
	}
	return result
}
