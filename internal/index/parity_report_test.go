//go:build !notreesitter

package index

import (
	"fmt"
	"sort"
	"strings"
	"testing"
)

// parity_report_test.go is the Phase-5 Part C headline: per-language
// precision evidence for the heuristic<->tree-sitter parity gate (CG-P0-4).
//
// The superset gate (parity_test.go) asserts the documented contract per
// edge; this report QUANTIFIES it per language over the shared fixture
// corpus — symbol counts, edge counts, shared/regex-only/ts-only edge sets,
// the shared-edge confidence distribution, and the derived regex precision
// (shared/regex) and recall (shared/ts, tree-sitter as the presence
// reference per the superset semantics in parity_test.go's header). The
// tolerance tables in parity_test.go are the EXPLANATION for any ts-only or
// regex-only edge; they are not failures and are not re-asserted here.
//
// Two assertion layers protect against drift:
//   - floor bounds per language (regex recall and precision must stay above
//     the documented lower-fidelity-subset contract — they catch a REGRESSION
//     where the regex path starts missing facts tree-sitter is confident in);
//   - a stable-summary PIN of the exact measured precision/recall per
//     language, so any extractor change that shifts the numbers (even one
//     still above the floors) fails loudly and forces a deliberate pin
//     update. The full table is printed on every run.
//
// The report also instruments the intel layer's bare-callee-never-methods
// guard (internal/intel/queries.go calleeIsTarget): the guard's documented
// rationale is Go-only (a bare reference never names a method under Go
// scoping), so Phase-5 A5 requires per-language evidence that the rule is
// not silently dropping foreign receiver-less method edges. The guard's
// decision is modelled below as a TEST-SIDE REIMPLEMENTATION (internal/index
// cannot import internal/intel — import cycle — so the guard's condition is
// reproduced over the ts-extracted corpus exactly as calleeIsTarget applies
// it: a bare callee that resolves to a method-shaped symbol is dropped only
// when the resolved symbol's language is Go). The pre-fix, language-agnostic
// counterfactual (any language) is also counted and reported so the A5
// verdict is visible in the evidence itself.

// edgeParityCounts is one language's numeric edge-parity summary. shared
// counts tree-sitter edges matched on the regex side — exactly, or in the
// documented last-segment chain form (chainFormLastSegment) — so the
// numbers follow the gate's own tolerance semantics.
type edgeParityCounts struct {
	tsEdges, reEdges int
	shared           int
	tsOnly, reOnly   int
	// confPairs counts shared edges by the (regex, tree-sitter) confidence
	// pair, e.g. "MEDIUM,HIGH" — the over-claim guard (regex <= ts) rendered
	// numerically.
	confPairs map[string]int
}

// edgeParityFor computes the per-file edge parity summary between the
// tree-sitter and regex call maps, mirroring checkEdgeParity's matching
// rules (exact target, chain-form last segment) but counting instead of
// erroring.
func edgeParityFor(tsCalls, reCalls map[string][]CallEdge) edgeParityCounts {
	ts := canonEdges(tsCalls)
	re := canonEdges(reCalls)
	out := edgeParityCounts{confPairs: map[string]int{}}
	for _, tsTargets := range ts {
		out.tsEdges += len(tsTargets)
	}
	for _, reTargets := range re {
		out.reEdges += len(reTargets)
	}
	// Mark regex edges consumed when matched (exact or chain form) so each
	// edge is counted exactly once on each side.
	consumed := map[string]map[string]bool{}
	consume := func(owner, target string) {
		if consumed[owner] == nil {
			consumed[owner] = map[string]bool{}
		}
		consumed[owner][target] = true
	}
	record := func(rc, tc Confidence) {
		out.confPairs[fmt.Sprintf("%s,%s", rc, tc)]++
	}
	for owner, tsTargets := range ts {
		reTargets := re[owner]
		for tsTarget, tc := range tsTargets {
			if rc, ok := reTargets[tsTarget]; ok {
				out.shared++
				consume(owner, tsTarget)
				record(rc, tc)
				continue
			}
			// Chain-form tolerance: regex records the last segment of a
			// dotted ts target ("upper" for "self.greet.upper").
			matched := false
			for reTarget, rc := range reTargets {
				if chainFormLastSegment(owner, tsTarget, reTarget) {
					out.shared++
					consume(owner, reTarget)
					record(rc, tc)
					matched = true
					break
				}
			}
			if !matched {
				out.tsOnly++
			}
		}
	}
	for owner, reTargets := range re {
		for reTarget := range reTargets {
			if consumed[owner] != nil && consumed[owner][reTarget] {
				continue
			}
			out.reOnly++
		}
	}
	return out
}

// guardDropModel is the test-side reimplementation of the intel layer's
// bare-callee-never-methods guard (calleeIsTarget, queries.go). It must be
// kept in lockstep with that function's decision rule:
//
//   - the rule only fires for a RECEIVER-LESS callee endpoint (no dot —
//     "greet", never "g.loud");
//   - it only drops when the callee resolves to a METHOD-shaped symbol
//     (Receiver != ""); in a single-file corpus the resolution is the
//     same-name method existence in the file;
//   - a receiver-qualified CALLER ("Greeter.loud") is rescued by the
//     receiver-matched resolution path that runs BEFORE the rule (the
//     same-package def wins), so only a BARE caller ("run") reaches the
//     rule;
//   - the rule itself is Go-scoped post-Phase-5 A5: it drops only when the
//     resolved symbol's language is "go".
//
// Model coverage note: the "go" branch is VACUOUS here — testfixture/ has no
// .go corpus (Go's extractor is goast, outside the parity surface), so
// goScopedDrops can never exceed 0 from corpus edges. And the TS
// inherited-edge survival shape (this.greet() from a base class) is pinned
// by TestNeverMethodsGuardIsGoScoped in internal/intel, not by this report;
// the model only proves the corpus never regresses the guard's decision.
//
// This is a faithful model of the guard's condition, marked as such per the
// Phase-5 task contract — internal/index cannot call the real guard without
// an import cycle.
type guardDropModel struct {
	// Candidates: receiver-less ts-extracted edges whose callee resolves to
	// a method-shaped symbol in the same file.
	candidates int
	// PreAgnosticDrops: how many candidates a LANGUAGE-AGNOSTIC version of
	// the rule (the pre-A5 behavior) would drop.
	preAgnosticDrops int
	// GoScopedDrops: how many the current Go-scoped rule drops.
	goScopedDrops int
}

// guardDropsFor runs the guard-drop model over one file's ts-extracted
// symbols and call edges. lang is the file's language.
func guardDropsFor(lang string, tsSyms []Symbol, tsCalls map[string][]CallEdge) guardDropModel {
	methodShaped := map[string]bool{}
	for _, s := range tsSyms {
		if s.Receiver != "" {
			methodShaped[s.Name] = true
		}
	}
	var m guardDropModel
	for owner, edges := range tsCalls {
		_, bareCaller := endpointPartsReport(owner)
		for _, e := range edges {
			if strings.Contains(e.Target, ".") || !methodShaped[e.Target] {
				continue // not a receiver-less call to a method-shaped target
			}
			m.candidates++
			// A receiver-qualified caller is rescued by the receiver-matched
			// resolution path before the never-methods rule runs.
			if bareCaller != "" {
				continue
			}
			m.preAgnosticDrops++
			if lang == "go" {
				m.goScopedDrops++
			}
		}
	}
	return m
}

// endpointPartsReport splits an owner key into qualifier and bare name
// (caller-side mirror of intel's endpointParts; used only by the guard
// model).
func endpointPartsReport(ref string) (qual, bare string) {
	if i := strings.LastIndexByte(ref, '.'); i >= 0 {
		return ref[:i], ref[i+1:]
	}
	return "", ref
}

// TestParityReportPerLanguage is the per-language precision report: it runs
// BOTH extractors over the shared fixture corpus, prints the numeric table,
// cross-checks the tiers against Index.PrecisionByLang, asserts the
// documented-subset floor bounds, and pins the exact measured
// precision/recall so drift fails loudly. It runs only in the default
// (tree-sitter) build — the precise path must exist to compare.
func TestParityReportPerLanguage(t *testing.T) {
	entries, err := parityFixtures.ReadDir("testfixture")
	if err != nil {
		t.Fatal(err)
	}
	type langReport struct {
		lang       string
		tsSyms     int
		reSyms     int
		sharedSyms int
		tsOnlySyms int
		reOnlySyms int
		edges      edgeParityCounts
		precision  float64
		recall     float64
		guard      guardDropModel
		pinnedPrec float64
		pinnedRec  float64
	}
	var reports []langReport
	for _, e := range entries {
		rel := "testfixture/" + e.Name()
		b, err := parityFixtures.ReadFile(rel)
		if err != nil {
			t.Fatal(err)
		}
		lang := detectLang(rel, b)
		tsSyms, tsCalls, _, _, errTS := tsExtract(rel, b, lang)
		reSyms, reCalls, _, _, errRE := extractForeignRegex(rel, b, lang)
		if errTS != nil || errRE != nil {
			t.Fatalf("%s: ts=%v re=%v", rel, errTS, errRE)
		}
		r := langReport{lang: lang}
		r.tsSyms, r.reSyms = len(tsSyms), len(reSyms)
		tsNames := map[string]bool{}
		for _, s := range tsSyms {
			tsNames[s.FullName()] = true
		}
		reNames := map[string]bool{}
		for _, s := range reSyms {
			reNames[s.FullName()] = true
		}
		for n := range tsNames {
			if reNames[n] {
				r.sharedSyms++
			} else {
				r.tsOnlySyms++
			}
		}
		for n := range reNames {
			if !tsNames[n] {
				r.reOnlySyms++
			}
		}
		r.edges = edgeParityFor(tsCalls, reCalls)
		if r.edges.reEdges > 0 {
			r.precision = float64(r.edges.shared) / float64(r.edges.reEdges)
		}
		if r.edges.tsEdges > 0 {
			r.recall = float64(r.edges.shared) / float64(r.edges.tsEdges)
		}
		r.guard = guardDropsFor(lang, tsSyms, tsCalls)
		reports = append(reports, r)
	}

	// Stable-summary pin: the exact measured precision/recall per language at
	// HEAD (Phase 5 Part C, 2026-10-06, after the LoudGreeter corpus
	// extension). Recall is 1.0 on every language — the subset contract holds
	// perfectly: the regex path surfaces every tree-sitter edge. Precision is
	// below 1.0 by exactly the DOCUMENTED declaration-line artifacts
	// (regexExtraEdges: 5 java, 4 python, 4 typescript) — verified below.
	// Update deliberately when an extractor improvement shifts the numbers.
	pinned := map[string][2]float64{
		"java":       {0.5454545454545454, 1.0},
		"python":     {0.6666666666666666, 1.0},
		"typescript": {0.6923076923076923, 1.0},
	}
	for i := range reports {
		p, ok := pinned[reports[i].lang]
		if !ok {
			t.Errorf("%s: no pinned precision/recall entry; add one", reports[i].lang)
			continue
		}
		reports[i].pinnedPrec, reports[i].pinnedRec = p[0], p[1]
	}

	// Every regex-only edge must be a DOCUMENTED declaration-line artifact
	// (regexExtraEdges) — the explanation, not a failure. The parity gate
	// hard-asserts this per edge; the report renders it as the precision
	// story so the low precision numbers are provably the documented subset
	// behavior, never a surprise.
	for _, r := range reports {
		if r.edges.reOnly != len(regexExtraEdges[r.lang]) {
			t.Errorf("%s: %d regex-only edges but regexExtraEdges documents %d — undocumented regex-only edges would fail the gate",
				r.lang, r.edges.reOnly, len(regexExtraEdges[r.lang]))
		}
	}

	// Floor bounds: regex is the documented lower-fidelity subset — it may
	// be less certain, never more certain, and never silently drop a fact
	// tree-sitter is confident in. A regression is a NEW drop in shared
	// edges. The floors are BACKSTOPS BY DESIGN, not the primary guards: the
	// exact per-language pins below and the per-edge superset gate in
	// parity_test.go carry the real regression load, and the floors exist so
	// a drift that slips both still fails. floorRecall sits at 0.95 so a
	// single dropped shared edge at current corpus sizes (tsEdges 4-9 per
	// language, recall pinned at exactly 1.0) fails BOTH layers — floor and
	// exact pin. Precision's floor stays lower because java's documented
	// declaration-line artifacts keep measured precision at 0.545-0.692.
	const (
		floorPrecision = 0.45
		floorRecall    = 0.95
		floorShared    = 4
	)

	fmt.Println("=== per-language parity report (regex vs tree-sitter, fixture corpus) ===")
	fmt.Printf("%-12s %6s %6s %6s %6s %6s | %6s %6s %6s %6s %6s | %7s %7s | %9s %8s %8s | %7s %7s\n",
		"lang", "tsSym", "reSym", "shSym", "tsOnly", "reOnly", "tsEdge", "reEdge", "shared", "tsOnlyE", "reOnlyE",
		"prec", "recall", "cand", "agnDrop", "goDrop", "pinPrec", "pinRec")
	for _, r := range reports {
		fmt.Printf("%-12s %6d %6d %6d %6d %6d | %6d %6d %6d %6d %6d | %7.3f %7.3f | %9d %8d %8d | %7.3f %7.3f\n",
			r.lang, r.tsSyms, r.reSyms, r.sharedSyms, r.tsOnlySyms, r.reOnlySyms,
			r.edges.tsEdges, r.edges.reEdges, r.edges.shared, r.edges.tsOnly, r.edges.reOnly,
			r.precision, r.recall, r.guard.candidates, r.guard.preAgnosticDrops, r.guard.goScopedDrops,
			r.pinnedPrec, r.pinnedRec)
	}
	fmt.Println("shared-edge confidence pairs (regex,ts):")
	confAgg := map[string]int{}
	for _, r := range reports {
		for pair, n := range r.edges.confPairs {
			confAgg[pair] += n
		}
	}
	for _, pair := range sortedKeys(confAgg) {
		fmt.Printf("  %s: %d\n", pair, confAgg[pair])
	}

	// Tier cross-check against Index.PrecisionByLang: the corpus languages
	// are extracted at exactly the tier the index records for them. The
	// synthetic index carries one symbol per corpus language so
	// Languages()/computePrecisionByLang see them (Languages derives from
	// Symbols[].Lang).
	ix := &Index{Pkgs: map[string]*Pkg{}}
	for _, r := range reports {
		ix.Pkgs["fixture/"+r.lang] = &Pkg{Name: r.lang, Path: "fixture/" + r.lang, Lang: r.lang}
		ix.Symbols = append(ix.Symbols, Symbol{Name: "Fixture", Kind: "type", File: "fixture/Fixture." + r.lang, Lang: r.lang})
	}
	ix.computePrecisionByLang()
	wantTier := map[string]string{"go": "resolved", "java": "resolved", "python": "ast", "typescript": "ast"}
	for _, r := range reports {
		got, ok := ix.PrecisionByLang[r.lang]
		want, wantOK := wantTier[r.lang]
		if !ok || !wantOK || got != want {
			t.Errorf("%s: PrecisionByLang tier = %q, want %q (corpus extraction tier)", r.lang, got, want)
		}
	}

	for _, r := range reports {
		// Floor bounds (regression catch — a new drop in shared edges).
		if r.edges.shared < floorShared {
			t.Errorf("%s: shared edges = %d < floor %d — the regex path is dropping facts tree-sitter is confident in", r.lang, r.edges.shared, floorShared)
		}
		if r.precision < floorPrecision {
			t.Errorf("%s: regex precision %.3f < floor %.2f — regex-only edges growing beyond the documented artifacts", r.lang, r.precision, floorPrecision)
		}
		if r.recall < floorRecall {
			t.Errorf("%s: regex recall %.3f < floor %.2f — the subset contract is violated", r.lang, r.recall, floorRecall)
		}
		// Stable-summary pin (deliberate-drift catch).
		if abs(r.precision-r.pinnedPrec) > 1e-9 || abs(r.recall-r.pinnedRec) > 1e-9 {
			t.Errorf("%s: precision/recall drifted from pin (%.3f/%.3f -> %.3f/%.3f); update the pin deliberately", r.lang, r.pinnedPrec, r.pinnedRec, r.precision, r.recall)
		}
		// A5 instrument: the Go-scoped never-methods guard must not drop any
		// corpus edge. Candidates with receiver-qualified callers are rescued
		// by the receiver-matched path; the guard model confirms the count.
		if r.guard.goScopedDrops != 0 {
			t.Errorf("%s: Go-scoped never-methods guard model drops %d receiver-less method edges (candidates %d) — corpus edges must survive", r.lang, r.guard.goScopedDrops, r.guard.candidates)
		}
	}
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

func sortedKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
