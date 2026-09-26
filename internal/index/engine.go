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
	// reusedResults counts per-file results reused from a prior index in the
	// build that produced this one (0 for full builds). Exposed via
	// ReusedResults for diagnostics.
	reusedResults int
	// symbolIdx is the precomputed name -> symbols lookup for symbolsFor;
	// nil means symbolsFor falls back to the linear scan.
	symbolIdx map[string][]Symbol
	// kindIdx is the precomputed kind -> symbols lookup for kind-filtered
	// Search queries ("func foo", "type Bar", "entry */admin*"); nil means
	// Search falls back to the linear scan over all symbols.
	kindIdx map[string][]Symbol
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
			ix.initMaps()
			ix.reindexByFile()
			ix.buildSymbolIndex()
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
	ix.initMaps()
	ix.reindexByFile()
	ix.buildSymbolIndex()
	metrics.Default().RecordCacheHit()
	return ix, nil
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
	// Fast path (Rec P0-3): same snapshot preference as Load; JSON canonical.
	if ix, ok := loadBinSnapshot(path); ok {
		if ix.Version != indexVersion {
			return nil, fmt.Errorf("index version %d (want %d): rebuild required", ix.Version, indexVersion)
		}
		ix.initMaps()
		ix.reindexByFile()
		ix.buildSymbolIndex()
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
	ix.reindexByFile()
	ix.buildSymbolIndex()
	return ix, nil
}

func (ix *Index) reindexByFile() {
	ix.SymbolsByFile = map[string][]Symbol{}
	for _, s := range ix.Symbols {
		ix.SymbolsByFile[s.File] = append(ix.SymbolsByFile[s.File], s)
	}
	// The kind buckets must be built from the FINAL symbol table: the
	// build/update sequences call this after resolveEntries (which flips
	// Entry on func/method symbols), and the load paths call it on persisted
	// symbols whose Entry flags are already baked in.
	ix.buildKindIndex()
}

// buildKindIndex precomputes the kind -> symbols buckets that let
// kind-filtered Search queries iterate only matching-kind symbols instead of
// scanning every one. Bucket membership mirrors symbolMatches exactly, in
// Symbols order:
//   - every non-empty Kind gets an exact bucket ("func", "method", ...),
//     except "entry" (a flag, not a Kind) and the searchTypeKinds members,
//     which land in the "type" super-category bucket instead;
//   - every symbol with Entry set lands in the "entry" bucket;
//   - "type" holds every symbol whose Kind is in searchTypeKinds.
//
// Search re-applies symbolMatches on top of the bucket, so a bucket is only
// ever a candidate superset — results and limit behavior are identical to
// the pre-index linear scan.
func (ix *Index) buildKindIndex() {
	ix.kindIdx = make(map[string][]Symbol)
	for _, s := range ix.Symbols {
		if s.Kind != "" && s.Kind != "entry" && !searchTypeKinds[s.Kind] {
			ix.kindIdx[s.Kind] = append(ix.kindIdx[s.Kind], s)
		}
		if searchTypeKinds[s.Kind] {
			ix.kindIdx["type"] = append(ix.kindIdx["type"], s)
		}
		if s.Entry {
			ix.kindIdx["entry"] = append(ix.kindIdx["entry"], s)
		}
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
		ix.applyFileResult(reuseOrCompute(cfg.prior, rel, src, mtime, &cfg.reused))
		return nil
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
			applied.Store(int64(nextSeq))
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

func (ix *Index) computeCallers() {
	// Resolve constructor-inferred callee qualifiers now that the full
	// package symbol set is merged (per-file extraction cannot see
	// cross-file constructors): "New.M" -> "Index.M" and
	// "Server.newGov.M" -> "Gov.M". Conservative — single-return
	// constructors whose return type is an actual declared type only.
	ix.rewriteConstructorCallees()
	ix.Callers = map[string][]string{}
	ix.AliasCallers = map[string][]string{}
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
// no kind index (hand-built Index structs that never ran reindexByFile), and
// Search falls back to the linear scan — the pre-index behavior.
func (ix *Index) kindSymbols(kind string) ([]Symbol, bool) {
	if ix.kindIdx == nil {
		return nil, false
	}
	syms, ok := ix.kindIdx[kind]
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
	return dedupeSorted(CallEdgeTargets(ix.Calls[s.FullName()]))
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
	methodRet := map[string]string{}
	methodDup := map[string]bool{}
	types := map[string]bool{}
	for _, s := range ix.Symbols {
		switch s.Kind {
		case "func":
			// The FIRST return is the constructed value; Go convention puts
			// the error last ("gov, err := ..."). The types[] check at
			// rewrite time keeps this honest.
			if len(s.Returns) >= 1 {
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
	// collected from every file of every package.
	fieldTypes := map[string]string{}
	for _, p := range ix.Pkgs {
		for k, v := range p.StructFields {
			if _, dup := fieldTypes[k]; !dup {
				fieldTypes[k] = v
			}
		}
	}
	// Package-merged constructor return types ("api.NewHandlers" ->
	// "Handlers"), keyed by both the package name and the import-path base
	// so calls resolve whether the source alias matched the package name
	// or the directory.
	ctorQual := map[string]string{}
	for _, p := range ix.Pkgs {
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
	rewrite := func(c string) string {
		i := strings.LastIndexByte(c, '.')
		if i <= 0 || i == len(c)-1 {
			return c
		}
		q, m := c[:i], c[i+1:]
		// Func constructor: "New.M".
		if r, ok := ctorRet[q]; ok && types[r] {
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
				if r, ok := fieldTypes[key]; ok && types[r] {
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
			ix.Calls[caller][i].Target = rewrite(ce.Target)
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
