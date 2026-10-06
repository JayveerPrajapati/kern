package intel

import (
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// dispatchIndex models the real-world cross-package method collision behind
// the F5 parity finding: two packages (internal/bpcli/mcp and internal/mcp)
// each define Server.dispatch (and Server.Serve, Server.handleHTTP), and the
// index's bare-name call buckets pool their "calls" edges under
// receiver-qualified endpoints ("Server.dispatch"). Graph node IDs are
// package-scoped ("internal/bpcli/mcp.Server.dispatch"), so the bare-name
// index cannot resolve the pooled endpoints to either definition; the
// index-level CallersFor/CallsFor (explore's source) report the same 7
// callers / 24 callees for BOTH definitions. Impact must agree.
func dispatchIndex() *index.Index {
	ix := &index.Index{
		Root: "/fake",
		Symbols: []index.Symbol{
			// internal/bpcli/mcp: the governance MCP server.
			{Kind: "type", Name: "Server", File: "bpcli/mcp/server.go", Line: 1, Lang: "go"},
			{Kind: "method", Name: "Serve", Receiver: "Server", File: "bpcli/mcp/server.go", Line: 2, Lang: "go"},
			{Kind: "method", Name: "dispatch", Receiver: "Server", File: "bpcli/mcp/server.go", Line: 3, Lang: "go"},
			{Kind: "method", Name: "handleHTTP", Receiver: "Server", File: "bpcli/mcp/server.go", Line: 4, Lang: "go"},
			{Kind: "method", Name: "callTool", Receiver: "Server", File: "bpcli/mcp/server.go", Line: 5, Lang: "go"},
			{Kind: "type", Name: "Gate", File: "bpcli/mcp/gate.go", Line: 1, Lang: "go"},
			{Kind: "method", Name: "Check", Receiver: "Gate", File: "bpcli/mcp/gate.go", Line: 2, Lang: "go"},
			// internal/mcp: the plain MCP server (same receiver-qualified names).
			{Kind: "type", Name: "Server", File: "mcp/server.go", Line: 1, Lang: "go"},
			{Kind: "method", Name: "Serve", Receiver: "Server", File: "mcp/server.go", Line: 2, Lang: "go"},
			{Kind: "method", Name: "dispatch", Receiver: "Server", File: "mcp/server.go", Line: 3, Lang: "go"},
			{Kind: "method", Name: "handleHTTP", Receiver: "Server", File: "mcp/server.go", Line: 4, Lang: "go"},
			{Kind: "func", Name: "TestCancelRequestAbortsInflight", File: "mcp/server_hardening_test.go", Line: 1, Lang: "go"},
			{Kind: "func", Name: "TestCancelRequestAbortsRealToolCall", File: "mcp/server_hardening_test.go", Line: 2, Lang: "go"},
			{Kind: "func", Name: "TestCancelRequestAsNotification", File: "mcp/server_hardening_test.go", Line: 3, Lang: "go"},
			{Kind: "func", Name: "TestCancelRequestStringID", File: "mcp/server_hardening_test.go", Line: 4, Lang: "go"},
		},
		// Bare-name call buckets: the caller keys are the receiver-qualified
		// names the index records; both packages' same-named methods share one
		// bucket, exactly as in the live index (explore's 7 callers).
		Calls: map[string][]index.CallEdge{
			"Server.Serve":                        {{Target: "Server.dispatch", Confidence: index.ConfidenceHigh}},
			"Server.handleHTTP":                   {{Target: "Server.dispatch", Confidence: index.ConfidenceHigh}},
			"Server.safeDispatch":                 {{Target: "Server.dispatch", Confidence: index.ConfidenceHigh}},
			"TestCancelRequestAbortsInflight":     {{Target: "Server.dispatch", Confidence: index.ConfidenceHigh}},
			"TestCancelRequestAbortsRealToolCall": {{Target: "Server.dispatch", Confidence: index.ConfidenceHigh}},
			"TestCancelRequestAsNotification":     {{Target: "Server.dispatch", Confidence: index.ConfidenceHigh}},
			"TestCancelRequestStringID":           {{Target: "Server.dispatch", Confidence: index.ConfidenceHigh}},
			"Server.dispatch": {
				{Target: "Gate.Check", Confidence: index.ConfidenceHigh},
				{Target: "Server.callTool", Confidence: index.ConfidenceHigh},
				{Target: "json.Unmarshal", Confidence: index.ConfidenceHigh},
				{Target: "s.gate.Check.Error", Confidence: index.ConfidenceHigh},
			},
		},
		Callers: map[string][]string{
			"Server.dispatch": {"Server.Serve", "Server.handleHTTP", "Server.safeDispatch",
				"TestCancelRequestAbortsInflight", "TestCancelRequestAbortsRealToolCall",
				"TestCancelRequestAsNotification", "TestCancelRequestStringID"},
		},
		Pkgs: map[string]*index.Pkg{
			"internal/bpcli/mcp": {Name: "mcp", Path: "internal/bpcli/mcp", Files: []string{"bpcli/mcp/server.go", "bpcli/mcp/gate.go"}, Lang: "go"},
			"internal/mcp":       {Name: "mcp", Path: "internal/mcp", Files: []string{"mcp/server.go", "mcp/server_hardening_test.go"}, Lang: "go"},
		},
	}
	return ix
}

// simpleNames reduces rendered names ("Server.Serve", "s.gate.Check.Error")
// to their last identifier segment — the form explore reports.
func simpleNames(names []string) []string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		if i := lastDot(n); i >= 0 {
			n = n[i+1:]
		}
		out = append(out, n)
	}
	return out
}

func lastDot(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '.' {
			return i
		}
	}
	return -1
}

// TestImpactExploreParityDispatch pins F5: kern impact and kern explore must
// agree on the same cross-package method even though the index pools the
// receiver-qualified edges of two same-named definitions. Impact's raw-edge
// collectors (DirectCallersNames / DirectDependsOnNames) must report the
// same caller/callee sets the index-level explore reads for the symbol.
func TestImpactExploreParityDispatch(t *testing.T) {
	g := FromIndex(dispatchIndex())

	// explore's index-level view of EITHER dispatch definition: 7 callers.
	wantCallers := []string{
		"Serve", "handleHTTP", "safeDispatch",
		"TestCancelRequestAbortsInflight", "TestCancelRequestAbortsRealToolCall",
		"TestCancelRequestAsNotification", "TestCancelRequestStringID",
	}
	// explore's index-level callees, unique simple names.
	wantCallees := []string{
		"Check", "callTool", "Unmarshal", "Error",
	}

	for _, target := range []string{
		"internal/bpcli/mcp.Server.dispatch",
		"internal/mcp.Server.dispatch",
	} {
		if !g.Resolvable(target) {
			t.Fatalf("target %q must resolve to a graph node", target)
		}
		callers := simpleNames(g.DirectCallersNames(target, false))
		sort.Strings(callers)
		if got, want := len(callers), len(wantCallers); got != want {
			t.Errorf("%s: DirectCallersNames = %d callers, want %d (explore parity): %v", target, got, want, callers)
		}
		for _, w := range wantCallers {
			if !containsString(callers, w) {
				t.Errorf("%s: caller %q missing from impact's caller set: %v", target, w, callers)
			}
		}
		callees := simpleNames(g.DirectDependsOnNames(target, false))
		for _, w := range wantCallees {
			if !containsString(callees, w) {
				t.Errorf("%s: callee %q missing from impact's callee set: %v", target, w, callees)
			}
		}
	}
}

// TestImpactExploreCallerParityBroad pins caller parity between kern impact
// (Graph.DirectCallersNames — what collectGraphImpact feeds TaskService.Impact)
// and kern explore (Index.CallersFor — what ExploreBudgeted renders) across a
// broad sample of a REAL index: the kern tree itself is built in-test with the
// same AST extraction `kern index` performs. The sample uses three
// deterministic strata so no symbol class can silently escape the pin:
//   - the alphabetical first 100 eligible symbols (callers>0 && callees>0),
//     unchanged from the original pin;
//   - a stride sample — every 50th eligible symbol — so mid-alphabet hubs
//     ("New", "Run") are no longer skipped by the alphabetical cut;
//   - every eligible symbol whose simple name has >=5 same-named definitions
//     (hub names — the shared bare-bucket attribution class), capped at 100;
//   - every eligible symbol whose simple name has 2-4 same-named definitions
//     (the near-hub coverage hole between the stride sample and the >=5 hub
//     class — names shared by a handful of packages are where bare-bucket
//     mis-attribution most often hides), capped at 100;
//   - the pinned internal/app.New case, the live 74-vs-78 divergence: explore
//     over-attributed four learning.New callers (RecordArchitectureDrift,
//     RecordDogfood, RecordPolicySignals, RecordSurfaceDrift) to app.New via
//     the shared bare "New" bucket until computeCallers stopped merging
//     cross-package qualified callee callers into it.
//
// Fixture-file symbols are excluded from the sample: the graph's first-match
// receiver resolution can attribute a chained call to a fixture definition
// (the evaluate/*/fixture store.Save) that the shared index method bucket
// cannot express per-symbol, and the parity scope is production trust (entry
// resolution already pins "never resolve to a fixture").
//
// The core pin is exact set equality on normalized simple names (the
// documented qualified-edge quirk — graph renders a caller under its bare
// node name while the index bucket may keep the qualified key — is tolerated
// via last-segment normalization). The raw qualified forms are asserted too,
// so a regression that ADDS a qualifier to one side still fails even though
// the normalized names would keep matching; the two surviving raw-form
// quirks (metrics Default, memory Recall — re-verified normalized-equal in
// Phase 5 Part C) are report-only in knownRawFormQuirks.
func TestImpactExploreCallerParityBroad(t *testing.T) {
	// "../.." is the repo root from the intel package dir: a real,
	// multi-package call graph (thousands of symbols) rather than a
	// hand-built fixture.
	ix, err := index.Build("../..")
	if err != nil {
		t.Fatalf("index.Build(../..): %v", err)
	}
	g := FromIndex(ix)

	// Eligible: symbols with >=1 caller AND >=1 callee, sorted by FullName()
	// for determinism. Fixture-file symbols (testdata/fixtures/fixture dirs)
	// are excluded: the graph's first-match receiver resolution can attribute
	// a chained call to a fixture definition (the evaluate/*/fixture
	// store.Save) that the shared index method bucket cannot express
	// per-symbol, and the parity campaign's scope is production trust (entry
	// resolution already pins "never resolve to a fixture").
	var eligible []index.Symbol
	for _, s := range ix.Symbols {
		if isFixtureFile(s.File) {
			continue
		}
		if len(ix.CallersFor(s)) == 0 || len(ix.CallsFor(s)) == 0 {
			continue
		}
		eligible = append(eligible, s)
	}
	sort.Slice(eligible, func(i, j int) bool { return eligible[i].FullName() < eligible[j].FullName() })

	// Hub names: simple names shared by >=5 definitions (the bare-bucket
	// attribution class the alphabetical first-100 could skip entirely).
	nameDefs := map[string]int{}
	for _, s := range ix.Symbols {
		nameDefs[s.Name]++
	}

	inSample := map[string]bool{}
	var sample []index.Symbol
	add := func(s index.Symbol) {
		key := s.FullName() + "\x00" + s.File + "\x00" + strconv.Itoa(s.Line)
		if inSample[key] {
			return
		}
		inSample[key] = true
		sample = append(sample, s)
	}
	// Stratum 1: the alphabetical first 100 (the original pin).
	for _, s := range eligible[:min(100, len(eligible))] {
		add(s)
	}
	// Stratum 2: stride — every 50th eligible symbol, capped at 100.
	strideAdded := 0
	for i := 0; i < len(eligible) && strideAdded < 100; i += 50 {
		add(eligible[i])
		strideAdded++
	}
	// Stratum 3: hub names (>=5 same-named definitions), capped at 100.
	hubAdded := 0
	for _, s := range eligible {
		if nameDefs[s.Name] >= 5 {
			add(s)
			hubAdded++
			if hubAdded >= 100 {
				break
			}
		}
	}
	// Stratum 4: near-hub names (2-4 same-named definitions) — the coverage
	// hole between the stride sample and the >=5 hub class. Bare-bucket
	// attribution errors are most plausible exactly here (a name shared by a
	// handful of packages), and neither the alphabetical cut nor the stride
	// is guaranteed to hit one. Capped like the others for runtime bounds.
	nearHubAdded := 0
	for _, s := range eligible {
		if d := nameDefs[s.Name]; d >= 2 && d <= 4 {
			add(s)
			nearHubAdded++
			if nearHubAdded >= 100 {
				break
			}
		}
	}
	// Pinned case: internal/app.New — the live 74-vs-78 caller divergence.
	for _, s := range ix.Symbols {
		if s.Name == "New" && s.File == "internal/app/platform.go" {
			add(s)
			break
		}
	}
	// Deterministic order for the assertion loop.
	sort.Slice(sample, func(i, j int) bool { return sample[i].FullName() < sample[j].FullName() })
	t.Logf("parity sample: %d symbols (first-100 + every-50th stride + hub names + near-hub 2-4 names + pinned app.New), total symbols %d",
		len(sample), len(ix.Symbols))

	// Graph node IDs are package-scoped — the same formula FromIndex uses.
	pkgByFile := graphPackagePathByFile(ix)
	nodeID := func(s index.Symbol) string {
		p := pkgByFile[s.File]
		if p == "" {
			p = filepath.Dir(s.File)
		}
		if p == "" || p == "." {
			return s.FullName()
		}
		return p + "." + s.FullName()
	}

	for _, s := range sample {
		target := nodeID(s)
		if !g.Resolvable(target) {
			t.Logf("%s (%s): graph node %q not resolvable — skipped (impact cannot address this symbol)",
				s.FullName(), s.File, target)
			continue
		}
		impact := g.DirectCallersNames(target, false)
		explore := ix.CallersFor(s)
		gapKey := s.FullName() + "|" + s.File

		// Core pin: exact set equality on normalized simple names.
		impactNorm, exploreNorm := simpleNames(impact), simpleNames(explore)
		sort.Strings(impactNorm)
		sort.Strings(exploreNorm)
		if !slices.Equal(impactNorm, exploreNorm) {
			t.Errorf("%s (%s): caller parity divergence — impact(DirectCallersNames) %v vs explore(CallersFor) %v",
				s.FullName(), s.File, impactNorm, exploreNorm)
		}

		// Regression pin: the raw qualified forms must agree as well, so a
		// change that adds/removes a qualifier on one side still fails. A
		// known report-only quirk is exempt here too (its raw forms diverge
		// by the same render-form set), as are the documented qualified-edge
		// quirks — raw forms differing by a qualifier while the normalized
		// sets agree (pre-existing; the graph renders a caller under its
		// qualified node name while the index bucket keeps the bare key, or
		// vice versa).
		if !knownRawFormQuirks[gapKey] {
			impactRaw, exploreRaw := append([]string(nil), impact...), append([]string(nil), explore...)
			sort.Strings(impactRaw)
			sort.Strings(exploreRaw)
			if !slices.Equal(impactRaw, exploreRaw) {
				t.Errorf("%s (%s): caller raw-form divergence — impact %v vs explore %v (normalized: %v vs %v)",
					s.FullName(), s.File, impactRaw, exploreRaw, impactNorm, exploreNorm)
			}
		}
	}
}

// knownRawFormQuirks lists symbols whose raw caller forms differ by a
// qualifier while their normalized sets agree — the documented qualified-edge
// quirk in either direction (the graph renders "Bundle.Explain" while the
// index bucket keeps "Explain"). Report-only for the raw-form pin; the
// normalized set-equality pin still hard-fails for these symbols, so a real
// caller-set regression cannot hide behind the quirk. This is a render-form
// difference, not an attribution gap: the graph renders a caller under its
// node's Qualified name while CallersFor returns the raw recorded bucket key,
// so the two surfaces cannot be forced to byte-equal without changing one
// side's display contract.
var knownRawFormQuirks = map[string]bool{
	// Bundle.Verify (deep-dive 2026-10-05): the graph renders the caller
	// under its node's Qualified name while CallersFor returns the raw
	// recorded bucket key — a render-form difference, not an attribution
	// gap (normalized sets equal; the same call site, two identifier
	// forms).
	"Bundle.Verify|internal/evidence/bundle.go": true,
	// docsearch.Load (Phase 3 parity campaign 2026-10-06): the bare pooled
	// caller "Search" (six project defs — the docsearch Index.Search, the
	// index package's Index.Search, three mcp funcs, CapabilityRegistry.Search)
	// resolves target-anchored to the docsearch Index.Search on the graph
	// side (its Qualified name "Index.Search" renders) while CallersFor keeps
	// the raw bare bucket key ("Search"). Same call site, two identifier
	// forms — the documented qualified-edge quirk; normalized sets are equal
	// and still hard-asserted, so an attribution regression cannot hide
	// behind this entry.
	"Load|internal/docsearch/docsearch.go": true,
	// Default (internal/metrics/metrics.go) and Recall
	// (internal/memory/memory.go): the same qualified-edge render quirk —
	// the graph renders a bare caller under its resolved node's Qualified
	// name ("Recorder.Load", "MemoryStore.Recall") while CallersFor keeps
	// the raw bare bucket key ("Load", "Recall"). Re-verified in Phase 5
	// Part C (2026-10-06): normalized sets are exactly equal on both, so
	// these are genuinely render-form differences, not attribution gaps;
	// the normalized pin still hard-fails for both.
	"Default|internal/metrics/metrics.go": true,
	"Recall|internal/memory/memory.go":    true,
}

func containsString(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
