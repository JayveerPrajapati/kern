// Package architecture implements Architecture Governance: a declarative
// architecture.yaml rule file with named layers and per-rule metadata, evaluated
// deterministically against the standard index. Pattern rules delegate to
// guard.CheckBoundaries and layer-based checks run on top. The default build is
// stdlib-only, so YAML is parsed by a small hand-rolled parser for the fixed
// schema (plus .json); a malformed rule file fails closed.
package architecture

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/guard"
	"github.com/JayveerPrajapati/kern/internal/index"
)

// --- Engine ---

// Engine evaluates a Config against the index.
type Engine struct {
	config *Config
}

// NewEngine creates an engine over the given config.
func NewEngine(c *Config) *Engine {
	return &Engine{config: c}
}

// Check runs the full rule set against the index and files: pattern rules are
// delegated to guard.CheckBoundaries, then layer-based checks run on top.
// Output is deterministic (sorted).
func (e *Engine) Check(ix *index.Index, files []string) []Violation {
	var out []Violation
	if e == nil || e.config == nil {
		return out
	}

	// 1) Pattern rules (from/to): delegate to the intel guard layer. The guard's
	// dirMatch only does exact/prefix/suffix comparison, so normalize glob
	// suffixes (e.g. "web/**") to a form it understands: stripping "/**" lets the
	// existing prefix check ("pattern/…") match self-or-descendant.
	var br []domain.BoundaryRule
	for _, r := range e.config.Rules {
		if r.From == "" || r.To == "" {
			continue
		}
		br = append(br, domain.BoundaryRule{From: normalizeGlob(r.From), To: normalizeGlob(r.To), Action: r.Action})
	}
	severityOf := func(from, to, action string) string {
		for _, r := range e.config.Rules {
			if normalizeGlob(r.From) == from && normalizeGlob(r.To) == to && r.Action == action {
				return severity(r.Severity)
			}
		}
		return "error"
	}
	ruleIDOf := func(from, to, action string) string {
		for _, r := range e.config.Rules {
			if normalizeGlob(r.From) == from && normalizeGlob(r.To) == to && r.Action == action {
				if r.ID != "" {
					return r.ID
				}
				return r.From + "->" + r.To
			}
		}
		return from + "->" + to
	}
	for _, v := range guard.CheckBoundaries(ix, &guard.Boundaries{Rules: br}, files) {
		out = append(out, Violation{
			Violation: v,
			RuleID:    ruleIDOf(v.RuleFrom, v.RuleTo, "forbid"),
			Severity:  severityOf(v.RuleFrom, v.RuleTo, "forbid"),
		})
	}

	// 2) Layer-based checks.
	out = append(out, e.layerChecks(ix, files)...)

	sortViolations(out)
	return out
}

// layerChecks evaluates layer_from/layer_to rules and per-layer "depends"
// constraints over the resolved call graph.
func (e *Engine) layerChecks(ix *index.Index, files []string) []Violation {
	var out []Violation
	layers := buildLayerSpecs(e.config.Layers)
	if len(layers) == 0 {
		return out
	}

	// allowPairs: a (LayerFrom,LayerTo) pair with an explicit allow rule wins
	// over any forbid for that pair.
	allowPair := map[string]bool{}
	for _, r := range e.config.Rules {
		if r.Action == "allow" && r.LayerFrom != "" && r.LayerTo != "" {
			allowPair[layerKey(r.LayerFrom, r.LayerTo)] = true
		}
	}

	for _, ed := range collectEdges(ix, files) {
		fromLayers := layersContaining(layers, ed.fromDir)
		toLayers := layersContaining(layers, ed.toDir)

		// Explicit layer_from/layer_to rules.
		for _, r := range e.config.Rules {
			if r.Action != "forbid" || (r.LayerFrom == "" && r.LayerTo == "") {
				continue
			}
			fromMatch := r.LayerFrom == "" || containsLayer(fromLayers, r.LayerFrom)
			toMatch := r.LayerTo == "" || containsLayer(toLayers, r.LayerTo)
			if fromMatch && toMatch {
				if r.LayerFrom != "" && r.LayerTo != "" && allowPair[layerKey(r.LayerFrom, r.LayerTo)] {
					continue
				}
				v := Violation{
					Violation: guard.Violation{
						CallerFile: ed.callerFile,
						CalleeFile: ed.calleeFile,
						Symbol:     ed.symbol,
						Line:       ed.line,
						RuleFrom:   layerLabel(r.LayerFrom),
						RuleTo:     layerLabel(r.LayerTo),
					},
					RuleID:   layerRuleID(r),
					Severity: severity(r.Severity),
				}
				out = append(out, v)
			}
		}

		// "depends" constraint: a layer may only depend on the layers it lists
		// (plus itself). Empty depends = no constraint.
		for _, fl := range fromLayers {
			if !fl.hasDeps {
				continue
			}
			for _, tl := range toLayers {
				if tl.name == fl.name {
					continue
				}
				if !fl.depends[tl.name] && !allowPair[layerKey(fl.name, tl.name)] {
					out = append(out, Violation{
						Violation: guard.Violation{
							CallerFile: ed.callerFile,
							CalleeFile: ed.calleeFile,
							Symbol:     ed.symbol,
							Line:       ed.line,
							RuleFrom:   layerLabel(fl.name),
							RuleTo:     layerLabel(tl.name),
						},
						RuleID:   "layer.depends." + fl.name + "." + tl.name,
						Severity: "error",
					})
					break
				}
			}
		}
	}
	// Collapse the import-level and call-level findings that describe the same
	// crossing (matching guard.CheckBoundaries' dedup), preferring the call-level
	// finding which carries symbol evidence.
	return dedupLayerViolations(out)
}

// dedupLayerViolations keeps one finding per caller->callee-directory crossing,
// preferring the call-level variant that carries a symbol.
func dedupLayerViolations(vs []Violation) []Violation {
	best := map[string]int{}
	var out []Violation
	for _, v := range vs {
		key := filepath.ToSlash(v.CallerFile) + "\x00" + filepath.ToSlash(filepath.Dir(v.CalleeFile))
		if i, ok := best[key]; ok {
			if out[i].Symbol == "" && v.Symbol != "" {
				out[i] = v
			}
			continue
		}
		best[key] = len(out)
		out = append(out, v)
	}
	return out
}

// --- edge collection (mirrors guard.CheckBoundaries' edge model) ---

type edge struct {
	fromDir, toDir string
	callerFile     string
	calleeFile     string
	symbol         string
	line           int
}

func collectEdges(ix *index.Index, files []string) []edge {
	var edges []edge
	if ix == nil {
		return edges
	}
	meta := map[string]index.Symbol{}
	for _, s := range ix.Symbols {
		if _, ok := meta[s.FullName()]; !ok {
			meta[s.FullName()] = s
		}
	}
	// Precompute both lookups once: resolveCallee used to scan all 17k
	// symbols for every qualified callee that missed the exact fast path, and
	// the import loop rebuilt the directory map per import (the pre-commit regression).
	bySimple := map[string][]index.Symbol{}
	for _, s := range ix.Symbols {
		bySimple[s.Name] = append(bySimple[s.Name], s)
	}
	dirSet := indexDirs(ix)
	// Per-file import qualifier -> package directory. This is the
	// disambiguator the old resolver lacked: it picked the lexicographically
	// smallest same-named symbol and ignored the qualifier entirely, so two
	// packages sharing a basename could misattribute calls (the same-basename misattribution).
	qualDirs := map[string]map[string]string{}
	for file, imps := range ix.ImportsByFile {
		m := map[string]string{}
		for _, imp := range imps {
			dir := importDir(imp.Path, dirSet)
			if dir == "" {
				continue
			}
			qual := imp.Alias
			if qual == "" {
				if p := ix.Pkgs[dir]; p != nil {
					qual = p.Name
				}
			}
			if qual == "" {
				qual = filepath.Base(dir)
			}
			m[qual] = dir
		}
		if len(m) > 0 {
			qualDirs[file] = m
		}
	}
	seen := map[string]bool{}
	add := func(fromDir, toDir, callerFile, calleeFile, symbol string, line int) {
		fromDir = filepath.ToSlash(fromDir)
		toDir = filepath.ToSlash(toDir)
		if fromDir == "" || toDir == "" || fromDir == toDir {
			return
		}
		key := filepath.ToSlash(callerFile) + "\x00" + toDir + "\x00" + symbol
		if seen[key] {
			return
		}
		seen[key] = true
		edges = append(edges, edge{fromDir, toDir, callerFile, calleeFile, symbol, line})
	}

	for _, f := range files {
		if isTestFile(f) {
			continue
		}
		fromDir := filepath.ToSlash(filepath.Dir(f))
		for _, s := range ix.SymbolsByFile[f] {
			full := s.FullName()
			for _, ce := range ix.Calls[full] {
				rs, ok := resolveCallee(meta, bySimple, qualDirs[f], ce.Target)
				if !ok {
					continue
				}
				resolved := rs.FullName()
				if resolved == full {
					continue
				}
				toDir := filepath.ToSlash(filepath.Dir(rs.File))
				if toDir != "" {
					add(fromDir, toDir, f, rs.File, resolved, rs.Line)
				}
			}
		}
		if pkg := ix.Pkgs[fromDir]; pkg != nil {
			for _, imp := range pkg.Imports {
				for toDir := range dirSet {
					if toDir == "" || toDir == fromDir {
						continue
					}
					if importMatches(imp.Path, toDir) {
						add(fromDir, toDir, f, toDir+"/", "", 0)
					}
				}
			}
		}
	}
	return edges
}

// --- layer matching ---

type layerSpec struct {
	name    string
	paths   []string
	depends map[string]bool
	hasDeps bool
}

func buildLayerSpecs(layers []Layer) []layerSpec {
	var out []layerSpec
	for _, l := range layers {
		ls := layerSpec{name: l.Name, paths: l.Paths}
		if len(l.Depends) > 0 {
			ls.hasDeps = true
			ls.depends = map[string]bool{}
			for _, d := range l.Depends {
				ls.depends[d] = true
			}
		}
		out = append(out, ls)
	}
	return out
}

func layersContaining(specs []layerSpec, dir string) []layerSpec {
	var out []layerSpec
	for _, s := range specs {
		if specPathMatches(s.paths, dir) {
			out = append(out, s)
		}
	}
	return out
}

func containsLayer(layers []layerSpec, name string) bool {
	for _, l := range layers {
		if l.name == name {
			return true
		}
	}
	return false
}

// specPathMatches reports whether a dir matches any of the layer's path
// patterns. Patterns may be plain directory names, glob patterns, "web/**", or
// "…/suffix" forms.
func specPathMatches(patterns []string, dir string) bool {
	for _, p := range patterns {
		if globMatch(p, dir) {
			return true
		}
	}
	return false
}

// normalizeGlob reduces a directory/glob pattern to a form the intel guard's
// dirMatch can evaluate. dirMatch only does exact/prefix/suffix comparison and
// does not expand "/**", so a self-or-descendant glob is collapsed to its base:
// "web/**" becomes "web", whose prefix check then matches "web" and "web/…".
func normalizeGlob(pattern string) string {
	if pattern == "" {
		return pattern
	}
	if strings.HasSuffix(pattern, "/**") {
		return strings.TrimSuffix(pattern, "/**")
	}
	if strings.HasSuffix(pattern, "/...") {
		return strings.TrimSuffix(pattern, "/...")
	}
	return pattern
}

// globMatch matches a rule/layer pattern against a directory. It supports exact,
// "prefix/…", "…/suffix", and "prefix/**" (self-or-descendant) forms.
func globMatch(pattern, dir string) bool {
	pattern = filepath.ToSlash(pattern)
	dir = filepath.ToSlash(dir)
	if pattern == "" || dir == "" {
		return false
	}
	if pattern == dir {
		return true
	}
	if strings.HasSuffix(pattern, "/**") {
		base := strings.TrimSuffix(pattern, "/**")
		return dir == base || strings.HasPrefix(dir, base+"/")
	}
	if strings.HasSuffix(pattern, "/...") {
		base := strings.TrimSuffix(pattern, "/...")
		return dir == base || strings.HasPrefix(dir, base+"/")
	}
	if !strings.Contains(pattern, "*") {
		return dir == pattern || strings.HasPrefix(dir, pattern+"/") || strings.HasSuffix(dir, "/"+pattern)
	}
	ok, _ := filepath.Match(pattern, dir)
	return ok
}

// --- small deterministic mirror helpers (index/intel internals are private) ---

func isTestFile(f string) bool {
	return strings.HasSuffix(f, "_test.go")
}

func resolveCallee(meta map[string]index.Symbol, bySimple map[string][]index.Symbol, qualDirs map[string]string, name string) (index.Symbol, bool) {
	if s, ok := meta[name]; ok {
		return s, true
	}
	i := strings.LastIndexByte(name, '.')
	if i <= 0 {
		return index.Symbol{}, false
	}
	qual, simple := name[:i], name[i+1:]
	if qual == "" || simple == "" {
		return index.Symbol{}, false
	}
	cands := bySimple[simple]
	// Receiver-qualified method call (Type.method) wins over a package
	// qualifier.
	if s, ok := smallestNamed(cands, func(s index.Symbol) bool { return s.Receiver == qual }); ok {
		return s, true
	}
	// Package-qualified call: when the caller imports a package under this
	// qualifier, the callee must live in that package's directory. This is
	// the only disambiguator that survives two packages sharing a basename.
	if dir, ok := qualDirs[qual]; ok && dir != "" {
		return smallestNamed(cands, func(s index.Symbol) bool {
			return filepath.ToSlash(filepath.Dir(s.File)) == dir
		})
	}
	// No import information (older index, non-Go language): keep the
	// deterministic lexical fallback rather than fabricating a new one.
	return smallestNamed(cands, func(index.Symbol) bool { return true })
}

// smallestNamed returns the lexicographically smallest FullName among the
// candidates that satisfy want, matching the historical deterministic pick.
func smallestNamed(cands []index.Symbol, want func(index.Symbol) bool) (index.Symbol, bool) {
	var best index.Symbol
	found := false
	for _, s := range cands {
		if !want(s) {
			continue
		}
		if !found || s.FullName() < best.FullName() {
			best = s
			found = true
		}
	}
	return best, found
}

// importDir maps an import path onto the most specific package directory in
// dirSet. Exact/whole-suffix matches (score 2) beat the lenient basename
// fallback (score 1) — without that rank, two packages named "util" both
// "match" and the pick is arbitrary. Longest dir, then lexicographic, keeps
// the result deterministic.
func importDir(importPath string, dirSet map[string]bool) string {
	best, bestScore := "", 0
	for d := range dirSet {
		score := 0
		if importPath == d || strings.HasSuffix(importPath, "/"+d) {
			score = 2
		} else if importMatches(importPath, d) {
			score = 1
		}
		if score == 0 {
			continue
		}
		if score > bestScore || (score == bestScore && (len(d) > len(best) || (len(d) == len(best) && d < best))) {
			best, bestScore = d, score
		}
	}
	return best
}

func simpleName(name string) string {
	if i := strings.LastIndex(name, "."); i >= 0 {
		return name[i+1:]
	}
	return name
}

func indexDirs(ix *index.Index) map[string]bool {
	dirs := map[string]bool{}
	for _, s := range ix.Symbols {
		if d := filepath.ToSlash(filepath.Dir(s.File)); d != "." && d != "" {
			dirs[d] = true
		}
	}
	return dirs
}

func importMatches(importPath, dir string) bool {
	if importPath == "" {
		return false
	}
	dir = filepath.ToSlash(dir)
	if strings.HasSuffix(importPath, "/"+dir) || importPath == dir {
		return true
	}
	return strings.HasSuffix(importPath, "/"+filepath.Base(dir))
}

// severity normalizes a rule severity: empty becomes "error"; anything other
// than "warning" is treated as "error".
func severity(s string) string {
	if s == "warning" {
		return "warning"
	}
	return "error"
}

func layerKey(a, b string) string {
	return a + "\x00" + b
}

func layerLabel(name string) string {
	if name == "" {
		return "layer:*"
	}
	return "layer:" + name
}

func layerRuleID(r Rule) string {
	if r.ID != "" {
		return r.ID
	}
	return "layer:" + r.LayerFrom + "->" + r.LayerTo
}

func sortViolations(vs []Violation) {
	sort.SliceStable(vs, func(i, j int) bool {
		a, b := vs[i], vs[j]
		if a.CallerFile != b.CallerFile {
			return a.CallerFile < b.CallerFile
		}
		if a.Symbol != b.Symbol {
			return a.Symbol < b.Symbol
		}
		if a.CalleeFile != b.CalleeFile {
			return a.CalleeFile < b.CalleeFile
		}
		return a.RuleFrom < b.RuleFrom
	})
}
