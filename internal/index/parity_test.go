//go:build treesitter

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
// (extractForeignRegex, the stdlib-only default) and tree-sitter (tsExtract,
// -tags treesitter). A user with or without the tag must not get silently
// different graphs. This gate runs ONLY in the tree-sitter build (the
// precise path must exist to compare), over the shared fixture corpus in
// testfixture/, and asserts:
//
//  1. Symbol sets agree (full name, kind, line, confidence), modulo the
//     documented symbolTolerances below.
//  2. Every tree-sitter call edge exists on the regex side, with regex
//     confidence <= tree-sitter confidence: the regex path may be less
//     certain, never more certain, and never silently drop an edge.
//  3. Regex-only edges are exactly the documented declaration-line
//     artifacts (regexExtraEdges): anything new fails the gate.
//  4. Inheritance maps agree exactly.
//
// The tolerance tables are the documented degradation list the plan
// requires: each entry names the construct and the confidence relationship.
// Body-end heuristics (Symbol.End) intentionally differ (regex estimates,
// tree-sitter uses node ranges) and are not part of the identity contract —
// both are extent hints only (see CG-P0-3).

// tsOnlySymbols documents symbols tree-sitter emits that the regex path
// legitimately never produces (field/property declarations are not decl
// rules in the regex extractor).
var tsOnlySymbols = map[string]map[string]string{
	"java": {
		"String": "var", // private String name; — field declaration
	},
	"typescript": {
		"name": "prop", // class/interface property declarations (lines 10, 31)
	},
}

// regexExtraEdges documents regex-only call edges: the declared name on a
// declaration line matches callRe and, being class-qualified, escapes the
// self-call filter (full == owner). MEDIUM, deliberately not filtered out —
// same-line bodies legitimately carry calls (function foo() { return bar() }).
var regexExtraEdges = map[string]map[string]map[string]bool{
	"java": {
		"Fixtures.helper":    {"helper": true},
		"Fixtures.main":      {"main": true},
		"Greeter.Greeter":    {"Greeter": true},
		"Greeter.greet":      {"greet": true},
		"Greeter.loud":       {"loud": true},
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
	},
}

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
		checkEdgeParity(t, rel, lang, tsCalls, reCalls)
		checkInheritParity(t, rel, tsInh, reInh)
	}
}

// checkSymbolParity asserts identical (full, kind, line, confidence) symbol
// sets, modulo the documented tsOnlySymbols table and End-line heuristics.
func checkSymbolParity(t *testing.T, rel, lang string, tsSyms, reSyms []Symbol) {
	tsByFull := map[string]Symbol{}
	for _, s := range tsSyms {
		tsByFull[s.FullName()] = s
	}
	reByFull := map[string]Symbol{}
	for _, s := range reSyms {
		reByFull[s.FullName()] = s
	}
	// Every tree-sitter symbol must exist on the regex side, unless
	// documented (field/property declarations).
	for full, ts := range tsByFull {
		re, ok := reByFull[full]
		if !ok {
			if kind, doc := tsOnlySymbols[lang][full]; doc && kind == ts.Kind {
				continue
			}
			t.Errorf("%s: symbol %s (%s, line %d) missing from regex output; tree-sitter-only symbols must be listed in tsOnlySymbols",
				rel, full, ts.Kind, ts.Line)
			continue
		}
		if re.Kind != ts.Kind || re.Line != ts.Line || re.Confidence != ts.Confidence {
			t.Errorf("%s: symbol %s diverges: regex %s/%d/%s vs tree-sitter %s/%d/%s",
				rel, full, re.Kind, re.Line, re.Confidence, ts.Kind, ts.Line, ts.Confidence)
		}
	}
	// Every regex symbol must exist on the tree-sitter side: the regex
	// path must not invent declarations.
	for full := range reByFull {
		if _, ok := tsByFull[full]; !ok {
			t.Errorf("%s: symbol %s exists only in regex output", rel, full)
		}
	}
}

// checkEdgeParity asserts every tree-sitter edge is present on the regex
// side with confidence <= ts confidence, and regex-only edges are exactly
// the documented declaration-line artifacts.
func checkEdgeParity(t *testing.T, rel, lang string, tsCalls, reCalls map[string][]CallEdge) {
	ts := canonEdges(tsCalls)
	re := canonEdges(reCalls)
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
		// 1. Every tree-sitter edge must exist on the regex side.
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
			if !matchedForm {
				t.Errorf("%s: edge %s -> %s (%s) missing from regex output — regex must emit it at <= %s, never drop it",
					rel, owner, target, tsConf, tsConf)
			}
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