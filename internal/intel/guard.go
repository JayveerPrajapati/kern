package intel

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// BoundaryRule declares one allowed or forbidden dependency edge between two
// package/directory patterns. Action is "forbid" (a violation) or "allow" (an
// explicit exemption that overrides forbids for the same pair).
type BoundaryRule struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Action string `json:"action"`
}

// Boundaries is the declarative guardrail file loaded from .kern/boundaries.json.
type Boundaries struct {
	Description string         `json:"description,omitempty"`
	Rules       []BoundaryRule `json:"rules"`
}

// DefaultBoundariesPath returns where the guardrail rules live for a root.
func DefaultBoundariesPath(root string) string {
	return filepath.Join(root, ".kern", "boundaries.json")
}

// LoadBoundaries reads the guardrail rules for root. A missing file is not an
// error — it yields a nil ruleset (nothing to enforce), preserving the
// zero-config experience, but logs a warning so the absence of guardrails is
// visible. A present-but-malformed file IS an error (fail-closed): a broken
// boundaries.json must never silently permit everything.
func LoadBoundaries(root string) (*Boundaries, error) {
	data, err := os.ReadFile(DefaultBoundariesPath(root))
	if err != nil {
		if os.IsNotExist(err) {
			log.Printf("WARNING: no boundary rules configured — %s not found; nothing to enforce", DefaultBoundariesPath(root))
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
// Fail-open on missing mandatory data is surfaced, never silent: a nil
// boundaries ruleset (no .kern/boundaries.json) with a non-empty check scope
// records a "boundaries-not-configured" skip (a warning), not a violation.
func CheckBoundariesPrecise(ix *index.Index, b *Boundaries, files []string, strict bool) ([]Violation, map[string]int) {
	var violations []Violation
	skipped := map[string]int{}
	if len(files) == 0 {
		// Clean skip: nothing is in scope to check, so there is nothing to
		// warn about either.
		return violations, skipped
	}
	if b == nil {
		// No .kern/boundaries.json — the guard is not configured. Returning
		// empty violations here would be a silent PASS, so mirror the
		// imports-by-file-missing precedent: surface the gap as a skip (a
		// warning), never a violation.
		skipped["boundaries-not-configured"] = len(files)
		return violations, skipped
	}
	if len(b.Rules) == 0 {
		// An explicitly empty rule list ("rules": []) in a present file is
		// deliberate user intent: nothing to enforce. Clean skip, no warning.
		return violations, skipped
	}

	meta := map[string]index.Symbol{}
	dirs := map[string]string{} // symbol FullName -> dir
	for _, s := range ix.Symbols {
		if _, ok := meta[s.FullName()]; !ok {
			meta[s.FullName()] = s
		}
		if _, ok := dirs[s.FullName()]; !ok {
			dirs[s.FullName()] = filepath.Dir(s.File)
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
		if isTestFile(f) {
			continue
		}
		fromDir := filepath.Dir(f)
		syms := ix.SymbolsByFile[f]
		for _, s := range syms {
			full := s.FullName()
			for _, c := range ix.Calls[full] {
				if strict {
					// Strict precision: an edge whose caller language is not fully
					// resolved ("resolved" tier) is unknown, not guessable, so it
					// is skipped instead of trusted.
					if p := ix.PrecisionByLang[s.Lang]; p != "resolved" {
						skipped[s.Lang]++
						continue
					}
				}
				resolved := resolveCallee(ix, meta, c)
				if resolved == "" || resolved == full {
					continue
				}
				if toDir := dirs[resolved]; toDir != "" {
					check(fromDir, toDir, f, symbolFile(meta, resolved), resolved, symbolLine(meta, resolved))
				}
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
				if importMatches(imp, toDir) {
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
func verdict(rules []BoundaryRule, fromDir, toDir string) *BoundaryRule {
	for i := range rules {
		if rules[i].Action == "allow" && dirMatch(rules[i].From, fromDir) && dirMatch(rules[i].To, toDir) {
			return nil
		}
	}
	for i := range rules {
		if rules[i].Action == "forbid" && dirMatch(rules[i].From, fromDir) && dirMatch(rules[i].To, toDir) {
			return &rules[i]
		}
	}
	return nil
}

// dirMatch matches a rule pattern against a directory: exact, "pattern/…"
// prefix, or "…/pattern" suffix.
func dirMatch(pattern, dir string) bool {
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
	if strings.HasSuffix(importPath, "/"+dir) || importPath == dir {
		return true
	}
	if strings.HasSuffix(importPath, "/"+filepath.Base(dir)) {
		return true
	}
	if strings.Contains(importPath, ".") {
		slash := strings.ReplaceAll(importPath, ".", "/")
		return slash == dir || strings.HasSuffix(slash, "/"+dir) || strings.HasSuffix(slash, "/"+filepath.Base(dir))
	}
	return false
}

// resolveCallee maps a raw callee name to a canonical in-project symbol
// FullName. When several symbols share a simple name the lexicographically
// smallest FullName wins, so verdicts are deterministic across runs.
func resolveCallee(ix *index.Index, meta map[string]index.Symbol, name string) string {
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

func symbolFile(meta map[string]index.Symbol, name string) string {
	if s, ok := meta[name]; ok {
		return s.File
	}
	return ""
}

func symbolLine(meta map[string]index.Symbol, name string) int {
	if s, ok := meta[name]; ok {
		return s.Line
	}
	return 0
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
