package index

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
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
func Build(root string) (*Index, error) {
	start := time.Now()
	metrics.Default().RecordIndexing()
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	ix := New(abs)
	// Load .gitignore + .kernignore patterns so gitignored directories
	// (e.g. graphify-out/, dist/, large generated trees) are skipped during
	// the index walk. Without this, the index scans every file on disk
	// regardless of .gitignore, producing huge symbol counts and slow builds.
	ign := ignore.Load(abs)
	err = filepath.WalkDir(abs, func(path string, d fs.DirEntry, err error) error {
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
			return rerr
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
		src, serr := os.ReadFile(path)
		if serr != nil {
			// Skip unreadable files (e.g. broken symlinks) instead of aborting
			// the whole index build. Matches the sec scanner's behavior.
			return nil
		}
		if !isIndexable(rel, src) {
			return nil
		}
		if info, ierr := d.Info(); ierr == nil {
			if mt := info.ModTime().UnixNano(); mt > ix.MaxMtime {
				ix.MaxMtime = mt
			}
		}
		ix.addFile(rel, src)
		return nil
	})
	if err != nil {
		return nil, err
	}
	ix.UpdatedAt = time.Now().UTC()
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
	metrics.Default().RecordIndexBuild(time.Since(start))
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

func (ix *Index) addFile(rel string, src []byte) {
	lang := detectLang(rel, src)
	// Record the content hash BEFORE the parse step. FreshnessProof's
	// indexableHashes hashes every indexable file (by extension/content,
	// not parse success), so FileHashes must cover the same set — including
	// files that fail to parse (e.g. broken.go in a test fixture). Recording
	// the hash after the parse-error early-return would exclude unparseable
	// files from FileHashes while indexableHashes includes them, causing a
	// permanent ContentRoot mismatch → false "stale" → ERROR.
	ix.FileHashes[rel] = cache.Hash(src)
	var syms []Symbol
	var calls map[string][]string
	var inherits map[string][]string
	var pkg *Pkg
	if lang == "go" {
		var err error
		syms, calls, inherits, pkg, err = extract(rel, src)
		if err != nil {
			return
		}
	} else {
		syms, calls, inherits, pkg, _ = extractForeign(rel, src, lang)
	}
	if ix.GeneratedFiles == nil {
		ix.GeneratedFiles = map[string]bool{}
	}
	ix.GeneratedFiles[rel] = IsGeneratedPath(rel) || isGeneratedContent(src)
	ix.Symbols = append(ix.Symbols, syms...)
	for owner, callees := range calls {
		ix.Calls[owner] = append(ix.Calls[owner], callees...)
	}
	for subtype, bases := range inherits {
		ix.Inherits[subtype] = append(ix.Inherits[subtype], bases...)
	}
	if pkg != nil {
		if existing, ok := ix.Pkgs[pkg.Path]; ok {
			existing.Files = append(existing.Files, pkg.Files...)
			// Merge imports from every file of the package, not just the
			// first one indexed. Without this, guard's import-level
			// boundary check only ever sees the first file's imports.
			for _, imp := range pkg.Imports {
				if !slices.Contains(existing.Imports, imp) {
					existing.Imports = append(existing.Imports, imp)
				}
			}
		} else {
			ix.Pkgs[pkg.Path] = pkg
		}
		// Per-file import attribution: the extractor's pkg carries exactly
		// this file's imports (Go: pkg built from f.Imports; foreign: the
		// per-file imports list). Record the file's own imports, never the
		// package-aggregated ones, so guard can attribute a forbidden import
		// to the file that actually imports it. Files with no imports still
		// get a (empty) entry so guard can distinguish "file indexed, no
		// imports" from "old index format without imports_by_file".
		for _, file := range pkg.Files {
			// append([]string{}, ...) keeps a non-nil empty slice for files
			// with no imports so they serialize as [] (not null) in index.json.
			ix.ImportsByFile[file] = append([]string{}, pkg.Imports...)
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

func (ix *Index) symbolsFor(name string) []Symbol {
	var out []Symbol
	for _, s := range ix.Symbols {
		if s.Name == name || s.FullName() == name {
			out = append(out, s)
		}
	}
	return out
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
		b.WriteString(itoa(d.Line))
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
		b.WriteString(itoa(i))
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

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
