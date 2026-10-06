//go:build !notreesitter

package index

import (
	"embed"
	"fmt"
	"sort"
	"strings"
	"testing"
)

//go:embed testfixture/*.py testfixture/*.ts testfixture/*.java
var parityFixtures embed.FS

// parity_test.go is the heuristic<->tree-sitter parity gate (CG-P0-4).
//
// kern ships two extractors for non-Go languages: the regex/heuristic path
// (extractForeignRegex, the -tags notreesitter build) and tree-sitter
// (tsExtract, the default build). The two builds differ in PRECISION, never
// in CONTRADICTION: the regex path is a documented lower-fidelity subset of
// the tree-sitter graph — tree-sitter may legitimately emit symbols and call
// edges the regex rules structurally cannot express (props, fields,
// AST-verified callees), while the regex path must never be MORE certain
// than tree-sitter about a shared fact and never silently drop a fact
// tree-sitter is confident in. A user in either build gets graphs that agree
// on every shared fact and differ only in documented, additive precision.
//
// This gate runs ONLY in the default (tree-sitter) build — the precise path
// must exist to compare — over the shared fixture corpus in testfixture/,
// and asserts the superset contract:
//
//  1. Regex symbols are a subset of tree-sitter symbols modulo documented
//     regex artifacts (regexOnlySymbols): every regex symbol must exist on
//     the ts side with matching (full, kind, line, confidence), unless it is
//     a documented declaration-line artifact.
//  2. Tree-sitter symbols may EXCEED the regex set: ts-only symbols are
//     tolerated only when they are documented per-symbol (tsOnlySymbols) or
//     belong to a class the regex extractor structurally cannot produce
//     (tsOnlySymbolClasses, e.g. field/property declarations) — additional
//     precision, never a contradiction of regex output.
//  3. Every tree-sitter call edge exists on the regex side, with regex
//     confidence <= tree-sitter confidence for shared edges: the regex path
//     may be less certain, never more certain, and never silently drop an
//     edge. Tree-sitter may carry edges regex cannot express only when they
//     are documented (tsOnlyEdges) or target a ts-only callee.
//  4. Regex-only edges are exactly the documented declaration-line
//     artifacts (regexExtraEdges): anything new fails the gate.
//  5. Inheritance maps agree exactly.
//
// The tolerance tables are the documented degradation list the plan
// requires: each entry names the construct and the confidence relationship.
// Body-end heuristics (Symbol.End) intentionally differ (regex estimates,
// tree-sitter uses node ranges) and are not part of the identity contract —
// both are extent hints only (see CG-P0-3).

// tsOnlySymbols documents symbols tree-sitter emits that the regex path
// legitimately never produces, named per occurrence (field/property
// declarations are not decl rules in the regex extractor). The per-symbol
// list is precise documentation; tsOnlySymbolClasses below generalizes it
// to whole kinds the regex rules structurally cannot emit.
var tsOnlySymbols = map[string]map[string]string{
	"java": {
		"String": "var", // private String name; — field declaration
	},
	"typescript": {
		"name": "prop", // class/interface property declarations (lines 10, 31)
	},
}

// tsOnlySymbolClasses documents KINDS of ts-only symbols that are
// structurally regex-impossible per language: the regex extractor has no
// declaration rule producing that kind for that language, so any ts symbol
// of that kind is additional precision, not a contradiction. A ts-only
// symbol passes the gate if it is listed per-symbol in tsOnlySymbols OR its
// kind is in this table.
var tsOnlySymbolClasses = map[string]map[string]bool{
	// Java field declarations ("private String name;") map to "var" in
	// tree-sitter; the regex java rules only declare class/interface/enum/
	// record/method, never vars.
	"java": {
		"var": true,
	},
	// JS/TS property declarations (property_signature, public_field_definition)
	// map to "prop"; the regex js rules have no property rule.
	"javascript": {
		"prop": true,
	},
	"typescript": {
		"prop": true,
	},
}

// regexOnlySymbols documents symbols the regex path emits that tree-sitter
// does not: declaration-line artifacts the regex rules read as standalone
// symbols while the AST sees the same text as part of a larger construct.
// The regex path must never invent declarations beyond this documented
// list. Currently empty — every regex symbol in the fixture corpus exists on
// the ts side — but the symbol assertion below REQUIRES any future
// regex-only symbol to be listed here (fullname -> kind, mirroring
// tsOnlySymbols) before it may exist.
var regexOnlySymbols = map[string]map[string]string{}

// regexExtraEdges documents regex-only call edges: the declared name on a
// declaration line matches callRe and, being class-qualified, escapes the
// self-call filter (full == owner). MEDIUM, deliberately not filtered out —
// same-line bodies legitimately carry calls (function foo() { return bar() }).
// These are the ONLY regex-only edges the superset contract tolerates; any
// other regex-only edge fails the gate.
var regexExtraEdges = map[string]map[string]map[string]bool{
	"java": {
		"Fixtures.helper": {"helper": true},
		"Fixtures.main":   {"main": true},
		"Greeter.Greeter": {"Greeter": true},
		"Greeter.greet":   {"greet": true},
		"Greeter.loud":    {"loud": true},
	},
	"python": {
		"Greeter.__init__": {"__init__": true},
		"Greeter.greet":    {"greet": true},
		"Greeter.loud":     {"loud": true},
		"Child.greet":      {"greet": true},
	},
	"typescript": {
		"Greeter.constructor": {"constructor": true},
		"Greeter.greet":       {"greet": true},
		"Greeter.loud":        {"loud": true},
		"LoudGreeter.shout":   {"shout": true},
	},
}

// tsOnlyEdges documents call edges tree-sitter emits that the regex path
// legitimately never produces, mirroring regexExtraEdges (owner -> target).
// Currently empty — every ts edge in the fixture corpus is matched on the
// regex side, exactly or in last-segment chain form — but the superset
// contract allows ts to carry edges regex cannot express, so any such edge
// must be listed here OR resolve to a ts-only callee (a symbol regex never
// produced, so it cannot emit an edge to it) before it may exist.
var tsOnlyEdges = map[string]map[string]map[string]bool{}

// chainFormLastSegment reports whether the regex edge (owner, reTarget) is
// the last-segment form of a tree-sitter dotted-chain target: ts records
// "self.greet.upper", regex (unresolvable chains) records "upper". The
// regex form is the documented degradation; confidence must still satisfy
// the <= invariant.
func chainFormLastSegment(owner, tsTarget, reTarget string) bool {
	if !strings.Contains(tsTarget, ".") {
		return false
	}
	seg := tsTarget
	if i := strings.LastIndexByte(seg, '.'); i >= 0 {
		seg = seg[i+1:]
	}
	return seg == reTarget && reTarget != owner
}

// tsOnlyCallee reports whether an edge target resolves to a symbol that
// exists only on the tree-sitter side for this file. The regex extractor
// cannot emit an edge to a callee its own symbol scan never produced, so
// such edges are structurally ts-only (additional precision, not a dropped
// regex edge). Dotted targets fall back to their last segment, the
// chain-form sibling of chainFormLastSegment.
func tsOnlyCallee(target string, tsOnly map[string]bool) bool {
	if tsOnly[target] {
		return true
	}
	if i := strings.LastIndexByte(target, '.'); i >= 0 {
		return tsOnly[target[i+1:]]
	}
	return false
}

func canonEdges(calls map[string][]CallEdge) map[string]map[string]Confidence {
	out := map[string]map[string]Confidence{}
	for owner, edges := range calls {
		m := map[string]Confidence{}
		for _, e := range edges {
			if _, dup := m[e.Target]; !dup {
				m[e.Target] = e.Confidence
			}
		}
		out[owner] = m
	}
	return out
}

func TestHeuristicTreesitterParity(t *testing.T) {
	entries, err := parityFixtures.ReadDir("testfixture")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		rel := "testfixture/" + e.Name()
		b, err := parityFixtures.ReadFile(rel)
		if err != nil {
			t.Fatal(err)
		}
		lang := detectLang(rel, b)
		tsSyms, tsCalls, tsInh, _, errTS := tsExtract(rel, b, lang)
		reSyms, reCalls, reInh, _, errRE := extractForeignRegex(rel, b, lang)
		if errTS != nil {
			t.Fatalf("%s: tree-sitter extraction failed: %v", rel, errTS)
		}
		if errRE != nil {
			t.Fatalf("%s: regex extraction failed: %v", rel, errRE)
		}
		checkSymbolParity(t, rel, lang, tsSyms, reSyms)
		checkEdgeParity(t, rel, lang, tsSyms, reSyms, tsCalls, reCalls)
		checkInheritParity(t, rel, tsInh, reInh)
	}
}

// checkSymbolParity asserts the superset symbol contract: every regex symbol
// must exist on the tree-sitter side (same full name, kind, line,
// confidence) unless documented as a regex artifact (regexOnlySymbols); the
// tree-sitter set may EXCEED the regex set only through documented
// per-symbol tolerances (tsOnlySymbols) or structurally regex-impossible
// kinds (tsOnlySymbolClasses). Shared symbols must never contradict — kind,
// line and confidence must match exactly.
func checkSymbolParity(t *testing.T, rel, lang string, tsSyms, reSyms []Symbol) {
	tsByFull := map[string]Symbol{}
	for _, s := range tsSyms {
		tsByFull[s.FullName()] = s
	}
	reByFull := map[string]Symbol{}
	for _, s := range reSyms {
		reByFull[s.FullName()] = s
	}
	// Tree-sitter may exceed the regex set, but never contradict it: every
	// shared symbol must agree exactly, and ts-only symbols must be either
	// documented per-symbol or a structurally regex-impossible kind.
	for full, ts := range tsByFull {
		re, ok := reByFull[full]
		if ok {
			if re.Kind != ts.Kind || re.Line != ts.Line || re.Confidence != ts.Confidence {
				t.Errorf("%s: symbol %s diverges: regex %s/%d/%s vs tree-sitter %s/%d/%s",
					rel, full, re.Kind, re.Line, re.Confidence, ts.Kind, ts.Line, ts.Confidence)
			}
			continue
		}
		if kind, doc := tsOnlySymbols[lang][full]; doc && kind == ts.Kind {
			continue
		}
		if tsOnlySymbolClasses[lang][ts.Kind] {
			continue
		}
		t.Errorf("%s: symbol %s (%s, line %d) missing from regex output; ts-only symbols must be listed in tsOnlySymbols or belong to a documented tsOnlySymbolClasses kind",
			rel, full, ts.Kind, ts.Line)
	}
	// The regex path must not invent declarations: every regex symbol must
	// exist on the tree-sitter side, or be a documented regex artifact.
	for full, re := range reByFull {
		if _, ok := tsByFull[full]; ok {
			continue
		}
		if kind, doc := regexOnlySymbols[lang][full]; doc && kind == re.Kind {
			continue
		}
		t.Errorf("%s: symbol %s (%s, line %d) exists only in regex output; regex-only symbols must be listed in regexOnlySymbols",
			rel, full, re.Kind, re.Line)
	}
}

// checkEdgeParity asserts the superset edge contract: every tree-sitter edge
// is present on the regex side with confidence <= ts confidence; regex-only
// edges are exactly the documented declaration-line artifacts; ts may carry
// edges regex cannot express only when documented (tsOnlyEdges) or when the
// callee is itself a ts-only symbol (the regex side never produced the
// target, so the edge is additive precision, not a dropped regex edge).
func checkEdgeParity(t *testing.T, rel, lang string, tsSyms, reSyms []Symbol, tsCalls, reCalls map[string][]CallEdge) {
	ts := canonEdges(tsCalls)
	re := canonEdges(reCalls)
	// tsOnly is the set of callees that exist only on the tree-sitter side
	// for this file: symbols the regex scan never produced. Edges to them
	// are structurally ts-only.
	reNames := map[string]bool{}
	for _, s := range reSyms {
		reNames[s.FullName()] = true
		reNames[s.Name] = true
	}
	tsOnly := map[string]bool{}
	for _, s := range tsSyms {
		if !reNames[s.FullName()] && !reNames[s.Name] {
			tsOnly[s.FullName()] = true
			tsOnly[s.Name] = true
		}
	}
	owners := map[string]bool{}
	for o := range ts {
		owners[o] = true
	}
	for o := range re {
		owners[o] = true
	}
	for owner := range owners {
		reEdges := re[owner]
		if reEdges == nil {
			reEdges = map[string]Confidence{}
		}
		// 1. Every tree-sitter edge must exist on the regex side — shared
		// edges at regex confidence <= ts confidence — or be a documented
		// ts-only edge (tsOnlyEdges) / ts-only-callee edge.
		for target, tsConf := range ts[owner] {
			if reConf, ok := reEdges[target]; ok {
				if confRank(reConf) > confRank(tsConf) {
					t.Errorf("%s: %s -> %s over-claims: regex %s > tree-sitter %s",
						rel, owner, target, reConf, tsConf)
				}
				continue
			}
			// Documented form tolerance: dotted-chain target.
			matchedForm := false
			for reTarget, reConf := range reEdges {
				if chainFormLastSegment(owner, target, reTarget) {
					if confRank(reConf) > confRank(tsConf) {
						t.Errorf("%s: %s -> %s (chain form %s) over-claims: regex %s > tree-sitter %s",
							rel, owner, reTarget, target, reConf, tsConf)
					}
					matchedForm = true
					break
				}
			}
			if matchedForm {
				continue
			}
			// Superset allowance: the ts edge is additional precision when
			// documented per-symbol or when its callee is a ts-only symbol.
			if tsOnlyEdges[lang][owner][target] {
				continue
			}
			if tsOnlyCallee(target, tsOnly) {
				continue
			}
			t.Errorf("%s: edge %s -> %s (%s) missing from regex output and not a documented ts-only edge",
				rel, owner, target, tsConf)
		}
		// 2. Regex-only edges must be exactly the documented artifacts
		// (declaration-line self-calls), or the last-segment form of a
		// tree-sitter dotted-chain target (chain form tolerance).
		for target, reConf := range reEdges {
			if _, ok := ts[owner][target]; ok {
				continue
			}
			if regexExtraEdges[lang][owner][target] {
				continue
			}
			chainForm := false
			for tsTarget := range ts[owner] {
				if chainFormLastSegment(owner, tsTarget, target) {
					chainForm = true
					break
				}
			}
			if chainForm {
				continue
			}
			t.Errorf("%s: undocumented regex-only edge %s -> %s (%s); add to regexExtraEdges only after review",
				rel, owner, target, reConf)
		}
	}
}

func checkInheritParity(t *testing.T, rel string, tsInh, reInh map[string][]string) {
	norm := func(m map[string][]string) map[string]string {
		out := map[string]string{}
		for k, v := range m {
			s := append([]string(nil), v...)
			sort.Strings(s)
			out[k] = strings.Join(s, ",")
		}
		return out
	}
	tsN, reN := norm(tsInh), norm(reInh)
	if fmt.Sprint(tsN) != fmt.Sprint(reN) {
		t.Errorf("%s: inheritance diverges: regex %v vs tree-sitter %v", rel, reN, tsN)
	}
}

// TestParityCorpusReachesBothPaths guards against a corpus that both
// extractors handle trivially (e.g. an empty file): the corpus must produce
// real symbols and edges on both sides, or the gate proves nothing.
func TestParityCorpusReachesBothPaths(t *testing.T) {
	entries, _ := parityFixtures.ReadDir("testfixture")
	totalSyms, totalEdges := 0, 0
	for _, e := range entries {
		rel := "testfixture/" + e.Name()
		b, _ := parityFixtures.ReadFile(rel)
		lang := detectLang(rel, b)
		tsSyms, tsCalls, _, _, err := tsExtract(rel, b, lang)
		if err != nil {
			t.Fatal(err)
		}
		reSyms, _, _, _, err := extractForeignRegex(rel, b, lang)
		if err != nil {
			t.Fatal(err)
		}
		if len(tsSyms) < 5 || len(reSyms) < 5 {
			t.Errorf("%s: corpus too thin for a parity gate (ts %d syms, regex %d syms)", rel, len(tsSyms), len(reSyms))
		}
		totalSyms += len(tsSyms)
		for _, edges := range tsCalls {
			totalEdges += len(edges)
		}
	}
	if totalSyms < 20 || totalEdges < 8 {
		t.Errorf("corpus too thin overall: %d symbols, %d ts edges", totalSyms, totalEdges)
	}
}
