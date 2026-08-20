// Package architecture implements Architecture Governance: a declarative
// architecture.yaml rule file with named layers and per-rule metadata, evaluated
// deterministically against the standard index. Pattern rules delegate to
// intel.CheckBoundaries and layer-based checks run on top. The default build is
// stdlib-only, so YAML is parsed by a small hand-rolled parser for the fixed
// schema (plus .json); a malformed rule file fails closed.
package architecture

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
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
// delegated to intel.CheckBoundaries, then layer-based checks run on top.
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
	var br []intel.BoundaryRule
	for _, r := range e.config.Rules {
		if r.From == "" || r.To == "" {
			continue
		}
		br = append(br, intel.BoundaryRule{From: normalizeGlob(r.From), To: normalizeGlob(r.To), Action: r.Action})
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
	for _, v := range intel.CheckBoundaries(ix, &intel.Boundaries{Rules: br}, files) {
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
					Violation: intel.Violation{
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
						Violation: intel.Violation{
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
	// crossing (matching intel.CheckBoundaries' dedup), preferring the call-level
	// finding which carries symbol evidence.
	return dedupLayerViolations(out)
}

// dedupLayerViolations keeps one finding per caller->callee-directory crossing,
// preferring the call-level variant that carries a symbol.
func dedupLayerViolations(vs []Violation) []Violation {
	best := map[string]int{}
	var out []Violation
	for _, v := range vs {
		key := v.CallerFile + "\x00" + filepath.Dir(v.CalleeFile)
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

// --- edge collection (mirrors intel.CheckBoundaries' edge model) ---

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
	dirs := map[string]string{}
	for _, s := range ix.Symbols {
		if _, ok := meta[s.FullName()]; !ok {
			meta[s.FullName()] = s
		}
		if _, ok := dirs[s.FullName()]; !ok {
			dirs[s.FullName()] = filepath.Dir(s.File)
		}
	}
	seen := map[string]bool{}
	add := func(fromDir, toDir, callerFile, calleeFile, symbol string, line int) {
		if fromDir == "" || toDir == "" || fromDir == toDir {
			return
		}
		key := callerFile + "\x00" + toDir + "\x00" + symbol
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
		fromDir := filepath.Dir(f)
		for _, s := range ix.SymbolsByFile[f] {
			full := s.FullName()
			for _, c := range ix.Calls[full] {
				resolved := resolveCallee(meta, c)
				if resolved == "" || resolved == full {
					continue
				}
				if toDir := dirs[resolved]; toDir != "" {
					cf := ""
					if sf, ok := meta[resolved]; ok {
						cf = sf.File
					}
					ln := 0
					if sf, ok := meta[resolved]; ok {
						ln = sf.Line
					}
					add(fromDir, toDir, f, cf, resolved, ln)
				}
			}
		}
		if pkg := ix.Pkgs[fromDir]; pkg != nil {
			for _, imp := range pkg.Imports {
				for toDir := range indexDirs(ix) {
					if toDir == "" || toDir == fromDir {
						continue
					}
					if importMatches(imp, toDir) {
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

func resolveCallee(meta map[string]index.Symbol, name string) string {
	if _, ok := meta[name]; ok {
		return name
	}
	simple := simpleName(name)
	var best string
	for full, s := range meta {
		if s.Name == simple && (best == "" || full < best) {
			best = full
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
		if d := filepath.Dir(s.File); d != "." && d != "" {
			dirs[d] = true
		}
	}
	return dirs
}

func importMatches(importPath, dir string) bool {
	if importPath == "" {
		return false
	}
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
