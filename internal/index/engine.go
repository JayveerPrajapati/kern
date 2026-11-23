package index

import (
	"regexp"
	"sync"
	"time"

	"github.com/JayveerPrajapati/kern/internal/indexwatch"
)

// indexVersion is bumped whenever the persisted index schema or derived-data
// semantics change, so stale caches are rebuilt automatically. v16 preserves
// enclosing declarations alongside call source files; v17 retains Go import
// aliases and qualified, lexically-scoped receiver types; v18 adds per-file
// JS/TS default/reexport records; v19 requires explicit runtime export visibility;
// v20 preserves compiler-selected Go declaration IDs and binding mode;
// v21 adds javac identities and compiler-derived dispatch candidate provenance;
// v22 records Go type-alias RHS text (asttypes.Symbol.Alias) so impact/refs/
// explore can see through `type A = B` instead of reporting an alias all-clear.
const indexVersion = 22

// BuildStats records how an index was produced, surfaced by `kern index
// --status` and the build/update JSON summaries so build cost is visible
// without profiling: Kind is "build" (full parse) or "update" (incremental
// refresh), Ms the wall-clock duration of the producing pass, Workers the
// parse pool size actually used (1 = serial path or small-tree bypass),
// Reused the per-file results copied from a prior index. A zero Kind marks
// indexes produced before this field existed (or hand-constructed ones):
// consumers report nothing, not zeros.
type BuildStats struct {
	Kind    string `json:"kind"`
	Ms      int64  `json:"ms"`
	Workers int    `json:"workers"`
	Reused  int    `json:"reused"`
}

// Metadata is the metadata-only index view: enough to answer counts and a
// freshness proof without loading symbols or call edges (`kern index where` /
// `kern index --status`). Index carries Identity, FileHashes and the small
// derived maps; Symbols and Calls are intentionally empty.
type Metadata struct {
	Index    *Index
	Symbols  int
	Files    int
	Packages int
}

// Index is the in-memory representation of a project's AST index.
type Index struct {
	Root          string                `json:"root"`
	Version       int                   `json:"version"`
	Symbols       []Symbol              `json:"symbols"`
	Calls         map[string][]CallEdge `json:"calls"`
	Callers       map[string][]string   `json:"callers"`
	GoTypes       bool                  `json:"go_types,omitempty"`
	JavaCompiler  bool                  `json:"java_compiler,omitempty"`
	JavaClasspath string                `json:"java_classpath,omitempty"`
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
	ImportsByFile map[string][]ImportEdge `json:"imports_by_file,omitempty"`
	// ExportsByFile maps a JS/TS source file to its explicit exports and
	// reexports, so imports of those names bind to the declaring symbol.
	ExportsByFile  map[string][]ExportEdge `json:"exports_by_file,omitempty"`
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
	// freshnessMemo caches the last non-strict FreshnessProof verdict
	// (single-flight + TTL — see identity.go) so warm tool calls do not
	// re-walk the tree or re-stage git per call. Never serialized.
	freshnessMemo *proofMemo `json:"-"`
	// PromotedLowEdges / UnresolvedLowEdges are the finalize-time
	// reconciliation counters (CG-P0-5): call edges whose target resolved
	// against the completed symbol table and was promoted from LOW to
	// MEDIUM, and LOW edges that remain genuinely unresolved. Surfaced in
	// kern_health; zero on indexes built before the pass existed.
	PromotedLowEdges   int `json:"promoted_low_edges,omitempty"`
	UnresolvedLowEdges int `json:"unresolved_low_edges,omitempty"`
	// DroppedSymbolConflicts counts same-identity duplicate symbols that
	// were QUARANTINED (later duplicates dropped, first declaration kept)
	// instead of failing the build — the availability-over-strictness
	// policy: one file's extractor anomaly must never take the whole
	// repo's index down. Surfaced in `kern index --status`; the build also
	// warns on stderr per file. Reflects the last FULL build (delta updates
	// carry the stored value forward). Zero on indexes built before the
	// field existed — and on every build that had no conflicts.
	DroppedSymbolConflicts int `json:"dropped_symbol_conflicts,omitempty"`
	// CallResolution counts distinct call targets and how many of them fail
	// to resolve against the symbol table, split by cause (External =
	// foreign-import-qualified stdlib/vendor targets, expected unresolved;
	// InRepo = the honest actionable share). Surfaced in `kern index
	// --status`: a high external share on a real repo is expected and
	// correct; the in-repo share is the number whose regressions are
	// provable. Zero on indexes built before this pass existed.
	CallResolution CallResStats `json:"call_resolution,omitempty"`
	// BuildStats records how this index was last produced (kind, wall
	// duration, workers, reused per-file results) so `kern index --status`
	// can surface build cost. Zero Kind on indexes built before this field
	// existed. Surfaced read-only; never consulted by build/freshness logic.
	BuildStats BuildStats `json:"build_stats,omitempty"`
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
	staleMu       *sync.Mutex
	staleSnapshot *FreshnessSnapshot
	// reusedResults counts per-file results reused from a prior index in the
	// build that produced this one (0 for full builds). Exposed via
	// ReusedResults for diagnostics.
	reusedResults int
	// Adjacency is the persisted caller set (identity -> caller names) built
	// once per save; CallersFor serves from it instead of rebuilding the
	// export catalog per process. nil for in-memory/JSON-loaded indexes.
	Adjacency map[string][]string `json:"-"`
	// skipPersist marks a no-op Update result (content-identical to the
	// persisted store): Save writes nothing because there is nothing new.
	skipPersist bool
	// delta names the files/owners an Update changed so Save can write those
	// rows instead of rewriting the entire store (typically 900ms -> ~60ms).
	delta *deltaPlan
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
	symbolOnce  sync.Once
	kindOnce    sync.Once
	symbolIdx   map[string][]Symbol
	kindIdx     map[string][]Symbol
	bindingOnce sync.Once
	bindings    *exportCatalog
	communities sync.Once
	declComm    map[string]string
	callerOnce  sync.Once
	callerIdx   *callerIndex
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

// ignoreDirs is the walk-skip policy shared with the extracted hashing family
// (internal/indexwatch) — the single map lives there so the build walk and
// the freshness/hash walks can never diverge.
var ignoreDirs = indexwatch.IgnoreDirs

// IgnoredDir reports whether a directory name is skipped during index walks
// (node_modules, vendor, build artifacts, ...). Exported for downstream
// scanners that mirror the index's file-selection policy.
func IgnoredDir(name string) bool { return ignoreDirs[name] }

var (
	symbolRegexCacheMu sync.RWMutex
	// symbolRegexCache maps a stripped search pattern to its compiled regex.
	// Compiled regexes are immutable and safe for concurrent use, so one
	// cached entry may be shared across concurrent Search calls.
	symbolRegexCache = make(map[string]*regexp.Regexp)
	// symbolRegexOrder tracks insertion order for FIFO eviction.
	symbolRegexOrder []string
)
