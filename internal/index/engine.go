package index

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/cache"
)

// indexVersion is bumped whenever the persisted index schema changes, so
// stale caches are rebuilt automatically instead of serving zero-value fields.
const indexVersion = 7

// Index is the in-memory representation of a project's AST index.
type Index struct {
	Root    string              `json:"root"`
	Version int                 `json:"version"`
	Symbols []Symbol            `json:"symbols"`
	Calls   map[string][]string `json:"calls"`
	Callers map[string][]string `json:"callers"`
	// AliasCallers maps a bare name to the callers of dotted callees with that
	// bare name ("Println" -> callers of "fmt.Println"). It is a lookup aid
	// for names that do not resolve to a local symbol; it never contributes
	// callers to a resolved local symbol, so a call to fmt.Println can never
	// show up as a caller of an unrelated local Println (W2-15, W2-16).
	AliasCallers map[string][]string `json:"alias_callers,omitempty"`
	// Inherits maps a subtype's full name to its bases, each tagged with the
	// edge kind ("extends:Animal", "implements:Pet", "embeds:Base"). InheritedBy
	// is the reverse map (base -> subtypes) for find-implementations queries.
	Inherits       map[string][]string `json:"inherits,omitempty"`
	InheritedBy    map[string][]string `json:"inherited_by,omitempty"`
	Pkgs           map[string]*Pkg     `json:"packages"`
	FileHashes     map[string]string   `json:"file_hashes"`
	GeneratedFiles map[string]bool     `json:"generated_files,omitempty"`
	SymbolsByFile  map[string][]Symbol `json:"-"`
	UpdatedAt      time.Time           `json:"updated_at"`
	// MaxMtime is the largest file modification time (Unix nanos) across the
	// indexable files at build time. Stale() uses it as a cheap generation
	// gate so repeated freshness checks short-circuit without re-hashing
	// every file; the exact content-hash manifest check only runs when the
	// gate trips (adopted from code-graph-mcp's generation-counter cache
	// invalidation).
	MaxMtime int64 `json:"max_mtime,omitempty"`
}

// New returns an empty index rooted at root.
func New(root string) *Index {
	return &Index{
		Root:           root,
		Version:        indexVersion,
		Calls:          map[string][]string{},
		Callers:        map[string][]string{},
		Inherits:       map[string][]string{},
		InheritedBy:    map[string][]string{},
		Pkgs:           map[string]*Pkg{},
		FileHashes:     map[string]string{},
		GeneratedFiles: map[string]bool{},
		SymbolsByFile:  map[string][]Symbol{},
	}
}

// StorePath returns the on-disk location for the index of root.
func StorePath(root string) string {
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = root
	}
	return cache.Path("index", cache.Hash([]byte(abs))+".json")
}

// Save persists the index. The write is atomic (temp file + rename) so a
// concurrent reader never observes a partially-written index.
func (ix *Index) Save() error {
	data, err := json.Marshal(ix)
	if err != nil {
		return err
	}
	p := StorePath(ix.Root)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// Load reads the index for root. Returns nil if absent.
func Load(root string) (*Index, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = root
	}
	data, err := os.ReadFile(StorePath(abs))
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
	ix.reindexByFile()
	return ix, nil
}

// Stale reports whether the cached index no longer matches the files on disk:
// a source file was added, removed, or edited since the index was built. The
// check hashes the current indexable file set against the manifest captured at
// build time, so intel commands never silently serve out-of-date call graphs.
func (ix *Index) Stale() bool {
	if ix == nil || len(ix.FileHashes) == 0 {
		return true
	}
	// Fast gate: if the indexable file count and newest modification time both
	// match the build-time manifest, nothing changed, so skip re-hashing. A
	// gate miss falls through to the exact hash comparison. Old indexes carry
	// MaxMtime == 0, which never matches a fresh build, so they always take
	// the exact path. Tradeoff: an edit that preserves mtime (rare, e.g.
	// deliberately-touched timestamps) evades the gate until the next build.
	if ix.MaxMtime > 0 {
		if maxMtime, count, err := indexableMaxMtime(ix.Root); err == nil && count == len(ix.FileHashes) && maxMtime == ix.MaxMtime {
			return false
		}
	}
	cur, err := indexableHashes(ix.Root)
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
}

// IgnoredDir reports whether a directory name is skipped during index walks
// (node_modules, vendor, build artifacts, ...). Exported for downstream
// scanners that mirror the index's file-selection policy.
func IgnoredDir(name string) bool { return ignoreDirs[name] }

// Build walks root, parses every source file and assembles the index. On
// error it returns a nil index so a half-built index is never mistaken for a
// usable one.
func Build(root string) (*Index, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	ix := New(abs)
	err = filepath.WalkDir(abs, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != abs && ignoreDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		rel, rerr := filepath.Rel(abs, path)
		if rerr != nil {
			return rerr
		}
		if !quickExt(rel) && filepath.Ext(rel) != "" {
			return nil
		}
		src, serr := os.ReadFile(path)
		if serr != nil {
			return serr
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
	ix.resolveEntries()
	ix.reindexByFile()
	return ix, nil
}

func (ix *Index) addFile(rel string, src []byte) {
	lang := detectLang(rel, src)
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
	ix.FileHashes[rel] = cache.Hash(src)
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
		} else {
			ix.Pkgs[pkg.Path] = pkg
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
			// Package-qualified local calls ("db.Open") are recorded under
			// their qualified key, not the symbol's own key ("Open"); merge
			// them onto the local symbol when the qualifier names the
			// symbol's package directory. Foreign targets (fmt.Println) and
			// unresolved receiver calls (v.M) never merge — that would forge
			// phantom callers for unrelated same-named symbols (W2-15).
			if d, ok := qualifiedCalleeSymbol(ix, c); ok && d.FullName() != c {
				ix.Callers[d.FullName()] = append(ix.Callers[d.FullName()], caller)
			}
			// Lookup alias: a dotted callee is also recorded under its bare
			// name so graph lookups by simple name find foreign or unresolved
			// targets (fmt.Println -> "Println"). Aliases live in a separate
			// map and are never merged into a local symbol's caller list —
			// that would forge phantom callers for unrelated same-named
			// symbols (W2-15: fmt.Println -> local Println; W2-16:
			// Alpha.Save -> Beta.Save). Never alias a caller to itself (that
			// would forge a self-caller edge when e.g. Start calls Server.Start).
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
	if kind != "" && s.Kind != kind {
		return false
	}
	return re.MatchString(s.Name) || (s.Receiver != "" && re.MatchString(s.Receiver+"."+s.Name)) ||
		(s.Route != "" && re.MatchString(s.Route))
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

// qualifiedCalleeSymbol resolves a dotted callee key like "db.Open" to the
// local symbol whose package directory matches the qualifier. It returns
// ok=false for foreign targets ("fmt.Println") and unresolved receiver calls
// ("v.M"), whose callers must never be attributed to a local symbol of the
// same bare name (W2-15).
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
// consulted: bare-name aliases are never merged in, because a simple name can
// name many symbols (Alpha.Save vs Beta.Save) and foreign targets (fmt.Println)
// would forge phantom callers (W2-15, W2-16).
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
