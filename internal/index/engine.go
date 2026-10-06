package index

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/JayveerPrajapati/kern/internal/cache"
	"github.com/JayveerPrajapati/kern/internal/ignore"
	"github.com/JayveerPrajapati/kern/internal/metrics"
	"github.com/JayveerPrajapati/kern/internal/tokstats"
)

// indexVersion is bumped whenever the persisted index schema changes, so
// stale caches are rebuilt automatically instead of serving zero-value fields.
const indexVersion = 13

// Index is the in-memory representation of a project's AST index.
type Index struct {
	Root    string                `json:"root"`
	Version int                   `json:"version"`
	Symbols []Symbol              `json:"symbols"`
	Calls   map[string][]CallEdge `json:"calls"`
	Callers map[string][]string   `json:"callers"`
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
	ImportsByFile  map[string][]ImportEdge `json:"imports_by_file,omitempty"`
	FileHashes     map[string]string       `json:"file_hashes"`
	GeneratedFiles map[string]bool         `json:"generated_files,omitempty"`
	// Communities maps a symbol full name to its community label, populated by
	// the SQLite store's Load and by CommunityLabels on demand.
	Communities map[string]string `json:"communities,omitempty"`
	// PrecisionByLang records the highest edge-precision tier achieved per
	// language in this index. Values: "resolved" (cross-file binding
	// resolution via go/ast), "ast" (per-file AST/tree-sitter extraction,
	// name-heuristic cross-file), "heuristic" (regex). Drives --precision strict.
	PrecisionByLang map[string]string   `json:"precision_by_lang,omitempty"`
	SymbolsByFile   map[string][]Symbol `json:"-"`
	// PromotedLowEdges / UnresolvedLowEdges are the finalize-time
	// reconciliation counters (CG-P0-5): call edges whose target resolved
	// against the completed symbol table and was promoted from LOW to
	// MEDIUM, and LOW edges that remain genuinely unresolved. Surfaced in
	// kern_health; zero on indexes built before the pass existed.
	PromotedLowEdges   int `json:"promoted_low_edges,omitempty"`
	UnresolvedLowEdges int `json:"unresolved_low_edges,omitempty"`
	// CallResolution counts distinct call targets and how many of them fail
	// to resolve against the symbol table. Surfaced in `kern index
	// --status`: a high unresolved share on a real repo is usually external
	// stdlib/vendor targets or untyped dynamic-language receivers, which is
	// expected and correct — the number exists so the honest per-repo
	// resolution rate is visible and regressions are provable. Zero on
	// indexes built before this pass existed.
	CallResolution CallResStats `json:"call_resolution,omitempty"`
	// ProseVocab is the build-time inverted word→symbol table:
	// each prose word ("middleware", "retry") maps to the full names of
	// symbols whose name or defining directory matches that word. Built by
	// buildProseVocab in every finalize sequence and served by LookupProse;
	// nil on indexes built before this feature — lookup must handle nil
	// gracefully.
	ProseVocab map[string][]string `json:"prose_vocab,omitempty"`
	UpdatedAt  time.Time           `json:"updated_at"`
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

	// staleSnapshot retains the per-file content observation (hashes + mtimes)
	// from the most recent staleness check that ran a content walk on this
	// index, so the immediately following UpdateWithSnapshot can reuse the walk
	// instead of re-reading and re-hashing the whole tree. Cleared whenever a
	// staleness check decides WITHOUT a content walk (nil/empty index, git-OID
	// fast path, walk failure), so the retained snapshot always corresponds to
	// the walk the last verdict was based on. Unexported: never serialized.
	// staleMu guards staleSnapshot: staleness checks deliberately run off any
	// app-level lock and can fan out concurrently (e.g. web.freshGraph's
	// off-lock Stale() walk), so the retained observation pointer is only
	// written and read under this mutex. Published snapshots are immutable
	// after retention — only the pointer swaps.
	staleMu       sync.Mutex
	staleSnapshot *FreshnessSnapshot
	// reusedResults counts per-file results reused from a prior index in the
	// build that produced this one (0 for full builds). Exposed via
	// ReusedResults for diagnostics.
	reusedResults int
	// cache holds the lazily-built lookup tables: name -> symbols for
	// symbolsFor, and kind -> symbols for kind-filtered Search. The build and
	// update paths populate them eagerly (buildSymbolIndex, reindexByFile);
	// the load paths defer the O(symbols) construction to the first query that
	// needs it, so a process that loads an index and never queries (status,
	// staleness checks, a watcher waiting for changes) skips the passes
	// entirely. Each table is guarded by its own sync.Once inside the cache, so
	// a deferred build racing with concurrent readers after Load builds the
	// table exactly once and is race-free. Unexported: never serialized.
	cache *indexCache
}

// indexCache is the lazily-built lookup tables of an Index, allocated by
// initMaps so every constructed index (New, JSON/SQLite/snapshot Load) has
// one before it can be published to readers. The pointer indirection keeps
// the sync.Onces out of the Index struct itself, so copying an Index value
// (canonical in tests) never copies a lock. Each table is built from
// ix.Symbols at build time and never rebuilt afterwards: both builders are
// idempotent by construction (sync.Once), which also makes a repeated
// reindexByFile/buildSymbolIndex on an already-populated index a no-op.
type indexCache struct {
	symbolOnce sync.Once
	kindOnce   sync.Once
	symbolIdx  map[string][]Symbol
	kindIdx    map[string][]Symbol
}

// getCache returns the index's lookup cache. It is allocated by initMaps
// for every index this package constructs, so in practice this never
// allocates; the fallback covers hand-built Index values (tests) that never
// ran initMaps, in which case a throwaway cache is returned WITHOUT storing
// it on the index — keeping the first-access path free of unsynchronized
// writes (safe under concurrent readers).
func (ix *Index) getCache() *indexCache {
	if c := ix.cache; c != nil {
		return c
	}
	return &indexCache{}
}

// New returns an empty index rooted at root.
func New(root string) *Index {
	ix := &Index{
		Root:            root,
		Version:         indexVersion,
		Calls:           map[string][]CallEdge{},
		Callers:         map[string][]string{},
		Inherits:        map[string][]string{},
		InheritedBy:     map[string][]string{},
		Pkgs:            map[string]*Pkg{},
		ImportsByFile:   map[string][]ImportEdge{},
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
		ix.Calls = map[string][]CallEdge{}
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
		ix.ImportsByFile = map[string][]ImportEdge{}
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
	if ix.cache == nil {
		ix.cache = &indexCache{}
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
// Save additionally refuses to overwrite an on-disk index whose schema
// version is NEWER than this index's: a long-lived daemon or watcher started
// with an older binary holds its own in-memory index and would otherwise
// silently clobber a newer index.json written by a current binary — the
// indexVersion guard only runs at load time, not at save time.
func (ix *Index) Save() error {
	// SQLite-primary (default build): the concurrent WAL store is the
	// canonical write path for the persisted index, so a build/update writes
	// ONE format instead of three (JSON + gob snapshot + SQLite). The JSON
	// cache and its gob snapshot below are written only when SQLite is
	// compiled out (-tags nosqlite) or the SQLite write fails — the
	// persistence contract degrades to the legacy formats instead of
	// disappearing. JSON remains the read-migration path for caches written
	// by older kern (Load falls back to it).
	if SQLiteEnabled() {
		if serr := SaveSQLite(ix.Root, ix); serr == nil {
			return nil
		} else {
			log.Printf("kern index: sqlite persist failed for %s, falling back to the JSON cache: %v", ix.Root, serr)
		}
	}
	data, err := json.Marshal(ix)
	if err != nil {
		return err
	}
	p := StorePath(ix.Root)
	if v := onDiskVersion(p); v > ix.Version {
		return fmt.Errorf("refusing to overwrite index schema v%d with v%d: the on-disk index was written by a newer kern — restart this process so it loads the newer index", v, ix.Version)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	// Ensure the local repository ignores .kern via .git/info/exclude without
	// dirtying or requiring a tracked .gitignore file.
	ensureGitExclude(ix.Root)

	// Unique temp file avoids the race where two processes both write to
	// p + ".tmp" and one truncates the other's bytes before rename.
	f, err := os.CreateTemp(filepath.Dir(p), ".kern-index-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := f.Name()
	defer func() { _ = os.Remove(tmpPath) }() // no-op if rename succeeded
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, p); err != nil {
		return err
	}
	// Binary snapshot cache (Rec P0-3): written alongside the canonical JSON
	// so the next Load/LoadFile can skip the multi-MB JSON parse. Best
	// effort — a snapshot failure is non-fatal (JSON remains canonical).
	_ = writeBinSnapshot(ix, p)
	return nil
}

// onDiskVersion reads the "version" field of a persisted index without
// decoding the whole (potentially multi-MB) file. The field appears as the
// second member of the object (struct field order: Root, Version, ...), so
// scanning the first 4KB is deterministic for kern-written files. Returns 0
// when the file is absent, unreadable, or the field cannot be found — the
// Save guard then no-ops, preserving current behavior.
func onDiskVersion(p string) int {
	f, err := os.Open(p)
	if err != nil {
		return 0
	}
	defer func() { _ = f.Close() }()
	buf := make([]byte, 4096)
	n, _ := io.ReadFull(f, buf)
	if n == 0 {
		return 0
	}
	m := versionFieldRe.FindSubmatch(buf[:n])
	if m == nil {
		return 0
	}
	v, err := strconv.Atoi(string(m[1]))
	if err != nil {
		return 0
	}
	return v
}

// versionFieldRe matches the index's schema version member. A literal
// `"version":` cannot occur inside the Root string (paths cannot contain
// quotes), so the first match is the real field.
var versionFieldRe = regexp.MustCompile(`"version"\s*:\s*(\d+)`)

// ensureGitExclude guarantees that <root>/.git/info/exclude includes .kern/
// so that whenever kern indexes any repository, git never tracks or shows
// .kern in git status, without creating any git diffs in client/shared repos.
func ensureGitExclude(root string) {
	if root == "" {
		return
	}
	gitDir := filepath.Join(root, ".git")
	fi, err := os.Stat(gitDir)
	if err != nil {
		return
	}
	var infoDir string
	if fi.IsDir() {
		infoDir = filepath.Join(gitDir, "info")
	} else {
		// Could be a worktree or submodule pointing to a gitdir file:
		// "gitdir: /path/to/.git/worktrees/name"
		b, err := os.ReadFile(gitDir)
		if err != nil {
			return
		}
		line := strings.TrimSpace(string(b))
		if strings.HasPrefix(line, "gitdir:") {
			target := strings.TrimSpace(strings.TrimPrefix(line, "gitdir:"))
			if !filepath.IsAbs(target) {
				target = filepath.Join(root, target)
			}
			infoDir = filepath.Join(target, "info")
		} else {
			return
		}
	}
	_ = os.MkdirAll(infoDir, 0o755)
	excludePath := filepath.Join(infoDir, "exclude")
	b, _ := os.ReadFile(excludePath)
	content := string(b)
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == ".kern" || trimmed == ".kern/" {
			return // already excluded
		}
	}
	separator := "\n"
	if len(content) == 0 || strings.HasSuffix(content, "\n") {
		separator = ""
	}
	newContent := content + separator + "# kern local exclude\n.kern/\n"
	if err := os.WriteFile(excludePath, []byte(newContent), 0o644); err != nil {
		// Benign for indexing, but never invisible: without the entry git
		// tracks .kern/, so say why it is missing.
		log.Printf("kern index: could not add .kern/ to %s (git may show .kern as untracked): %v", excludePath, err)
	}
}

// Load reads the index for root. Returns nil if absent.
func Load(root string) (*Index, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = root
	}
	p := StorePath(abs)
	// Fast path (Rec P0-3): a fresh binary snapshot decodes ~5-10x faster
	// than the JSON document; JSON stays canonical and any miss/staleness
	// falls through to the existing path below. The snapshot is trusted only
	// while the JSON it mirrors is not out-dated by a newer SQLite store:
	// since Save became SQLite-primary, index.json is no longer rewritten, so
	// a legacy snapshot would otherwise keep matching its frozen index.json
	// stat and serve stale content forever.
	if !sqliteStoreNewerThanJSON(p) {
		if ix, ok := loadBinSnapshot(p); ok {
			if ix.Version != indexVersion {
				metrics.Default().RecordCacheMiss()
				return nil, fmt.Errorf("index version %d (want %d): rebuild required", ix.Version, indexVersion)
			}
			reRootIndex(ix, abs)
			ix.initMaps()
			// The lookup caches (symbolIdx, kindIdx) are deferred to the
			// first query that needs them; SymbolsByFile is the only map
			// built here because it is an exported field consumers read
			// directly after Load.
			ix.buildSymbolsByFile()
			metrics.Default().RecordCacheHit()
			return ix, nil
		}
	}
	// SQLite-primary store (default build): read it when present. Load errors
	// are tolerated (mirroring project.Session's load chain) — a corrupt or
	// version-mismatched store falls through to the JSON migration path, and
	// OpenSQLite self-heals a corrupt file by quarantining it.
	if SQLiteEnabled() {
		if _, serr := os.Stat(SQLitePath(abs)); serr == nil {
			if ix, lerr := LoadSQLite(abs); lerr == nil && ix != nil {
				reRootIndex(ix, abs)
				metrics.Default().RecordCacheHit()
				return ix, nil
			}
		}
	}
	data, err := os.ReadFile(p)
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
	reRootIndex(ix, abs)
	ix.initMaps()
	// The lookup caches (symbolIdx, kindIdx) are deferred to the first
	// query that needs them; SymbolsByFile is the only map built here
	// because it is an exported field consumers read directly after Load.
	ix.buildSymbolsByFile()
	metrics.Default().RecordCacheHit()
	return ix, nil
}

// reRootIndex re-points a loaded index's Root at the directory it was loaded
// for when the recorded root is a DIFFERENT absolute path. Without this, Stale() → FreshnessProof(ix.Root) would
// evaluate the original tree — which is unchanged — and report the index
// "fresh" forever, so LoadOrBuild / `kern index` silently reuse a stale index
// and only `kern index --force` healed it. Re-pointing makes every subsequent
// freshness evaluation compare the recorded identity against THIS tree, so a
// moved repo rebuilds automatically on the next load (and a byte-identical
// copy — same git tree OID — stays fresh but with paths resolving into the
// copy, which the write-path out-of-root guards already cover). Relative
// recorded roots (the normal `kern index .` case) are left untouched: they
// resolve to the same directory and forcing a rebuild on every load would be
// a needless full re-walk.
func reRootIndex(ix *Index, abs string) {
	if ix == nil || ix.Root == "" || !filepath.IsAbs(ix.Root) {
		return
	}
	if filepath.Clean(ix.Root) != filepath.Clean(abs) {
		ix.Root = abs
	}
}

// sqliteStoreNewerThanJSON reports whether the SQLite store was written after
// the JSON cache at jsonPath. It guards the gob-snapshot fast path in Load:
// the snapshot's freshness proof compares against index.json's stat, and once
// Save is SQLite-primary (default build) index.json is no longer rewritten, so
// a legacy snapshot would otherwise keep matching its frozen stat and serve
// stale content forever. When SQLite is compiled out, or either file is
// missing, it reports false and the snapshot path is left to its own
// (JSON-stat) freshness check.
func sqliteStoreNewerThanJSON(jsonPath string) bool {
	if !SQLiteEnabled() {
		return false
	}
	jj, err := os.Stat(jsonPath)
	if err != nil {
		return false // no JSON → nothing for a snapshot to mirror
	}
	sj, err := os.Stat(filepath.Join(filepath.Dir(jsonPath), "index.sqlite"))
	if err != nil {
		return false // no SQLite store → legacy JSON/snapshot path stands
	}
	return sj.ModTime().After(jj.ModTime())
}

// Stale reports whether a source file was added, removed, or edited since the
// index was built, so intel never serves out-of-date call graphs. The
// authoritative verdict comes from FreshnessProof (git tree OID, falling back
// to a content re-hash).
//
// An earlier version kept a stat-only count gate here (walked-file count vs
// len(FileHashes)) as a cheap pre-rejection. It was removed: the two counts
// are not comparable — the build walk admits quickExt files that never enter
// FileHashes (e.g. LICENSE, Makefile, content-unindexable .md), so on any
// repo containing such files the gate reported stale PERMANENTLY, forcing
// every Stale() caller (guard check, LoadOrBuild, web console, Session) down
// a needless full update path and defeating web's staleCooldown. A genuine
// count change (file added/removed) always changes the content root too, so
// the proof alone decides — the gate saved latency only on indexes that were
// stale anyway, and every caller rebuilds immediately after a stale verdict.
func (ix *Index) Stale() bool {
	if ix == nil || len(ix.FileHashes) == 0 {
		if ix != nil {
			// No baseline to compare: no content walk ran, so no observation
			// is retained for a following update.
			ix.clearStaleSnapshot()
		}
		return true
	}
	// No recorded identity (in-memory test index, or hand-built Index struct):
	// fall back to the pre-identity content-hash comparison.
	if ix.Identity == nil {
		return ix.legacyStale()
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
			ix.clearStaleSnapshot() // no content walk ran
			return false
		}
	}
	cur, mtimes, err := indexableHashesObserved(ix.Root, ign)
	if err != nil {
		ix.clearStaleSnapshot() // no usable observation
		return true
	}
	ix.retainStaleSnapshot(cur, mtimes)
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
	// Fast path (Rec P0-3): same snapshot preference as Load; JSON canonical.
	if ix, ok := loadBinSnapshot(path); ok {
		if ix.Version != indexVersion {
			return nil, fmt.Errorf("index version %d (want %d): rebuild required", ix.Version, indexVersion)
		}
		ix.initMaps()
		// SymbolsByFile is built eagerly (exported field, read directly by
		// consumers); the symbolIdx/kindIdx lookup caches are deferred to
		// the first query.
		ix.buildSymbolsByFile()
		return ix, nil
	}
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
	// SymbolsByFile is built eagerly (exported field, read directly by
	// consumers); the symbolIdx/kindIdx lookup caches are deferred to the
	// first query.
	ix.buildSymbolsByFile()
	return ix, nil
}

// reindexByFile rebuilds the exported SymbolsByFile map and the kindIdx
// lookup buckets from the final symbol table. The build/update sequences
// call this after resolveEntries (which flips Entry on func/method
// symbols), and the load paths call only buildSymbolsByFile, deferring the
// kindIdx buckets to the first kind-filtered Search.
func (ix *Index) reindexByFile() {
	ix.buildSymbolsByFile()
	ix.buildKindIndex()
}

// buildSymbolsByFile rebuilds SymbolsByFile from the final symbol table.
// Unlike the unexported lookup caches (symbolIdx, kindIdx) it cannot be
// deferred on the load paths: SymbolsByFile is an exported field read
// directly by consumers outside this package (intel/guard, intel/changes,
// web, fragility, architecture), so every load must present it. It is the
// cheapest of the three passes — a single O(symbols) append — so keeping
// it eager costs little.
func (ix *Index) buildSymbolsByFile() {
	ix.SymbolsByFile = map[string][]Symbol{}
	for _, s := range ix.Symbols {
		ix.SymbolsByFile[s.File] = append(ix.SymbolsByFile[s.File], s)
	}
}

// buildKindTable computes the kind -> symbols buckets in Symbols order.
// Bucket membership mirrors symbolMatches exactly:
//   - every non-empty Kind gets an exact bucket ("func", "method", ...),
//     except "entry" (a flag, not a Kind) and the searchTypeKinds members,
//     which land in the "type" super-category bucket instead;
//   - every symbol with Entry set lands in the "entry" bucket;
//   - "type" holds every symbol whose Kind is in searchTypeKinds.
//
// Search re-applies symbolMatches on top of the bucket, so a bucket is only
// ever a candidate superset — results and limit behavior are identical to
// the pre-index linear scan.
func buildKindTable(symbols []Symbol) map[string][]Symbol {
	m := make(map[string][]Symbol)
	for _, s := range symbols {
		if s.Kind != "" && s.Kind != "entry" && !searchTypeKinds[s.Kind] {
			m[s.Kind] = append(m[s.Kind], s)
		}
		if searchTypeKinds[s.Kind] {
			m["type"] = append(m["type"], s)
		}
		if s.Entry {
			m["entry"] = append(m["entry"], s)
		}
	}
	return m
}

// buildKindIndex precomputes the kind -> symbols buckets that let
// kind-filtered Search queries iterate only matching-kind symbols instead of
// scanning every one. Called eagerly by the build/update finalize paths
// (via reindexByFile, after resolveEntries) and lazily by the first
// kind-filtered Search on an index that loaded without the buckets. The
// sync.Once makes it idempotent (a second call is a no-op — the table is
// derived from an immutable symbol table) and safe when the deferred build
// races with concurrent readers.
func (ix *Index) buildKindIndex() {
	c := ix.getCache()
	c.kindOnce.Do(func() {
		c.kindIdx = buildKindTable(ix.Symbols)
	})
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
	slices.Sort(out)
	return out
}

var ignoreDirs = map[string]bool{
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
}

// IgnoredDir reports whether a directory name is skipped during index walks
// (node_modules, vendor, build artifacts, ...). Exported for downstream
// scanners that mirror the index's file-selection policy.
func IgnoredDir(name string) bool { return ignoreDirs[name] }

// BuildOption customizes BuildWithOptions.
type BuildOption func(*buildConfig)

// buildConfig carries the options resolved for one build run. reused is
// written by build workers (atomically) to count prior-result reuse.
//
// workers and maxBytes are the resource-adaptive tunables. Zero means "use
// the machine-derived default" (see resources.go); a non-zero explicit
// option always wins over the adaptive default.
type buildConfig struct {
	prior  *Index
	reused atomic.Int64
	// workers is the parse/scan worker pool size (0 = adaptive default).
	workers int
	// maxBytes is the largest file the index will read and scan
	// (0 = adaptive default).
	maxBytes int64
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
	c.Imports = append([]ImportEdge(nil), p.Imports...)
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

// fileModTimeNanos returns the file's modification time in Unix nanoseconds,
// or 0 when the stat fails (mirroring the serial build, where a failed stat
// simply leaves MaxMtime untouched for that file).
func fileModTimeNanos(d fs.DirEntry) int64 {
	if info, ierr := d.Info(); ierr == nil {
		return info.ModTime().UnixNano()
	}
	return 0
}

// walkIndexable walks root applying the index's file-selection policy —
// ignoreDirs, .gitignore/.kernignore patterns, quickExt, regular-file and
// maxFileBytes caps — and calls fn for every accepted file in lexical walk
// order. It is the single source of truth for which files an index covers,
// shared by buildSerial, buildParallel and Update so the incremental path
// never diverges from a full rebuild on skip rules.
func walkIndexable(root string, ign *ignore.Matcher, maxBytes int64, fn func(rel, path string, mtime int64) error) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
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
		if !quickExt(rel) && filepath.Ext(rel) != "" {
			return nil
		}
		// Skip non-regular files (FIFOs, sockets, device nodes) to avoid blocking reads.
		if !d.Type().IsRegular() {
			return nil
		}
		// Skip files larger than maxBytes before reading them so huge
		// generated/bundled files never get loaded into memory or scanned.
		if info, ierr := d.Info(); ierr == nil && info.Size() > maxBytes {
			return nil
		}
		return fn(rel, path, fileModTimeNanos(d))
	})
}

// buildSerial is the original single-threaded build: it walks root, parses
// every source file in lexical walk order and assembles the index. It is the
// byte-for-byte reference behavior that buildParallel must reproduce (its
// output is what kern's freshness/identity proofs compare against).
func buildSerial(abs string, cfg *buildConfig) (*Index, error) {
	ix := New(abs)
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
		if !isIndexable(rel, src) {
			return nil
		}
		return ix.applyFileResult(reuseOrCompute(cfg.prior, rel, src, mtime, &cfg.reused))
	})
	if err != nil {
		return nil, err
	}
	ix.UpdatedAt = time.Now().UTC()
	ix.buildSymbolIndex()
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
//     maxFileBytes) and collecting one job per accepted file in lexical walk
//     order. No file contents are read here.
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

	// Phase 1: serial walk collecting jobs. The walk applies the exact same
	// skip policy as the serial build via the shared walkIndexable.
	var jobs []fileJob
	err := walkIndexable(abs, ign, t.maxFileBytes, func(rel, path string, mtime int64) error {
		jobs = append(jobs, fileJob{seq: len(jobs), rel: rel, path: path, mtime: mtime})
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Phase 2: worker pool. Workers are pure — they never touch ix; every ix
	// mutation happens only in the main goroutine's merge loop below.
	// Small trees: pool + ordered-merge overhead exceeds the parse cost, so
	// apply jobs serially in lexical order (byte-identical to the pool path).
	if len(jobs) < t.parallelMin {
		for _, j := range jobs {
			if r, ok := reuseByMtime(cfg.prior, j.rel, j.mtime); ok {
				cfg.reused.Add(1)
				if err := ix.applyFileResult(r); err != nil {
					return nil, err
				}
				continue
			}
			src, serr := os.ReadFile(j.path)
			if serr != nil {
				continue // unreadable: same semantics as the serial build
			}
			if !isIndexable(j.rel, src) {
				continue
			}
			if err := ix.applyFileResult(reuseOrCompute(cfg.prior, j.rel, src, j.mtime, &cfg.reused)); err != nil {
				return nil, err
			}
		}
	} else {
		workers := t.workers
		results := make(chan fileResult, t.resultBuf)
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
						runtime.Gosched()
						continue
					}
					idx := next.Add(1) - 1
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
		if firstErr != nil {
			return nil, firstErr
		}
	}
	ix.UpdatedAt = time.Now().UTC()
	ix.buildSymbolIndex()
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
func (ix *Index) applyFileResult(r fileResult) error {
	// Same-identity duplicate guard: the graph keys nodes by FullName, so a
	// same-(name, kind, receiver) duplicate on a different line would
	// silently collapse into one node. Fail loudly.
	if err := checkFileSymbolConflicts(r.rel, r.syms); err != nil {
		return err
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
		ix.Calls[owner] = append(ix.Calls[owner], callees...)
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
		}
	}
	return nil
}

// symbolConflictKinds are kinds treated as declared entities; "entry" and
// "heading" markers legitimately repeat and are exempt.
var symbolConflictKinds = map[string]bool{
	"func": true, "method": true, "type": true, "struct": true,
	"interface": true, "class": true, "enum": true, "trait": true,
	"module": true, "union": true, "impl": true, // "prop" intentionally absent: JSON/YAML data-file keys legitimately repeat on different lines
	"const": true, "var": true, "doc": true,
}

// checkFileSymbolConflicts fails loudly when one file's extracted symbols
// collide: the graph keys nodes by FullName, so two symbols sharing (name,
// kind, receiver) on different lines are the SAME node defined twice.
// Different receivers (A.list vs B.list) are distinct nodes, left to the
// ambiguity machinery. Sorts indices — never the symbol slice — O(n log n). Only type kinds and receiver-bearing symbols fail loud: scoped locals (const/var/func, empty receiver), shell re-assignment, data-file props and Go's `_` legitimately repeat.
func checkFileSymbolConflicts(rel string, syms []Symbol) error {
	if len(syms) < 2 {
		return nil
	}
	order := make([]int, len(syms))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int {
		x, y := syms[a], syms[b]
		switch {
		case x.Name != y.Name:
			return strings.Compare(x.Name, y.Name)
		case x.Kind != y.Kind:
			return strings.Compare(x.Kind, y.Kind)
		case x.Receiver != y.Receiver:
			return strings.Compare(x.Receiver, y.Receiver)
		default:
			return x.Line - y.Line
		}
	})
	for i := 1; i < len(order); i++ {
		p, c := syms[order[i-1]], syms[order[i]]
		if p.Name == c.Name && p.Kind == c.Kind && p.Receiver == c.Receiver &&
			(p.Line != c.Line || p.End != c.End) && symbolConflictKinds[c.Kind] && (c.Receiver != "" || typeKinds[c.Kind] || c.Kind == "type") {
			return fmt.Errorf("index: duplicate symbol conflict in %s: %q (%s) at lines %d and %d — the same identity was extracted twice; refusing to index (fail-loud; report an extractor bug)", rel, c.Name, c.Kind, p.Line, c.Line)
		}
	}
	return nil
}

// CallResStats counts distinct call targets and how many are unresolved.
type CallResStats struct {
	Total      int `json:"total"`
	Unresolved int `json:"unresolved"`
}

// measureCallResolution counts distinct callee targets and how many fail
// resolveName. Runs after computeCallers in every finalize/load sequence so
// `kern index --status` can report the honest per-repo resolution rate.
func (ix *Index) measureCallResolution() {
	seen := map[string]struct{}{}
	var unres int
	for _, callees := range ix.Calls {
		for _, ce := range callees {
			if _, dup := seen[ce.Target]; dup {
				continue
			}
			seen[ce.Target] = struct{}{}
			if _, ok := resolveName(ix, ce.Target); !ok {
				unres++
			}
		}
	}
	ix.CallResolution = CallResStats{Total: len(seen), Unresolved: unres}
}

// methodLangFromFile resolves the language a method symbol's file must have
// when the mapping is unambiguous from the file path alone — the language of
// a .go file can only be "go" (goast is the sole Go extractor), a .py file
// only "python", and so on, mirroring detectLang's extension mapping.
// Content-dependent languages (.vue/.svelte script lang), .astro, and
// extensionless shebang scripts cannot be recovered from the path alone, so
// they return "" and the caller treats them as ambiguous.
func methodLangFromFile(rel string) string {
	switch strings.ToLower(filepath.Ext(rel)) {
	case ".vue", ".svelte", ".astro", "":
		return "" // content-dependent or extensionless: ambiguous without src
	}
	return detectLang(rel, nil)
}

// enforceMethodLangInvariant guards the A5 bare-callee-never-methods rule's
// Go scoping (internal/intel/queries.go calleeIsTarget drops a bare callee
// only when the resolved method's Language == "go"): the rule is trustworthy
// only while every method symbol carries its language. goast stamps
// Lang:"go" on every Go symbol (goast.go) and the foreign extractors stamp
// the detected language, so Receiver != "" && Lang == "" can only come from a
// hand-built index or a broken extractor — which would silently re-open the
// over-attribution bug (a Go method missing its "go" tag escapes the guard
// and pools bare-reference callers). Unambiguous file-language fixup (.go ⇒
// "go", .py ⇒ "python", …) repairs the symbol; an ambiguous case (unknown
// extension, content-dependent language) fails loudly with a count rather
// than guessing. Runs once per finalize, at the head of computeCallers,
// before any pass reads Lang (computePrecisionByLang, the graph guard).
func (ix *Index) enforceMethodLangInvariant() {
	var offenders []string
	for i := range ix.Symbols {
		s := &ix.Symbols[i]
		if s.Receiver == "" || s.Lang != "" {
			continue
		}
		if lang := methodLangFromFile(s.File); lang != "" {
			s.Lang = lang
			continue
		}
		offenders = append(offenders, s.FullName()+" @ "+s.File)
	}
	if len(offenders) > 0 {
		panic(fmt.Sprintf("index invariant: %d method symbol(s) have Receiver != \"\" but no Lang (the A5 bare-callee guard needs every method's language); fix the extractor or set Lang explicitly: %s",
			len(offenders), strings.Join(offenders, "; ")))
	}
}

func (ix *Index) computeCallers() {
	// Load-bearing invariant (A5): every method symbol must carry its
	// language — the intel bare-callee-never-methods guard is scoped on
	// Lang == "go", so a method with Receiver != "" and Lang == "" would
	// silently re-open the over-attribution bug. Fixes up unambiguous
	// file-language cases; fails loudly on ambiguous ones.
	ix.enforceMethodLangInvariant()
	// Resolve constructor-inferred callee qualifiers now that the full
	// package symbol set is merged (per-file extraction cannot see
	// cross-file constructors): "New.M" -> "Index.M" and
	// "Server.newGov.M" -> "Gov.M". Conservative — single-return
	// constructors whose return type is an actual declared type only.
	ix.rewriteConstructorCallees()
	ix.Callers = map[string][]string{}
	ix.AliasCallers = map[string][]string{}
	// Project package directory bases (e.g. "lock", "memory") vs foreign
	// import bases ("fmt", "time"): the receiver-qualified merge below must
	// never attribute a foreign package's callee ("fmt.Println") to a local
	// symbol, even when the simple name is unique project-wide.
	foreignImportBases := ix.foreignImportBases()
	// Names the project itself defines — types, receivers and constructors —
	// used to tell a receiver-qualified single-dot key ("Client.Code") from an
	// unresolvable-variable key ("mystery.M") whose receiver is defined
	// nowhere: the latter must never merge onto a local symbol's canonical
	// bucket (CallersIncludingAliases still finds it via the alias layer).
	definedQualifierNames := map[string]bool{}
	for _, s := range ix.Symbols {
		if s.Receiver != "" {
			definedQualifierNames[s.Receiver] = true
		}
		switch s.Kind {
		case "type", "struct", "interface", "class", "record", "enum", "func", "method", "function":
			definedQualifierNames[s.Name] = true
		}
	}
	for caller, callees := range ix.Calls {
		for _, ce := range callees {
			c := ce.Target
			if c == caller {
				continue
			}
			// Canonical edge: recorded under the exact callee key.
			ix.Callers[c] = append(ix.Callers[c], caller)
			// Merge package-qualified local calls ("db.Open") onto the local
			// symbol when the qualifier names its package dir; foreign and
			// unresolved targets never merge (avoids forging callers). The
			// merge is restricted to buckets that cannot mis-attribute: a
			// UNIQUE bare name owns its bare bucket outright (CallersFor
			// returns it without package filtering — the graph resolves any
			// qualifier for a unique simple name, so these callers must land
			// here or impact over-reports), while a SHARED bare name merges
			// only SAME-PACKAGE qualified callers — the bare-bucket
			// attribution (CallersFor) checks only the CALLER's package, so
			// a cross-package qualified caller ("learning.New" from
			// internal/app) would otherwise be mis-attributed to a
			// same-named symbol in the caller's package (app.New) even
			// though the recorded callee is learning.New — the caller
			// already sits under the canonical "learning.New" key, which
			// CallersFor's qualified-key loop attributes to the correct
			// symbol.
			if d, ok := qualifiedCalleeSymbol(ix, c); ok && d.FullName() != c && d.FullName() != caller {
				if len(ix.symbolsFor(d.FullName())) == 1 || callerInPackage(ix, caller, filepath.Dir(d.File)) {
					ix.Callers[d.FullName()] = append(ix.Callers[d.FullName()], caller)
				}
			}
			// Merge receiver/type/variable-qualified callee keys
			// ("Client.Code", "s.govSnapshot.Access.Authorize",
			// "NewGovernance.Access.CanRead") onto the unique same-named symbol
			// when the simple name identifies exactly one definition
			// project-wide. The graph's raw-edge collector resolves any qualifier
			// for a unique simple name (resolveNodeID case 1), so explore's
			// caller buckets must report those callers too or impact
			// over-reports. Keys whose qualifier is a foreign package import
			// base ("fmt.Println", "time.Date.Add") never merge: the callee is
			// not a local symbol, and a local same-named definition is never the
			// recorded target. A merge that would add the caller to its OWN
			// bucket (a stdlib chain rewritten onto the caller's own type, e.g.
			// Duration.String -> time.Duration.String landing on
			// "Duration.String") is a self-edge, never a caller — skipped.
			if d, ok := uniqueReceiverQualifiedSymbol(ix, c, foreignImportBases, definedQualifierNames); ok && d.FullName() != c && d.FullName() != caller {
				ix.Callers[d.FullName()] = append(ix.Callers[d.FullName()], caller)
			}
			// Merge receiver-chain callee keys whose bare name is SHARED
			// across receivers ("NewFirewall.WithAgents.AuditLog.All",
			// "f.AuditLog.All", "e.Confidence.String"): the qualifier's LAST
			// segment names the receiver type, mirroring the graph's
			// receiver match (resolveNodeID's booleanNested / exact-receiver
			// cases), which attributes such edges even when the simple name
			// is ambiguous. The merge only fires when the receiver+name
			// combination is UNIQUE project-wide: the method FullName bucket
			// ("Store.Save") is shared by every same-named method and
			// CallersFor returns it wholesale, so a chained caller would
			// otherwise be mis-attributed to all of them (the graph
			// attributes a chained receiver key to its first same-receiver
			// match only). Package-qualified keys ("learning.New") never
			// match a receiver named "learning" and stay untouched — the
			// shared-bare-bucket protection above still holds.
			if i := strings.LastIndexByte(c, '.'); i > 0 && i+1 < len(c) {
				qual, bare := c[:i], c[i+1:]
				recv := qual
				if j := strings.LastIndexByte(qual, '.'); j >= 0 {
					recv = qual[j+1:]
				}
				if recv != "" {
					var match Symbol
					matches := 0
					for _, d := range ix.symbolsFor(bare) {
						if d.Receiver == recv && d.FullName() != c {
							match = d
							matches++
						}
					}
					if matches == 1 {
						if match.FullName() != caller {
							ix.Callers[match.FullName()] = append(ix.Callers[match.FullName()], caller)
						}
					}
				}
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
	// Resolve structural interface implementations: match concrete types to interface
	// definitions in the same package.
	ifaceDir := map[string]string{}
	concreteDir := map[string]string{}
	concreteMethods := map[string]map[string]bool{}
	for _, s := range ix.Symbols {
		dir := filepath.Dir(s.File)
		if s.Kind == "interface" {
			ifaceDir[s.Name] = dir
		} else if s.Kind == "struct" || s.Kind == "type" {
			concreteDir[s.Name] = dir
		}
		if s.Kind == "method" && s.Receiver != "" {
			if concreteMethods[s.Receiver] == nil {
				concreteMethods[s.Receiver] = map[string]bool{}
			}
			concreteMethods[s.Receiver][s.Name] = true
			if concreteDir[s.Receiver] == "" {
				concreteDir[s.Receiver] = dir
			}
		}
	}
	for iface, iDir := range ifaceDir {
		for concrete, cDir := range concreteDir {
			if concrete == iface || cDir != iDir {
				continue
			}
			if _, isIface := ifaceDir[concrete]; isIface {
				continue
			}
			if len(concreteMethods[concrete]) > 0 {
				edge := "implements:" + iface
				already := false
				for _, e := range ix.Inherits[concrete] {
					if e == edge {
						already = true
						break
					}
				}
				if !already {
					if ix.Inherits == nil {
						ix.Inherits = map[string][]string{}
					}
					ix.Inherits[concrete] = append(ix.Inherits[concrete], edge)
				}
			}
		}
	}

	// The implements: edges above were appended in ifaceDir/concreteDir
	// MAP-ITERATION order (nondeterministic), so the Inherits values carry the
	// same nondeterminism class the InheritedBy reverse map had — that map is
	// dedupeSorted below, but the forward values were never sorted (finding
	// A1). Sort + dedupe them in the same finalize pass so the persisted
	// index is byte-deterministic regardless of map iteration order.
	for k := range ix.Inherits {
		ix.Inherits[k] = dedupeSorted(ix.Inherits[k])
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
		for _, ce := range callees {
			c := ce.Target
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
				// A self virtual-dispatch edge (the caller is itself the
				// implementer) is noise: computeCallers skips self-edges, so
				// recording one here made the build-time Callers map differ
				// from the recomputed one on every load path (JSON↔SQLite
				// parity), for an edge no traversal wants.
				if virtualCallee == caller {
					continue
				}
				key := caller + "->" + virtualCallee
				if added[key] {
					continue
				}
				added[key] = true
				// Virtual dispatch edges are inferred through the inheritance
				// graph, not stated in source: LOW.
				ix.Calls[caller] = append(ix.Calls[caller], CallEdge{Target: virtualCallee, Confidence: ConfidenceLow})
				ix.Callers[virtualCallee] = append(ix.Callers[virtualCallee], caller)
			}
		}
	}

	// Dedupe after adding virtual edges.
	for k := range ix.Calls {
		ix.Calls[k] = dedupeCallEdges(ix.Calls[k])
	}
	for k := range ix.Callers {
		ix.Callers[k] = dedupeSorted(ix.Callers[k])
	}
}

func dedupeSorted(in []string) []string {
	if in == nil {
		return nil
	}
	if len(in) == 0 {
		return in
	}
	cp := append([]string(nil), in...)
	slices.Sort(cp)
	return slices.Compact(cp)
}

// dedupeCallEdges dedupes a call-edge slice by target keeping the
// HIGHEST-confidence representative. A first-wins dedupe let an
// edge recorded LOW before it was re-resolved stay LOW forever; promotion
// can also merge distinct target forms ("db.Open" and "Open") into one key,
// and the highest confidence is the honest verdict for that key. Ties keep
// the first occurrence, preserving input order determinism. The output is
// sorted by target, so the Calls map stays in the deterministic order the
// finalize passes rely on.
func dedupeCallEdges(in []CallEdge) []CallEdge {
	if len(in) < 2 {
		return in
	}
	best := map[string]CallEdge{}
	for _, e := range in {
		if prev, ok := best[e.Target]; !ok || confRank(e.Confidence) > confRank(prev.Confidence) {
			best[e.Target] = e
		}
	}
	out := make([]CallEdge, 0, len(best))
	for _, e := range best {
		out = append(out, e)
	}
	slices.SortFunc(out, func(a, b CallEdge) int { return strings.Compare(a.Target, b.Target) })
	return out
}

// confRank orders the confidence tiers so the best representative wins:
// HIGH(3) > MEDIUM(2) > LOW(1). An empty confidence ranks LOW.
func confRank(c Confidence) int {
	switch c {
	case ConfidenceHigh:
		return 3
	case ConfidenceMedium:
		return 2
	default:
		return 1
	}
}

// symbolsFor returns every symbol whose bare or full name matches. Build
// paths precompute the lookup table eagerly (buildSymbolIndex), making
// this O(1); indexes that loaded without it build the table once on the
// first lookup (see below), so no path pays a per-call linear scan.
// Callers only iterate the result — the cached slices are shared.
func (ix *Index) symbolsFor(name string) []Symbol {
	// The name -> symbols table is built eagerly by the build/update paths
	// (buildSymbolIndex) and lazily on the first lookup for indexes that
	// loaded without it. The sync.Once makes the deferred build safe under
	// concurrent readers; the table is derived from the immutable symbol
	// table, so the eager and deferred paths produce identical results.
	c := ix.getCache()
	c.symbolOnce.Do(func() {
		c.symbolIdx = buildSymbolTable(ix.Symbols)
	})
	return c.symbolIdx[name]
}

// buildSymbolTable computes the name -> symbols map that turns symbolsFor
// from a full-index linear scan into a map lookup. Per-name slice order
// matches the linear scan's (append in Symbols order), so results are
// identical.
func buildSymbolTable(symbols []Symbol) map[string][]Symbol {
	m := make(map[string][]Symbol, len(symbols)*2)
	for _, s := range symbols {
		m[s.Name] = append(m[s.Name], s)
		if fn := s.FullName(); fn != s.Name {
			m[fn] = append(m[fn], s)
		}
	}
	return m
}

// buildSymbolIndex precomputes the name -> symbols map that turns
// symbolsFor from a full-index linear scan into a map lookup. Called by
// the build paths before the finalize passes (computeCallers and
// addDispatchEdges call symbolsFor per call edge, which made finalize
// O(edges x symbols) — the dominant build cost on symbol-heavy repos) and
// by the SQLite store before its load-time computeCallers. The sync.Once
// makes it idempotent — a Load that skipped the eager pass has the table
// built on the first query instead — and safe under concurrent first
// access.
func (ix *Index) buildSymbolIndex() {
	c := ix.getCache()
	c.symbolOnce.Do(func() {
		c.symbolIdx = buildSymbolTable(ix.Symbols)
	})
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
// Kind-prefixed queries iterate the precomputed kind bucket (when the index
// has one) instead of every symbol; plain queries keep the full scan.
func (ix *Index) Search(pattern string, limit int) []Symbol {
	if limit <= 0 {
		limit = 50
	}
	re, kind := symbolRegex(pattern)
	if kind != "" {
		// Buckets preserve Symbols order and Search re-applies symbolMatches,
		// so results (and the limit cut) are identical to the linear scan.
		if syms, ok := ix.kindSymbols(kind); ok {
			var out []Symbol
			for _, s := range syms {
				if symbolMatches(s, kind, re) {
					out = append(out, s)
					if len(out) >= limit {
						break
					}
				}
			}
			return out
		}
	}
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

// kindSymbols returns the precomputed symbol bucket for a search kind
// ("func", "method", "type", "entry", ...). ok is false when the index has
// no kind index, and Search falls back to the linear scan — the
// pre-index behavior. The bucket is built on the first call (sync.Once) if
// the index loaded without one, so a kind-filtered Search pays one
// O(symbols) pass and then serves O(bucket) lookups.
func (ix *Index) kindSymbols(kind string) ([]Symbol, bool) {
	c := ix.getCache()
	c.kindOnce.Do(func() {
		c.kindIdx = buildKindTable(ix.Symbols)
	})
	syms, ok := c.kindIdx[kind]
	return syms, ok
}

// symbolRegexCacheSize bounds the number of compiled search regexes kept in
// memory. Search compiles one regex per query and the compile is pure
// overhead relative to the scan; without a cache, repeated patterns (the
// common case in agent loops) pay it every time.
const symbolRegexCacheSize = 128

var (
	symbolRegexCacheMu sync.Mutex
	// symbolRegexCache maps a stripped search pattern to its compiled regex.
	// Compiled regexes are immutable and safe for concurrent use, so one
	// cached entry may be shared across concurrent Search calls.
	symbolRegexCache = make(map[string]*regexp.Regexp)
	// symbolRegexOrder tracks insertion order for FIFO eviction.
	symbolRegexOrder []string
)

// cachedSymbolRegex returns the compiled regex for a stripped search pattern,
// compiling and caching it on first use. Eviction is FIFO once the cache
// reaches symbolRegexCacheSize entries; evicting a regex never changes
// behavior — the same pattern is simply recompiled on its next use.
func cachedSymbolRegex(p string) *regexp.Regexp {
	symbolRegexCacheMu.Lock()
	defer symbolRegexCacheMu.Unlock()
	if re, ok := symbolRegexCache[p]; ok {
		return re
	}
	expr := "^" + strings.ReplaceAll(regexp.QuoteMeta(p), `\*`, `.*`) + "$"
	re := regexp.MustCompile(expr)
	if len(symbolRegexCache) >= symbolRegexCacheSize {
		delete(symbolRegexCache, symbolRegexOrder[0])
		symbolRegexOrder = symbolRegexOrder[1:]
	}
	symbolRegexCache[p] = re
	symbolRegexOrder = append(symbolRegexOrder, p)
	return re
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
	// The cache is keyed on the stripped pattern, so "func foo" and
	// "method foo" share one compiled regex; only the kind differs.
	return cachedSymbolRegex(p), kind
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
	return CallEdgeTargets(ix.Calls[symbol])
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
// A package-qualified reference names a package-level symbol: when the
// package defines the name receiver-less, that func is the target — a
// same-named METHOD in the same package must not win ("tokenize.Count" is the
// func Count's form; BPECounter.Count is receiver-qualified and bpe.go sorts
// before tokenize.go, so the first same-base match used to pool the func's
// callers onto the method).
func qualifiedCalleeSymbol(ix *Index, c string) (Symbol, bool) {
	i := strings.LastIndexByte(c, '.')
	if i <= 0 || i+1 >= len(c) {
		return Symbol{}, false
	}
	qualifier, bare := c[:i], c[i+1:]
	var methodFallback Symbol
	haveMethodFallback := false
	for _, d := range ix.symbolsFor(bare) {
		if filepath.Base(filepath.Dir(d.File)) != qualifier {
			continue
		}
		if d.Receiver == "" {
			return d, true
		}
		if !haveMethodFallback {
			methodFallback, haveMethodFallback = d, true
		}
	}
	return methodFallback, haveMethodFallback
}

// uniqueReceiverQualifiedSymbol returns the single project symbol whose
// simple name is the final segment of a receiver/type/variable-qualified
// callee key ("Client.Code", "s.Access.Authorize"), when that simple name
// identifies exactly one definition project-wide. The graph resolves any
// qualifier for a unique simple name, so these callers must land in the
// explore bucket of that one symbol. A qualifier chain (≥2 dots) is never a
// package-qualified reference in Go source (a package selector cannot carry
// further selectors), so chains resolve whenever the simple name is unique
// and the chain does not start at a foreign package import base
// ("time.Now.UTC.Add"). A single-dot key resolves only when its qualifier
// is a name the project itself defines — a type, receiver or constructor
// ("Client.Code") — never a foreign package ("fmt.Println",
// "json.Unmarshal") or an unresolvable variable ("mystery.M"): those callers
// stay in the canonical bucket under their own key and in the alias layer,
// never merged into a local symbol's canonical bucket (the
// TestCallersIncludingAliases contract).
func uniqueReceiverQualifiedSymbol(ix *Index, c string, foreignImportBases, definedQualifierNames map[string]bool) (Symbol, bool) {
	i := strings.LastIndexByte(c, '.')
	if i <= 0 || i+1 >= len(c) {
		return Symbol{}, false
	}
	matches := ix.symbolsFor(c[i+1:])
	if len(matches) != 1 {
		return Symbol{}, false
	}
	first := c[:strings.IndexByte(c, '.')]
	if foreignImportBases[first] {
		return Symbol{}, false
	}
	if !strings.Contains(c[strings.IndexByte(c, '.')+1:], ".") && !definedQualifierNames[first] {
		// Single-dot key whose qualifier is not a project-defined
		// name: a foreign package or an unresolvable variable — never
		// a local symbol's receiver. The canonical map must stay
		// resolved-only; CallersFor's unique-name path still surfaces
		// these callers (the graph resolves any qualifier for a unique
		// name), so parity holds without polluting the canonical map.
		return Symbol{}, false
	}
	return matches[0], true
}

// CallersFor returns deduplicated callers attributable to a local symbol.
// Attribution is package-aware. Methods key under the unique "Type.Method"
// full name, and a package-level symbol whose bare name is unambiguous owns
// its bare bucket outright — both keep the exact recorded callers,
// byte-identical to before. When two or more packages define the same simple
// name, the shared bare bucket mixes every same-named symbol's callers, so a
// bare edge is attributed to s only when the caller side resolves to a symbol
// in s's own package directory, and package-qualified keys
// ("governance.AuthorizeContext") are attributed only to the exact symbol
// they resolve to (mirroring qualifiedCalleeSymbol's dir match). A bare edge
// whose caller lives in a different package is never attributed to a
// same-named symbol — that is the merge bug this guards against.
func (ix *Index) CallersFor(s Symbol) []string {
	exact := ix.Callers[s.FullName()]
	if s.Receiver != "" || len(ix.symbolsFor(s.Name)) == 1 || s.File == "" {
		out := dedupeSorted(exact)
		// Unique simple name: the graph resolves ANY qualifier for a unique
		// name (resolveNodeID case 1), so every dotted callee key ending in
		// the name ("g.WhatDependsOn" — a variable receiver,
		// "kdiff.IndexSpanResolver" — an import alias, "mystery.M" — an
		// unresolvable variable) attributes its callers to the one symbol.
		// Keys whose first qualifier is a foreign package import base
		// ("fmt.Println") never attribute to a local symbol (the
		// TestForeignCalleeNeverAliasesLocalSymbol contract); the canonical
		// map stays resolved-only — this path only widens the RENDERED
		// caller set, matching the graph.
		if len(ix.symbolsFor(s.Name)) == 1 {
			foreign := ix.foreignImportBases()
			for k, callers := range ix.Callers {
				if k == s.FullName() || simpleKey(k) != s.Name {
					continue
				}
				i := strings.IndexByte(k, '.')
				if i <= 0 || foreign[k[:i]] {
					continue
				}
				for _, c := range callers {
					// A caller that IS this symbol is the symbol's own
					// chained self-edge ("Schema.Properties.check" ->
					// "Schema.check" recorded by Schema.check itself) —
					// never a caller (computeCallers skips self-edges on
					// the direct path; the widened render must too).
					if c == s.FullName() {
						continue
					}
					out = append(out, c)
				}
			}
		}
		// A METHOD's bare-name bucket is not its own: a bare edge lives under
		// the bare key ("check"), and the graph's caller-package fallback
		// resolves a same-package bare reference to the method when it is the
		// package's only definition of the name. Mirror that — same-package
		// bare callers join the method's callers. Bare FUNCS already own
		// their bare bucket via exact above; a method never does (methods are
		// not bare-callable, so its receiver-qualified bucket holds only
		// qualified callers).
		if s.Receiver != "" {
			dir := filepath.Dir(s.File)
			// Package-qualified keys ("metrics.Load") whose qualifier names
			// the package: the graph resolves a RESOLVABLE caller's
			// qualifier through its own imports (landing only where the
			// imports point — a blueprint caller of "metrics.Load" hits
			// internal/bpreceipt/metrics, not this method), and falls back
			// to a last-segment package heuristic ONLY when the caller is
			// unresolvable (a pooled bare caller bucket like "runMetrics",
			// shared by two packages). Mirror that: only the key's
			// UNRESOLVABLE callers join here — attributing a resolvable
			// caller by directory base alone would over-attribute
			// (internal/metrics and internal/bpreceipt/metrics share the
			// "metrics" base). A package-qualified key ("tokenize.Count")
			// names the package's RECEIVER-LESS symbol: when the package
			// defines the simple name as a func, the key is the func's form
			// and its callers belong to the func, never to a same-named
			// method (the graph's import-qualified resolution lands on the
			// func too — BPECounter.Count/Estimator.Count must not inherit
			// the func Count's callers).
			hasLocalFunc := false
			for _, d := range ix.symbolsFor(s.Name) {
				if d.Receiver == "" && filepath.Dir(d.File) == dir {
					hasLocalFunc = true
					break
				}
			}
			if !hasLocalFunc {
				for k, callers := range ix.Callers {
					if k == s.FullName() || simpleKey(k) != s.Name {
						continue
					}
					i := strings.LastIndexByte(k, '.')
					if i <= 0 || i+1 >= len(k) {
						continue
					}
					if k[:i] != filepath.Base(dir) {
						continue
					}
					for _, c := range callers {
						if len(ix.symbolsFor(c)) != 1 {
							out = append(out, c)
						}
					}
				}
			}
			// Bare-name bucket: a method named with a Go predeclared
			// identifier ("append", "len") is never bare-callable — a bare
			// reference binds to the builtin (Go scoping) or to a
			// package-level func that shadows it, so the bucket holds only
			// builtin calls and must not widen the method's callers.
			if !isPredeclared(s.Name) && exactlyOneLocalDef(ix, s.Name, dir) {
				for _, c := range ix.Callers[s.Name] {
					// Mirror the graph's callee-side resolution of a bare
					// callee (resolveEndpointPackageAware anchored on the
					// caller), which depends on the CALLER's form:
					//  - a receiver-qualified caller ("Client.audit", pooled
					//    by the Python and TS SDKs) is resolved by trying
					//    every receiver-matched candidate — the same-package
					//    def wins, so a caller with ANY def in this package
					//    is attributed (callerHasLocalDef);
					//  - a bare caller ("List") needs an unambiguous
					//    resolution — every def must live here
					//    (callerInPackage; the orgapprovals List that drives
					//    the bare func loadLocked must not leak onto
					//    ArtifactStore.loadLocked just because
					//    internal/tasklife also defines a List);
					//  - either form is additionally attributed when the
					//    caller's package imports this def's package (the
					//    internal/web rate-limit test drives the SDK's
					//    Client.do through an HTTP client).
					if ix.callerAttributedBare(c, s, dir) {
						out = append(out, c)
					}
				}
			}
		}
		return dedupeSorted(out)
	}
	dir := filepath.Dir(s.File)
	var out []string
	// Package-qualified keys ("db.Open", "lock.Acquire") whose qualifier names
	// a directory holding a definition of s: the bucket is attributed to every
	// same-directory candidate, not just the first the qualifier happens to
	// match — two packages can share a directory base ("memory" ->
	// internal/memory and internal/mcp/memory) and build-tagged duplicates can
	// share one package ("lock.Acquire" -> lock_unix.go and lock_windows.go),
	// and impact attributes the qualified endpoint to each same-named
	// definition whose package the qualifier names. A RESOLVABLE caller
	// (unique simple name) is attributed only when its package actually
	// imports this definition's package under the qualifier — the graph
	// resolves the qualified callee through the CALLER's imports, so a
	// caller of "governance.AuthorizeContext" (the core, imported as
	// "governance") must not be base-matched onto the MCP wrapper in
	// internal/mcp/governance just because both packages end in
	// "governance". Unresolvable callers join via the graph's last-segment
	// package fallback, which fires only when the caller cannot be
	// attributed.
	for k, callers := range ix.Callers {
		if k == s.FullName() || simpleKey(k) != s.Name {
			continue
		}
		i := strings.LastIndexByte(k, '.')
		if i <= 0 || i+1 >= len(k) {
			continue
		}
		qual := k[:i]
		for _, d := range ix.symbolsFor(s.Name) {
			if filepath.Base(filepath.Dir(d.File)) == qual && filepath.Dir(d.File) == dir {
				for _, c := range callers {
					if len(ix.symbolsFor(c)) != 1 || ix.callerImportsDef(c, d, qual) {
						out = append(out, c)
					}
				}
				break
			}
		}
	}
	// Bare-name bucket: same-package callers only, gated by exactlyOneLocalDef
	// (methods included — the graph's caller-package fallback ambiguity). Two
	// graph paths are mirrored: (a) when the bare name has a UNIQUE receiver-less
	// definition project-wide (resolveNodeID's qualified-match — methods never
	// match a bare reference), every bare edge calls that one definition, so an
	// ambiguous caller name is still attributed when it has a definition in the
	// package (the graph resolves the callee via the unique match and the caller
	// via its same-package preference); (b) otherwise a shared bare name needs a
	// caller wholly in the package (the graph needs a unique caller to
	// disambiguate). The strict all-matches callerInPackage stays in force for
	// the computeCallers merge guard, which must not let a cross-package
	// qualified caller enter a shared bare bucket.
	for _, c := range exact {
		if uniqueReceiverLessDef(ix, s.Name) {
			// Path (a): the bare name resolves to the unique receiver-less
			// definition regardless of local methods or the caller's
			// ambiguity; the caller is attributed via same-package
			// preference.
			if callerHasLocalDef(ix, c, dir) {
				out = append(out, c)
			}
		} else if exactlyOneLocalDef(ix, s.Name, dir) && ix.callerAttributedBare(c, s, dir) {
			// Path (b): shared bare name — the graph's caller-package
			// fallback needs an unambiguous local resolution (methods
			// included) and a caller resolved into the package: the
			// same-package def for a receiver-qualified caller (any
			// local def wins — Check.Run is shared by several packages
			// but the blueprint Check.Run calls the blueprint
			// normalizePath), every def local for a bare caller, or an
			// import-linked caller.
			out = append(out, c)
		}
	}
	return dedupeSorted(out)
}

// uniqueReceiverLessDef reports whether name has exactly ONE receiver-less
// definition project-wide — the graph's resolveNodeID qualified-match: a
// bare reference resolves to the unique receiver-less definition (a method's
// Qualified is receiver-qualified and never equals the bare reference). When
// this holds, every bare edge on the name calls that one definition, so the
// caller-side attribution can use the graph's same-package preference even
// for an ambiguous caller name.
func uniqueReceiverLessDef(ix *Index, name string) bool {
	n := 0
	for _, d := range ix.symbolsFor(name) {
		if d.Receiver == "" {
			n++
		}
	}
	return n == 1
}

// exactlyOneLocalDef reports whether the package at dir defines the bare
// name exactly once — methods included. A single local definition owns its
// bare edges; two or more (a func and a same-named method, or build-tagged
// duplicates like Acquire in lock_unix.go/lock_windows.go) leave a bare
// reference ambiguous within the package and attribute nothing — the same
// ambiguity the graph's caller-package fallback (resolveEndpointPackageAware)
// hits when several same-named local candidates block the resolution. (The
// bare-bucket loop still attributes the caller when the name has a UNIQUE
// receiver-less definition project-wide — the graph's resolveNodeID
// qualified-match path, which ignores methods entirely.)
func exactlyOneLocalDef(ix *Index, name, dir string) bool {
	n := 0
	for _, d := range ix.symbolsFor(name) {
		if filepath.Dir(d.File) == dir {
			n++
		}
	}
	return n == 1
}

// foreignImportBases returns the set of foreign package import bases ("fmt",
// "time") — import path bases that are not project package directory bases —
// used to keep a foreign qualified callee ("fmt.Println") from ever
// attributing its callers to a local same-named symbol. Computed per call
// (the maps are small); deliberately not persisted on the index.
func (ix *Index) foreignImportBases() map[string]bool {
	pkgDirBases := map[string]bool{}
	for path := range ix.Pkgs {
		if base := filepath.Base(path); base != "" {
			pkgDirBases[base] = true
		}
	}
	out := map[string]bool{}
	for _, p := range ix.Pkgs {
		for _, imp := range p.Imports {
			if base := filepath.Base(imp.Path); base != "" && !pkgDirBases[base] {
				out[base] = true
			}
		}
	}
	return out
}

// callerInPackage reports whether a caller endpoint resolves to a symbol whose
// file lives in dir. An unresolvable or multi-package caller name is never
// attributed: a bare edge whose caller could be a different package's
// same-named symbol must not merge into this symbol's callers.
func callerInPackage(ix *Index, caller, dir string) bool {
	matches := ix.symbolsFor(caller)
	if len(matches) == 0 {
		return false
	}
	for _, m := range matches {
		if filepath.Dir(m.File) != dir {
			return false
		}
	}
	return true
}

// callerHasLocalDef reports whether a caller endpoint has AT LEAST ONE
// definition in dir — the graph's same-package preference for an ambiguous
// caller name (resolveEndpointPackageAware attributes the caller to the
// same-package candidate when several packages share the name). Used by
// CallersFor's bare-bucket attribution; the strict all-matches
// callerInPackage remains the gate for the computeCallers merge guard so a
// cross-package qualified caller can never enter a shared bare bucket.
func callerHasLocalDef(ix *Index, caller, dir string) bool {
	for _, m := range ix.symbolsFor(caller) {
		if filepath.Dir(m.File) == dir {
			return true
		}
	}
	return false
}

// filePkgIndex maps a source file to its package path via the Pkgs file
// lists. Cheap (O(pkgs x files)); used only in the rendered-caller widening
// paths, never per-edge in a hot loop.
func (ix *Index) filePkgIndex() map[string]string {
	out := map[string]string{}
	for path, p := range ix.Pkgs {
		for _, f := range p.Files {
			if _, dup := out[f]; !dup {
				out[f] = path
			}
		}
	}
	return out
}

// callerImportsDef reports whether a RESOLVABLE caller's package imports the
// def's package under qual (the callee's recorded qualifier), mirroring the
// graph's import-based resolution of a qualified callee through the caller's
// own imports (resolveImportQualified). A caller in a package that does not
// import the def's package is not a caller of this def: the base-name match
// alone would let "governance.AuthorizeContext" callers of the core
// internal/governance leak onto the MCP wrapper in internal/mcp/governance,
// which merely shares the final path segment.
func (ix *Index) callerImportsDef(caller string, d Symbol, qual string) bool {
	defs := ix.symbolsFor(caller)
	if len(defs) != 1 {
		return false // unresolvable callers join via the graph's fallback instead
	}
	filePkg := ix.filePkgIndex()
	callerPkg := filePkg[defs[0].File]
	if callerPkg == "" {
		return false
	}
	p := ix.Pkgs[callerPkg]
	if p == nil {
		return false
	}
	defPkg := filePkg[d.File]
	if defPkg == "" {
		return false
	}
	for _, imp := range p.Imports {
		path := strings.Trim(imp.Path, `"' `)
		if path == "" {
			continue
		}
		seg := path[strings.LastIndexByte(path, '/')+1:]
		if seg != qual {
			continue
		}
		if path == defPkg || strings.HasSuffix(path, "/"+defPkg) {
			return true
		}
	}
	return false
}

// callerAttributedBare mirrors the graph's callee-side resolution of a bare
// callee (CallersFor's bare-bucket gate, shared by the method branch and the
// shared-name func path):
//   - a receiver-qualified caller ("Client.audit") is resolved package-aware
//     over every receiver-matched candidate, so the same-package def wins
//     (callerHasLocalDef);
//   - a bare caller needs an unambiguous resolution — every def must live in
//     this package (callerInPackage);
//   - either form is additionally attributed when the caller's package
//     imports this def's package (callerImportsDefPkg);
//   - a caller that IS the def itself (a self-edge recorded through a
//     chained/receiver form — "Schema.check" -> "Schema.check") is never a
//     caller.
func (ix *Index) callerAttributedBare(caller string, s Symbol, dir string) bool {
	if caller == s.FullName() {
		return false
	}
	if strings.Contains(caller, ".") {
		return callerHasLocalDef(ix, caller, dir) || ix.callerImportsDefPkg(caller, s)
	}
	// A bare caller whose name has a UNIQUE receiver-less definition
	// project-wide resolves to that definition — the graph's resolveNodeID
	// qualified-match (a receiver-less func's Qualified IS the bare name, so
	// it wins even when methods share the simple name; e.g. tokenize.Count
	// with five Count methods beside the func). Mirror the graph's
	// caller-anchored callee resolution (resolveEndpointPackageAware): the
	// bare callee resolves with the same-package preference anchored on the
	// CALLER's unique definition, so the edge is s only when s lives in that
	// definition's package. callerHasLocalDef would be wrong here — it counts
	// any same-named def (methods included) in dir, while the graph resolves
	// the caller to its unique receiver-less definition alone. Without this
	// branch, a shared caller name dropped real same-package callers (Phase 5
	// Part C: tokenize.Default's caller Count — Count() literally calls
	// Default()); and without the receiver-less-only check it would
	// over-attribute (mcp/etag's Registry.Count method must not make the
	// tokenize Count a caller of mcp/etag.Default). The import-based
	// attribution is anchored the same way (ADV-4): only the unique
	// receiver-less definition's package imports may attribute the edge —
	// a same-named method's package imports must not (callerImportsDefPkg
	// consults every same-named def, methods included, so the branch uses
	// the anchored form).
	if uniqueReceiverLessDef(ix, caller) {
		for _, cd := range ix.symbolsFor(caller) {
			if cd.Receiver == "" {
				if filepath.Dir(cd.File) == dir {
					return true
				}
				return ix.callerImportsDefPkgAnchored(cd, s)
			}
		}
		return false
	}
	return callerInPackage(ix, caller, dir) || ix.callerImportsDefPkg(caller, s)
}

// callerImportsDefPkg mirrors the graph's import-based resolution of a BARE
// callee (resolveEndpointPackageAware's import branch): a caller in a package
// that does NOT itself define the bare name still calls this def when its
// package imports the def's package (the internal/web rate-limit test drives
// the SDK's Client.do through an HTTP client). Same-package callers are
// handled by callerHasLocalDef.
func (ix *Index) callerImportsDefPkg(caller string, s Symbol) bool {
	// A bare callee with a predeclared identifier's name ("append", "len")
	// is the builtin unless the caller's package shadows it with a
	// package-level definition — never an import of a package defining a
	// same-named symbol (Go scoping; methods are not bare-callable).
	if isPredeclared(s.Name) {
		return false
	}
	// A bare callee can never name a METHOD: a bare reference resolves to a
	// package-level func or the builtin (Go scoping) — an import of the
	// method's package must not capture the call (a local closure named
	// "do" in internal/web is not the SDK's Client.do).
	if s.Receiver != "" {
		return false
	}
	callerDefs := ix.symbolsFor(caller)
	if len(callerDefs) == 0 {
		return false
	}
	// The caller's package must not define the bare name itself: the graph's
	// same-package preference resolves the bare callee to that local
	// definition, never to s.
	for _, cd := range callerDefs {
		cdir := filepath.Dir(cd.File)
		for _, d := range ix.symbolsFor(s.Name) {
			if filepath.Dir(d.File) == cdir {
				return false
			}
		}
	}
	filePkg := ix.filePkgIndex()
	defPkg := filePkg[s.File]
	if defPkg == "" {
		return false
	}
	for _, cd := range callerDefs {
		callerPkg := filePkg[cd.File]
		if callerPkg == "" || callerPkg == defPkg {
			continue
		}
		p := ix.Pkgs[callerPkg]
		if p == nil {
			continue
		}
		if pkgImportsDef(p, defPkg) {
			return true
		}
	}
	return false
}

// pkgImportsDef reports whether a package's import list contains the def's
// package (exact import path or repo-relative suffix), mirroring the graph's
// importMatchesQualifier.
func pkgImportsDef(p *Pkg, defPkg string) bool {
	for _, imp := range p.Imports {
		path := strings.Trim(imp.Path, `"' `)
		if path == "" {
			continue
		}
		if path == defPkg || strings.HasSuffix(path, "/"+defPkg) {
			return true
		}
	}
	return false
}

// callerImportsDefPkgAnchored is callerImportsDefPkg anchored on ONE caller
// definition (ADV-4): the import attribution runs only against the caller's
// unique receiver-less definition, never against its same-named methods. The
// graph resolves a bare caller with a unique receiver-less definition to that
// definition alone (resolveNodeID's qualified-match), so only ITS package's
// imports may attribute a bare edge to s — and only ITS package's local
// definitions may shadow the bare callee (Go same-package preference). The
// unanchored callerImportsDefPkg keeps the all-defs semantics for callers
// that remain ambiguous.
func (ix *Index) callerImportsDefPkgAnchored(callerDef Symbol, s Symbol) bool {
	// A bare callee with a predeclared identifier's name ("append", "len")
	// is the builtin unless the caller's package shadows it with a
	// package-level definition — never an import of a package defining a
	// same-named symbol (Go scoping; methods are not bare-callable).
	if isPredeclared(s.Name) {
		return false
	}
	// A bare callee can never name a METHOD: a bare reference resolves to a
	// package-level func or the builtin (Go scoping) — an import of the
	// method's package must not capture the call (a local closure named
	// "do" in internal/web is not the SDK's Client.do).
	if s.Receiver != "" {
		return false
	}
	// The caller's package must not define the bare name itself: the graph's
	// same-package preference resolves the bare callee to that local
	// definition, never to s. Checked for the anchored definition's package
	// alone — a same-named method's package cannot shadow the anchored
	// caller's bare reference.
	cdir := filepath.Dir(callerDef.File)
	for _, d := range ix.symbolsFor(s.Name) {
		if filepath.Dir(d.File) == cdir {
			return false
		}
	}
	filePkg := ix.filePkgIndex()
	defPkg := filePkg[s.File]
	if defPkg == "" {
		return false
	}
	callerPkg := filePkg[callerDef.File]
	if callerPkg == "" || callerPkg == defPkg {
		return false
	}
	p := ix.Pkgs[callerPkg]
	if p == nil {
		return false
	}
	return pkgImportsDef(p, defPkg)
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
// Methods and unambiguous package-level symbols keep the recorded bucket
// byte-identical. When two or more packages define the same simple name, the
// shared caller bucket mixes every same-named symbol's callees, so each edge
// is attributed to s only when the callee could have been recorded from s's
// own file: a bare callee that resolves into s's package (or stays foreign),
// or a qualified callee whose qualifier names an import of s's file or a type
// declared in s's package. A package-qualified callee naming s's own package
// ("governance.NewFirewall") was recorded by a same-named symbol in another
// file — s never qualifies its own package — and never leaks into s's list.
func (ix *Index) CallsFor(s Symbol) []string {
	edges := ix.Calls[s.FullName()]
	if s.Receiver != "" || len(ix.symbolsFor(s.Name)) == 1 || s.File == "" {
		return dedupeSorted(CallEdgeTargets(edges))
	}
	var out []string
	for _, e := range edges {
		if calleeAttributable(ix, e.Target, s) {
			out = append(out, e.Target)
		}
	}
	return dedupeSorted(out)
}

// calleeAttributable reports whether a recorded callee target belongs to s.
func calleeAttributable(ix *Index, c string, s Symbol) bool {
	i := strings.LastIndexByte(c, '.')
	if i <= 0 || i+1 >= len(c) {
		// Bare callee: keep when it resolves into s's package, or when it is
		// foreign — builtins and stdlib — which s's source can reference
		// without a qualifier. A bare name with no same-package declaration
		// resolves to the predeclared identifier (Go scoping), so a local
		// symbol of the same name in an unrelated package is never the
		// recorded callee.
		matches := ix.symbolsFor(c)
		if len(matches) == 0 {
			return true
		}
		dir := filepath.Dir(s.File)
		for _, m := range matches {
			if filepath.Dir(m.File) == dir {
				return true
			}
		}
		return isPredeclared(c)
	}
	qualifier := c[:i]
	// Package-qualified call from s's own file: the qualifier must name one
	// of s's imports ("fmt.Errorf", "domain.X"). A qualifier matching no
	// import of s's file was recorded from a different package's file (the
	// same-named twin), never from s.
	if ix.ImportsByFile != nil {
		for _, imp := range ix.ImportsByFile[s.File] {
			if filepath.Base(imp.Path) == qualifier {
				return true
			}
		}
	}
	// Receiver-qualified call on a type declared in s's package
	// ("Hooks.LoadIndex" from the wrapper, "Firewall.Check" from the core).
	if q0 := qualifier[0]; q0 >= 'A' && q0 <= 'Z' {
		dir := filepath.Dir(s.File)
		for _, t := range ix.symbolsFor(qualifier) {
			if filepath.Dir(t.File) == dir {
				return true
			}
		}
		return false
	}
	// Lowercase qualifier naming s's own package dir: a same-package call
	// recorded with a qualifier can only come from another file that imports
	// s's package — never from s itself — so it is the twin's edge.
	if d, ok := qualifiedCalleeSymbol(ix, c); ok && filepath.Dir(d.File) == filepath.Dir(s.File) {
		return false
	}
	return false
}

// isPredeclared reports whether name is a Go predeclared identifier (builtin
// function, type, constant or nil). A bare callee with that name and no
// same-package declaration is the builtin, never an unrelated package's
// same-named symbol (cross-package references always carry a qualifier).
func isPredeclared(name string) bool {
	switch name {
	case "append", "cap", "clear", "close", "complex", "copy", "delete",
		"imag", "len", "make", "max", "min", "new", "panic", "print",
		"println", "real", "recover",
		"bool", "byte", "complex64", "complex128", "error", "float32",
		"float64", "int", "int8", "int16", "int32", "int64", "rune",
		"string", "uint", "uint8", "uint16", "uint32", "uint64", "uintptr",
		"true", "false", "iota", "nil":
		return true
	}
	return false
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
	// The graph text names definitions, callers and callees across several
	// files; the savings denominator must cover ALL of those node sources
	// (finding V1), so collect the distinct files backing the rendered nodes
	// — the definitions' own files plus each resolved caller/callee's file.
	nodeFiles := map[string]bool{}
	for _, d := range defs {
		if d.File != "" {
			nodeFiles[d.File] = true
		}
	}
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
			if d, ok := resolveName(ix, c); ok {
				if d.File != "" {
					nodeFiles[d.File] = true
				}
			} else {
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
			if d, ok := resolveName(ix, c); ok {
				if d.File != "" {
					nodeFiles[d.File] = true
				}
			} else {
				b.WriteString("  (unresolved)")
			}
			b.WriteString("\n")
		}
	}
	result := strings.TrimSuffix(b.String(), "\n")
	files := make([]string, 0, len(nodeFiles))
	for f := range nodeFiles {
		files = append(files, f)
	}
	slices.Sort(files)
	stats := tokstats.TokenSavingsForGraph(ix.Root, files, result)
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
	return ix.ContextDef(defs[0], linesAround)
}

// ContextDef slices source for an EXPLICIT symbol — definition window plus
// callers/callees — without re-resolving the name. Context(symbol, n)
// re-resolves a bare name with a different first-match order than the
// caller's own resolution, which could mix candidates (e.g. a TS interface
// definition with a Go method source window for the same bare name).
func (ix *Index) ContextDef(d Symbol, linesAround int) string {
	if linesAround <= 0 {
		linesAround = 12
	}
	var b strings.Builder
	src, err := os.ReadFile(filepath.Join(ix.Root, d.File))
	if err != nil {
		return ""
	}
	all := strings.Split(string(src), "\n")
	start := d.Line - linesAround
	if start < 1 {
		start = 1
	}
	// Function-aware extent (CG-P0-3): every extractor records the symbol's
	// syntactic end line (go/ast, tree-sitter node ranges, regex brace
	// heuristics), so the window extends at least to the definition's end —
	// a truncated body is never served when the full one is known.
	end := d.Line + linesAround
	if d.End > d.Line && d.End > end {
		end = d.End
	}
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
		b.WriteString(strings.Join(contextCallerLabels(ix, callers), ", "))
		b.WriteString("\n")
	}
	if callees := ix.CallsFor(d); len(callees) > 0 {
		b.WriteString("calls: ")
		b.WriteString(strings.Join(callees, ", "))
		b.WriteString("\n")
	}
	result := strings.TrimSuffix(b.String(), "\n")
	stats := tokstats.TokenSavingsForContext(ix.Root, d.File, result)
	if summary := stats.Summary(); summary != "" {
		result += "\n\n" + summary
	}
	return result
}

// maxContextCallers caps the callers line of a context slice. Context is a
// token-budget tool: a hub with hundreds of callers must not blow the budget
// on one line, so the row list stops at maxContextCallers and the remainder
// is summarized as a count.
const maxContextCallers = 10

// contextCallerLabels renders each caller as "name (file:line)" when its
// definition resolves against the index symbol map, capped at
// maxContextCallers rows (the remainder collapses to "… and N more"). A
// caller whose definition cannot be resolved (cross-package qualified ref,
// nodesForIDs limitation) keeps its bare name — never fabricated.
func contextCallerLabels(ix *Index, callers []string) []string {
	shown := callers
	if len(shown) > maxContextCallers {
		shown = shown[:maxContextCallers]
	}
	out := make([]string, 0, len(shown)+1)
	for _, c := range shown {
		label := c
		if d, ok := ix.ResolveName(c); ok && d.File != "" && !ambiguousDef(ix, c) {
			label = fmt.Sprintf("%s (%s:%d)", c, d.File, d.Line)
		}
		out = append(out, label)
	}
	if len(callers) > maxContextCallers {
		out = append(out, fmt.Sprintf("… and %d more", len(callers)-maxContextCallers))
	}
	return out
}

// rewriteConstructorCallees rewrites callee qualifiers that are constructor
// names into the constructor's single return type. Two shapes:
//
//	"New.applyFileResult"   -> "Index.applyFileResult"   (func constructor)
//	"s.newGov.Filter"       -> "Gov.Filter"              (method constructor
//	                                                      via a multi-value
//	                                                      receiver-var assign)
//
// The rewrite is conservative: it fires only when the constructor's FIRST
// return names an actual type symbol declared in the project (Go convention
// puts the error last, so "gov, err := ..." maps through Returns[0]).
// Unambiguous method names only (a duplicated method name skips the rewrite).
// Runs before the Callers inversion in every build path (buildSerial,
// buildParallel, Update all call computeCallers).
func (ix *Index) rewriteConstructorCallees() {
	if len(ix.Symbols) == 0 {
		return
	}
	ctorRet := map[string]string{} // func name -> first return type
	ctorDup := map[string]bool{}   // shared bare constructor names skip the rewrite
	methodRet := map[string]string{}
	methodDup := map[string]bool{}
	types := map[string]bool{}
	for _, s := range ix.Symbols {
		switch s.Kind {
		case "func":
			// The FIRST return is the constructed value; Go convention puts
			// the error last ("gov, err := ..."). The types[] check at
			// rewrite time keeps this honest. A shared bare constructor name
			// (two packages both declaring "New") is marked ambiguous and
			// skips the rewrite — the same guard the methodRet map has
			// (finding A6): last-write-wins over ix.Symbols order attributed
			// a bare "New.M" to an arbitrary package's type.
			if len(s.Returns) >= 1 {
				if _, dup := ctorRet[s.Name]; dup {
					ctorDup[s.Name] = true
					continue
				}
				ctorRet[s.Name] = s.Returns[0]
			}
		case "method":
			if len(s.Returns) >= 1 {
				if _, dup := methodRet[s.Name]; dup {
					methodDup[s.Name] = true
					continue
				}
				methodRet[s.Name] = s.Returns[0]
			}
		case "type", "struct", "interface":
			types[s.Name] = true
		}
	}
	// Package-merged struct field types ("App.taskSvc" -> "TaskService"),
	// collected from every file of every package. Both the project-wide map
	// and the per-package maps merge in SORTED package-path order: a field
	// key shared by two packages ("Client.http" — internal/mcpclient's
	// *httpClient vs the Go SDK's *http.Client) previously resolved by
	// first-write-wins over random ix.Pkgs map iteration, which made the
	// final index nondeterministic and occasionally forged a self-edge
	// ("Client.http.roundTrip" rewritten against the wrong package's field
	// type became "Client.roundTrip").
	paths := make([]string, 0, len(ix.Pkgs))
	for p := range ix.Pkgs {
		paths = append(paths, p)
	}
	slices.Sort(paths)
	fieldTypes := map[string]string{}
	pkgFieldTypes := map[string]map[string]string{}
	filePkg := map[string]string{}
	for _, path := range paths {
		p := ix.Pkgs[path]
		pkgFieldTypes[path] = p.StructFields
		for _, f := range p.Files {
			if _, dup := filePkg[f]; !dup {
				filePkg[f] = path
			}
		}
		for k, v := range p.StructFields {
			if _, dup := fieldTypes[k]; !dup {
				fieldTypes[k] = v
			}
		}
	}
	// Package-merged constructor return types ("api.NewHandlers" ->
	// "Handlers"), keyed by both the package name and the import-path base
	// so calls resolve whether the source alias matched the package name
	// or the directory. Sorted package iteration keeps the first-wins
	// resolution deterministic (same collision class as the field types).
	ctorQual := map[string]string{}
	for _, path := range paths {
		p := ix.Pkgs[path]
		for k, v := range p.Constructors {
			if _, dup := ctorQual[p.Name+"."+k]; !dup {
				ctorQual[p.Name+"."+k] = v
			}
			if base := filepath.Base(p.Path); base != p.Name && base != "" {
				if _, dup := ctorQual[base+"."+k]; !dup {
					ctorQual[base+"."+k] = v
				}
			}
		}
	}
	// ownerPkg resolves a caller/owner key to its package so the
	// receiver-field chain rewrites against the OWNER's package's field map
	// first: the chain's first segment is the owner's receiver type
	// (resolveCallee at extract time resolved the receiver variable), so the
	// field belongs to that type in that package — the project-wide map must
	// not let a same-named field in another package win the rewrite.
	ownerPkg := func(owner string) string {
		defs := ix.symbolsFor(owner)
		if len(defs) != 1 {
			return ""
		}
		return filePkg[defs[0].File]
	}
	// fieldOf resolves a "Struct.field" prefix against the owner's package
	// first, then the deterministic sorted-merge project-wide map — the
	// fallback for chains whose struct lives in another package.
	fieldOf := func(owner, key string) (string, bool) {
		if pkg := ownerPkg(owner); pkg != "" {
			if pm := pkgFieldTypes[pkg]; pm != nil {
				if r, ok := pm[key]; ok && types[r] {
					return r, true
				}
			}
		}
		r, ok := fieldTypes[key]
		if !ok || !types[r] {
			return "", false
		}
		return r, true
	}
	rewrite := func(c, owner string) string {
		i := strings.LastIndexByte(c, '.')
		if i <= 0 || i == len(c)-1 {
			return c
		}
		q, m := c[:i], c[i+1:]
		// Func constructor: "New.M".
		if r, ok := ctorRet[q]; ok && !ctorDup[q] && types[r] {
			return r + "." + m
		}
		// Cross-package constructor-assigned receiver: "api.NewHandlers.Routes"
		// — the extractor resolved the receiver variable to the constructor's
		// qualified name (its return type is unknown outside the package), but
		// the package-merged Constructors map completes it here. Fires only
		// when the constructor's first return names a type declared in the
		// project (conservative, mirrors the other rewrites).
		if r, ok := ctorQual[q]; ok && types[r] {
			return r + "." + m
		}
		// Method constructor via receiver-var chain: "s.newGov.M" —
		// the last qualifier segment names the method.
		if j := strings.LastIndexByte(q, '.'); j >= 0 {
			qs := q[j+1:]
			if r, ok := methodRet[qs]; ok && !methodDup[qs] && types[r] {
				return r + "." + m
			}
		}
		// Receiver-field chain: "App.taskSvc.Deploy" — the extractor
		// resolved the receiver variable's type ("a" -> App) but the struct
		// may be declared in another file, so the field -> type link is only
		// visible here. Walk the longest matching "Struct.field" prefix and
		// rewrite to the field's type; iterate so multi-level chains
		// ("App.svc.client.M") resolve fully. Fires only when the field's
		// type is a type declared in the project (conservative, mirrors the
		// constructor rewrite).
		for {
			segs := strings.Split(c, ".")
			if len(segs) < 3 {
				break
			}
			rewritten := false
			for k := 1; k < len(segs)-1; k++ {
				key := strings.Join(segs[:k+1], ".")
				if r, ok := fieldOf(owner, key); ok {
					c = r + "." + strings.Join(segs[k+1:], ".")
					rewritten = true
					break
				}
			}
			if !rewritten {
				break
			}
		}
		return c
	}
	for caller, callees := range ix.Calls {
		for i, ce := range callees {
			ix.Calls[caller][i].Target = rewrite(ce.Target, caller)
		}
	}
}

// CallersIncludingAliases returns every recorded caller for a symbol's full
// name: the canonical callers plus simple-name aliases (edges the extractor
// recorded under a receiver-qualified or package-qualified callee form, e.g.
// "New.applyFileResult" when a constructor-inferred receiver variable is
// resolved to the constructor's name). Deletion/dead-code analysis must use
// this: MISSING a caller is the dangerous direction, and over-reporting only
// errs toward "unsafe to delete", never toward "safe".
func (ix *Index) CallersIncludingAliases(full string) []string {
	out := append([]string(nil), ix.Callers[full]...)
	if simple := simpleKey(full); simple != full {
		out = append(out, ix.AliasCallers[simple]...)
	} else {
		out = append(out, ix.AliasCallers[full]...)
	}
	return dedupeSorted(out)
}
