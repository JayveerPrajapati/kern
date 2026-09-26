package intel

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/index"
)

// Boundaries is the declarative guardrail file loaded from .kern/boundaries.json.
type Boundaries struct {
	Description string                `json:"description,omitempty"`
	Rules       []domain.BoundaryRule `json:"rules"`
	// Pure opts into @pure mutability assertions: when true, `kern guard
	// check` (and the MCP guard tool) also validate that every Go
	// function/method whose doc comment contains "@pure" does not mutate
	// anything outside its own stack frame. Off by default; the flag changes
	// nothing when absent.
	Pure bool `json:"pure,omitempty"`
}

// DefaultBoundariesPath returns where the guardrail rules live for a root.
func DefaultBoundariesPath(root string) string {
	return filepath.Join(root, ".kern", "boundaries.json")
}

// LoadBoundaries reads the guardrail rules for root. A missing file is not an
// error — it yields a nil ruleset (nothing to enforce), preserving the
// zero-config experience. A present-but-malformed file IS an error (fail-closed):
// a broken boundaries.json must never silently permit everything.
func LoadBoundaries(root string) (*Boundaries, error) {
	data, err := os.ReadFile(DefaultBoundariesPath(root))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var b Boundaries
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, fmt.Errorf("invalid %s: %w", DefaultBoundariesPath(root), err)
	}
	return &b, nil
}

// InferBoundaries analyzes the index (packages, communities, file structure)
// and returns default layered architecture guardrails (Controller/Router -> Service -> Repository/DB)
// when no explicit .kern/boundaries.json is defined.
func InferBoundaries(ix *index.Index) *Boundaries {
	if ix == nil {
		return &Boundaries{
			Description: "Inferred baseline layered architecture guardrails",
			Rules: []domain.BoundaryRule{
				{From: "repository", To: "controller", Action: "forbid"},
				{From: "service", To: "controller", Action: "forbid"},
				{From: "db", To: "web", Action: "forbid"},
				{From: "web", To: "db", Action: "forbid"},
			},
		}
	}

	// Detect directories corresponding to common layers
	layer1 := make(map[string]bool) // Presentation / Controller / Web / Route / Handler / API
	layer2 := make(map[string]bool) // Service / Business / Logic / UseCase / Domain
	layer3 := make(map[string]bool) // Repository / DAO / Store / DB / Database / Model

	// Maven-style "*-api" modules are contract/interface modules that the
	// *-service modules IMPLEMENT — the correct dependency direction is
	// service -> api. Treating "api" as a presentation layer (L1) for Java
	// projects fabricates mass false positives on every Maven monorepo
	// (dogfood finding on slice-adaptors: 197 spurious service->api
	// violations). For Go/Python/JS, an "api" dir is usually the HTTP
	// surface, so it stays L1 there.
	javaProject := false
	for f := range ix.FileHashes {
		if strings.HasSuffix(f, ".java") {
			javaProject = true
			break
		}
	}
	if !javaProject {
		for _, p := range ix.Pkgs {
			if p.Lang == "java" {
				javaProject = true
				break
			}
		}
	}
	// A dir's layer is decided by its OWN name (last path segment), never by
	// ancestor segments: a package "com/x/service/dao" is a DAO layer, not a
	// service layer, even though an ancestor module is named *-service.
	// Full-path keyword matching made every subpackage of a *-service module
	// also an L2 dir, which fabricated intra-module dao->service violations
	// (dogfood finding: 19 spurious findings on slice-adaptors).
	baseName := func(name string) string {
		name = filepath.ToSlash(name)
		if i := strings.LastIndexByte(name, '/'); i >= 0 {
			return name[i+1:]
		}
		return name
	}
	// mavenContractDir reports whether a Java relative dir lives under a
	// Maven "*-api" module segment. Those modules are the CONTRACT surface
	// (interfaces, DTOs, JAX-RS interfaces) that *-service modules implement
	// — the correct dependency direction is service -> api. Any layer keyword
	// (api, rest, web, ...) inside them is contract space, never presentation.
	mavenContractDir := func(dir string) bool {
		dir = filepath.ToSlash(dir)
		for _, seg := range strings.Split(dir, "/") {
			if strings.HasSuffix(seg, "-api") {
				return true
			}
		}
		return false
	}
	isL1 := func(name string) bool {
		lower := strings.ToLower(name)
		if javaProject && mavenContractDir(lower) {
			return false
		}
		base := baseName(lower)
		return strings.Contains(base, "controller") || strings.Contains(base, "handler") ||
			strings.Contains(base, "route") || strings.Contains(base, "web") ||
			strings.Contains(base, "rest") || strings.Contains(base, "transport") ||
			strings.Contains(base, "delivery") || strings.Contains(base, "endpoint") ||
			(!javaProject && strings.Contains(base, "api"))
	}
	isL2 := func(name string) bool {
		base := baseName(strings.ToLower(name))
		return strings.Contains(base, "service") || strings.Contains(base, "usecase") ||
			strings.Contains(base, "domain") || strings.Contains(base, "logic") ||
			strings.Contains(base, "biz")
	}
	isL3 := func(name string) bool {
		base := baseName(strings.ToLower(name))
		return strings.Contains(base, "repo") || strings.Contains(base, "dao") ||
			strings.Contains(base, "store") || strings.Contains(base, "database") ||
			base == "db" || strings.HasSuffix(base, "/db") || strings.Contains(base, "model") ||
			strings.Contains(base, "entity")
	}

	for p := range ix.Pkgs {
		pClean := filepath.ToSlash(p)
		if isL1(pClean) {
			layer1[pClean] = true
		}
		if isL2(pClean) {
			layer2[pClean] = true
		}
		if isL3(pClean) {
			layer3[pClean] = true
		}
	}

	for f := range ix.FileHashes {
		d := filepath.ToSlash(filepath.Dir(f))
		if d == "." || d == "" {
			continue
		}
		if isL1(d) {
			layer1[d] = true
		}
		if isL2(d) {
			layer2[d] = true
		}
		if isL3(d) {
			layer3[d] = true
		}
	}

	var rules []domain.BoundaryRule
	ruleSet := make(map[string]bool)
	addRule := func(from, to, action string) {
		key := from + "->" + to + ":" + action
		if !ruleSet[key] {
			ruleSet[key] = true
			rules = append(rules, domain.BoundaryRule{From: from, To: to, Action: action})
		}
	}

	// Layer 3 (Repository/DB) cannot depend on Layer 2 (Service) or Layer 1 (Controller)
	nested := func(a, b string) bool {
		// One layer dir contains the other: a dao subpackage of a *-service
		// module is part of that service, not a separate layer it must not
		// reach. Rules between ancestor-descendant dirs fabricate
		// intra-module violations (dao/impl -> dao flagged as dao -> service);
		// sibling-module pairs keep the real cross-module enforcement.
		return strings.HasPrefix(a+"/", b+"/") || strings.HasPrefix(b+"/", a+"/")
	}
	for l3 := range layer3 {
		for l1 := range layer1 {
			if !nested(l3, l1) {
				addRule(l3, l1, "forbid")
			}
		}
		for l2 := range layer2 {
			if !nested(l3, l2) {
				addRule(l3, l2, "forbid")
			}
		}
	}

	// Layer 2 (Service) cannot depend on Layer 1 (Controller)
	for l2 := range layer2 {
		for l1 := range layer1 {
			if !nested(l2, l1) {
				addRule(l2, l1, "forbid")
			}
		}
	}

	// Always ensure standard baseline keyword rules are present as fallbacks
	addRule("repository", "controller", "forbid")
	addRule("repository", "service", "forbid")
	addRule("service", "controller", "forbid")
	addRule("service", "web", "forbid")
	addRule("db", "web", "forbid")
	addRule("db", "controller", "forbid")
	addRule("db", "service", "forbid")

	return &Boundaries{
		Description: "Inferred layered architecture guardrails (Controller -> Service -> Repository/DB)",
		Rules:       rules,
	}
}

// InitBoundaries scaffolds a starter guardrail file.
func InitBoundaries(root string) error {
	tmpl := `{
  "description": "Architectural guardrails: forbid or allow dependency edges by directory pattern.",
  "rules": [
    {"from": "web", "to": "db", "action": "forbid"}
  ]
}
`
	path := DefaultBoundariesPath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(tmpl), 0o644)
}

// Violation is one rejected boundary crossing, with the rule that rejected it.
type Violation struct {
	CallerFile string `json:"caller_file"`
	CalleeFile string `json:"callee_file"`
	Symbol     string `json:"symbol,omitempty"`
	Line       int    `json:"line,omitempty"`
	RuleFrom   string `json:"rule_from"`
	RuleTo     string `json:"rule_to"`
}

// CheckBoundaries validates a set of files against the rules and returns every
// crossing that a forbid rule rejects (allow rules for the same pair win).
// Edges come from resolved in-project call edges; where a file has no local
// symbol yet (e.g. an import was just added), package imports are checked too.
// Default precision: all edges are trusted.
func CheckBoundaries(ix *index.Index, b *Boundaries, files []string) []Violation {
	v, _ := CheckBoundariesPrecise(ix, b, files, false)
	return v
}

// CheckBoundariesPrecise is CheckBoundaries with a precision mode. When strict
// is true, call edges whose caller language is not "resolved"-precision in the
// index (ix.PrecisionByLang) are skipped rather than guessed at, so a
// heuristic edge can never fabricate a boundary violation. The returned map
// records, per caller language, how many call edges were skipped.
//
// When b is nil (no .kern/boundaries.json present), default layered architecture
// guardrails are dynamically inferred from detected communities and symbol packages.
func CheckBoundariesPrecise(ix *index.Index, b *Boundaries, files []string, strict bool) ([]Violation, map[string]int) {
	var violations []Violation
	skipped := map[string]int{}
	if len(files) == 0 {
		// Clean skip: nothing is in scope to check, so there is nothing to
		// warn about either.
		return violations, skipped
	}
	if b == nil {
		// No .kern/boundaries.json and no inferred rules passed — surface the gap as a skip.
		skipped["boundaries-not-configured"] = len(files)
		return violations, skipped
	}
	if len(b.Rules) == 0 {
		// An explicitly empty rule list ("rules": []) in a present file is
		// deliberate user intent: nothing to enforce. Clean skip, no warning.
		return violations, skipped
	}

	meta := map[string]index.Symbol{}
	for _, s := range ix.Symbols {
		if _, ok := meta[s.FullName()]; !ok {
			meta[s.FullName()] = s
		}
	}

	// Bare-name collision set: the index keys call edges by the caller's
	// FullName, which for package-level functions is the bare name (every
	// package has a "New"). Edges under such a key merge calls from EVERY
	// same-named function across packages, so a per-file traversal would
	// attribute another package's calls to this file's symbol (fabricated
	// violations). Only traverse call edges for names this file uniquely
	// owns; the per-file import check below remains the enforcement path for
	// ambiguous names (it is file-attributed and collision-free).
	colliding := map[string]bool{}
	byNameFile := map[string]string{} // FullName -> first file that owns it
	for _, s := range ix.Symbols {
		full := s.FullName()
		if prev, ok := byNameFile[full]; ok {
			if prev != s.File {
				colliding[full] = true
			}
		} else {
			byNameFile[full] = s.File
		}
	}

	check := func(fromDir, toDir, callerFile, calleeFile, symbol string, line int) {
		if fromDir == "" || toDir == "" || fromDir == toDir {
			return
		}
		if rule := verdict(b.Rules, fromDir, toDir); rule != nil {
			violations = append(violations, Violation{
				CallerFile: callerFile,
				CalleeFile: calleeFile,
				Symbol:     symbol,
				Line:       line,
				RuleFrom:   rule.From,
				RuleTo:     rule.To,
			})
		}
	}

	for _, f := range files {
		if isTestFile(f) || isFixtureFile(f) {
			continue
		}
		fromDir := filepath.Dir(f)
		syms := ix.SymbolsByFile[f]
		for _, s := range syms {
			full := s.FullName()
			if colliding[full] {
				// Ambiguous bare-name key: edges under `full` may originate
				// from another package's same-named function. Do not
				// fabricate crossings from foreign edges; the import-level
				// check below still enforces this file's real dependencies.
				continue
			}
			for _, ce := range ix.Calls[full] {
				c := ce.Target
				if strict {
					// Strict precision: an edge whose caller language is not fully
					// resolved ("resolved" tier) is unknown, not guessable, so it
					// is skipped instead of trusted.
					if p := ix.PrecisionByLang[s.Lang]; p != "resolved" {
						skipped[s.Lang]++
						continue
					}
				}
				resolved, ok := resolveCallee(ix, meta, colliding, c, s.File)
				if !ok || resolved.FullName() == full {
					continue
				}
				check(fromDir, filepath.Dir(resolved.File), f, resolved.File, resolved.FullName(), resolved.Line)
			}
		}
		// Import-level check: catch edges where a file imports a forbidden
		// package even before it calls into it. Uses the changed file's own
		// imports (ix.ImportsByFile) — never the package-aggregated
		// Pkgs[dir].Imports, which would wrongly attribute a sibling file's
		// import to every changed file in the directory (that was the
		// false-positive bug). Indexes built by older kern lack
		// imports_by_file; when the directory still carries package-level
		// imports, that missing per-file data is surfaced below as a
		// skipped-precision warning rather than a silent pass — `kern index`
		// rebuilds restore full coverage.
		fImports := ix.ImportsByFile[f]
		if len(fImports) == 0 {
			// No per-file import data for this file. If the index still knows
			// the package imports something (Pkgs[dir].Imports non-empty), the
			// import-level check is about to pass without ever inspecting the
			// file's imports — fail-open on missing mandatory data. Surface the
			// gap as a skip (a warning), never a violation: we cannot know
			// whether the file itself imports a forbidden package, so no
			// verdict is fabricated, but the incomplete check must not be
			// silent either. Rebuild the index (`kern index`) to restore
			// per-file coverage.
			if pkg := ix.Pkgs[fromDir]; pkg != nil && len(pkg.Imports) > 0 {
				skipped["imports-by-file-missing:"+f]++
			}
		}
		for _, imp := range fImports {
			for toDir := range indexDirs(ix) {
				if toDir == "" || toDir == fromDir {
					continue
				}
				if importMatches(imp.Path, toDir) {
					if rule := verdict(b.Rules, fromDir, toDir); rule != nil {
						violations = append(violations, Violation{
							CallerFile: f,
							CalleeFile: toDir + "/",
							RuleFrom:   rule.From,
							RuleTo:     rule.To,
						})
					}
				}
			}
		}
	}

	// Collapse duplicates: an import-level finding and the call-level findings
	// for the same caller->callee file pair are the same crossing. Keep the
	// call-level one when present — it carries the symbol evidence.
	deduped := map[string]Violation{}
	for _, v := range violations {
		// Collapse import-level ("lib/") and call-level ("lib/lib.go")
		// findings for the same crossing by comparing directories.
		key := v.CallerFile + "\x00" + filepath.Dir(v.CalleeFile)
		if prev, ok := deduped[key]; !ok || (prev.Symbol == "" && v.Symbol != "") {
			deduped[key] = v
		}
	}
	violations = violations[:0]
	for _, v := range deduped {
		violations = append(violations, v)
	}
	sort.Slice(violations, func(i, j int) bool {
		if violations[i].CallerFile != violations[j].CallerFile {
			return violations[i].CallerFile < violations[j].CallerFile
		}
		if violations[i].Symbol != violations[j].Symbol {
			return violations[i].Symbol < violations[j].Symbol
		}
		return violations[i].CalleeFile < violations[j].CalleeFile
	})
	return violations, skipped
}

// verdict decides a from→to edge and is order-invariant. If ANY rule allows
// the pair the verdict is nil (permitted) — allow dominates no matter where
// in the slice it appears. Only when no allow rule matches is the first
// forbid rule that matches returned (a violation). If nothing matches, the
// pair is permitted (default-permit for unconfigured pairs).
func verdict(rules []domain.BoundaryRule, fromDir, toDir string) *domain.BoundaryRule {
	for i := range rules {
		if rules[i].Action == "allow" && DirMatch(rules[i].From, fromDir) && DirMatch(rules[i].To, toDir) {
			return nil
		}
	}
	for i := range rules {
		if rules[i].Action == "forbid" && DirMatch(rules[i].From, fromDir) && DirMatch(rules[i].To, toDir) {
			return &rules[i]
		}
	}
	return nil
}

// DirMatch is the canonical directory-pattern matcher (exact, "pattern/…"
// prefix, or "…/pattern" suffix match). It matches a rule pattern against a
// directory and is shared by the boundary guard layer and consumers in other
// packages (e.g. internal/context) that need the same semantics.
func DirMatch(pattern, dir string) bool {
	if pattern == "" {
		return false
	}
	return dir == pattern ||
		strings.HasPrefix(dir, pattern+"/") ||
		strings.HasSuffix(dir, "/"+pattern)
}

// importMatches reports whether an import path refers to a local directory.
// Go import paths are slash-separated; Java (and other JVM languages) use
// dotted package paths, so a slash-converted variant is tested as well.
func importMatches(importPath, dir string) bool {
	if importPath == "" || dir == "" {
		return false
	}
	// Go-style (slash) imports: the module-relative package dir is a suffix of
	// the full import path ("github.com/x/y/internal/z" <-> "internal/z").
	if strings.HasSuffix(importPath, "/"+dir) || importPath == dir {
		return true
	}
	// Java-style (dotted) imports: the package path is a suffix of the source
	// directory ("com.inn.rcp.foo" <-> ".../java/com/inn/rcp/foo"). The full
	// package path is required; basename-only matches cross shared suffixes.
	if strings.Contains(importPath, ".") {
		slash := strings.ReplaceAll(importPath, ".", "/")
		return slash == dir || strings.HasSuffix(dir, "/"+slash)
	}
	return false
}

// resolveCallee maps a raw callee name to a canonical in-project symbol,
// preferring definitions in the caller's own file. A bare name that is
// ambiguous (defined in several files, none of them the caller's) resolves
// to nothing: attributing it to one arbitrary file would fabricate a
// cross-package edge — the mirror image of the colliding-caller guard
// above (dogfood finding: TradingApp nse_service.py -> api/stocks.py
// "get_quote" was such a fabrication).
func resolveCallee(ix *index.Index, meta map[string]index.Symbol, colliding map[string]bool, name, callerFile string) (index.Symbol, bool) {
	if sym, ok := meta[name]; ok {
		// Exact FullName hit. meta keeps the first-seen symbol, so when the
		// name is defined in several files the winner may be a foreign one:
		// a definition in the caller's own file is the real callee.
		if sym.File == callerFile {
			return sym, true
		}
		for _, ls := range ix.SymbolsByFile[callerFile] {
			if ls.FullName() == name {
				return ls, true
			}
		}
		if !colliding[name] {
			return sym, true
		}
		return index.Symbol{}, false
	}
	i := strings.LastIndexByte(name, '.')
	if i <= 0 {
		// Bare callees must be exact indexed symbols; name-only resolution
		// across the whole index fabricates cross-package edges.
		return index.Symbol{}, false
	}
	qual, simple := name[:i], name[i+1:]
	if qual == "" || simple == "" {
		return index.Symbol{}, false
	}
	// Resolve "qualifier.simple" only when the qualifier scopes the symbol:
	// a matching receiver (Type.method) or package/directory basename
	// (pkg.Func); the exact qualified name is handled above. Same-file
	// definitions win over the meta scan.
	for _, ls := range ix.SymbolsByFile[callerFile] {
		if ls.Name == simple && (ls.Receiver == qual || filepath.Base(filepath.Dir(ls.File)) == qual) {
			return ls, true
		}
	}
	var best *index.Symbol
	for full, s := range meta {
		if s.Name != simple {
			continue
		}
		if s.Receiver == qual || filepath.Base(filepath.Dir(s.File)) == qual {
			if best == nil || full < best.FullName() {
				cp := s
				best = &cp
			}
		}
	}
	if best == nil || colliding[best.FullName()] {
		return index.Symbol{}, false
	}
	return *best, true
}

// indexDirs returns the distinct package directories present in the index.
func indexDirs(ix *index.Index) map[string]bool {
	dirs := map[string]bool{}
	for _, s := range ix.Symbols {
		if d := filepath.Dir(s.File); d != "." && d != "" {
			dirs[d] = true
		}
	}
	return dirs
}

// RenderViolations renders the guard verdict: PASS when clean, otherwise a
// REJECT block per forbidden crossing.
func RenderViolations(violations []Violation) string {
	if len(violations) == 0 {
		return "PASS: no boundary violations"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "REJECT: %d boundary violations\n", len(violations))
	for _, v := range violations {
		fmt.Fprintf(&b, "  %s -> %s", v.CallerFile, v.CalleeFile)
		if v.Symbol != "" {
			fmt.Fprintf(&b, "  (%s", v.Symbol)
			if v.Line > 0 {
				fmt.Fprintf(&b, ":%d", v.Line)
			}
			b.WriteString(")")
		}
		fmt.Fprintf(&b, "  [rule %s -> %s forbid]\n", v.RuleFrom, v.RuleTo)
	}
	return strings.TrimSuffix(b.String(), "\n")
}
