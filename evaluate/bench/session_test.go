package main

import (
	"math"
	"reflect"
	"testing"
)

// sessionLevelRank orders the progressive-disclosure levels for upgrade
// counting (L1 < L2 < L3; 0 for unknown levels).
func sessionLevelRank(level string) int {
	switch level {
	case "L1":
		return 1
	case "L2":
		return 2
	case "L3":
		return 3
	}
	return 0
}

// TestSessionTotalsMatchDerivedPayloads pins the simulation arithmetic for
// the first scripted plan (medium monolith trace): totals must equal the
// sums of the DERIVED step payloads (no invented constants since the
// 2026-11-20 remediation), B's payload must be exactly two whole-corpus
// token counts, and total = payload + rent on both rows.
func TestSessionTotalsAreDocumentedSums(t *testing.T) {
	p := sessionPlans[0]
	a, b := simulateSession(p)
	if a.steps != 6 || b.steps != 2 {
		t.Fatalf("plan 0 step counts = %d/%d, want 6 (A) / 2 (B)", a.steps, b.steps)
	}
	// A: payload = sum of derived step payloads; rent = sum of re-sent prefixes.
	wantPayload := 0
	for _, s := range p.a {
		if s.payload <= 0 {
			t.Fatalf("derived payload must be positive, got %d for level %s", s.payload, s.level)
		}
		wantPayload += s.payload
	}
	if a.payloadTokens != wantPayload {
		t.Errorf("A payload tokens = %d, want %d (sum of derived step payloads)", a.payloadTokens, wantPayload)
	}
	wantRent := 0
	for _, c := range sessionStepCosts(p.a) {
		wantRent += c
	}
	wantRent -= wantPayload
	if a.prefixRent != wantRent {
		t.Errorf("A prefix rent = %d, want %d", a.prefixRent, wantRent)
	}
	if a.totalTokens != a.payloadTokens+a.prefixRent {
		t.Errorf("A total != payload+rent: %d != %d+%d", a.totalTokens, a.payloadTokens, a.prefixRent)
	}
	// B: both direct steps carry the whole corpus (measured, not invented).
	var corpus string
	for _, f := range fixtures {
		if f.name == p.fixture {
			corpus = f.corpus
			break
		}
	}
	direct := sessionCount(o200kTokenizer(), corpus)
	if direct <= 0 {
		t.Fatalf("whole-corpus payload = %d, want positive", direct)
	}
	if b.payloadTokens != 2*direct {
		t.Errorf("B payload tokens = %d, want 2x whole-corpus (%d)", b.payloadTokens, 2*direct)
	}
	if b.prefixRent != direct {
		t.Errorf("B prefix rent = %d, want %d (step 2 re-sends step 1)", b.prefixRent, direct)
	}
	if b.totalTokens != b.payloadTokens+b.prefixRent {
		t.Errorf("B total != payload+rent: %d != %d+%d", b.totalTokens, b.payloadTokens, b.prefixRent)
	}
}

// TestSessionPrefixRentPerStep checks the residual-context rent accounting:
// step k's request carries the payloads of steps 1..k-1 as a re-sent prefix.
func TestSessionPrefixRentPerStep(t *testing.T) {
	steps := []sessionStep{
		{level: "L1", payload: 100},
		{level: "L2", payload: 50},
		{level: "L3", payload: 25},
	}
	costs := sessionStepCosts(steps)
	want := []int{100, 150, 175} // 100; 100+50; 100+50+25
	if !reflect.DeepEqual(costs, want) {
		t.Fatalf("per-step costs = %v, want %v", costs, want)
	}
	// Step 2's request includes the prefix of step 1 (100 tokens).
	if costs[1] != 100+50 {
		t.Errorf("step 2 cost = %d, want payload 50 + re-sent prefix 100", costs[1])
	}
	// Step 3's request includes the prefixes of steps 1..2 (150 tokens).
	if costs[2] != 100+50+25 {
		t.Errorf("step 3 cost = %d, want payload 25 + re-sent prefix 150", costs[2])
	}
	// Aggregate: payload 175, rent 0+100+150=250, total 425.
	r := computeStrategy("t", "s", steps)
	if r.payloadTokens != 175 || r.prefixRent != 250 || r.totalTokens != 425 {
		t.Errorf("aggregate = payload %d rent %d total %d, want 175/250/425",
			r.payloadTokens, r.prefixRent, r.totalTokens)
	}
}

// TestSessionDeterminism: the simulation is pure arithmetic, so two runs must
// produce identical rows.
func TestSessionDeterminism(t *testing.T) {
	first := runSessionMetrics()
	second := runSessionMetrics()
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("session simulation is not deterministic:\nfirst=%+v\nsecond=%+v", first, second)
	}
	if len(first) != 2*len(sessionPlans) {
		t.Fatalf("got %d rows, want 2 per plan (%d)", len(first), len(sessionPlans))
	}
}

// TestSessionDeltaMath pins the delta% (B vs A) arithmetic and its placement
// on strategy B rows only.
func TestSessionDeltaMath(t *testing.T) {
	a, b := simulateSession(sessionPlans[0])
	if a.hasDelta {
		t.Error("A row must not carry a delta% (B vs A)")
	}
	if !b.hasDelta {
		t.Fatal("B row must carry the delta% (B vs A)")
	}
	want := (float64(b.totalTokens) - float64(a.totalTokens)) / float64(a.totalTokens) * 100
	if math.Abs(b.deltaPct-want) > 1e-9 {
		t.Errorf("delta%% = %.6f, want %.6f", b.deltaPct, want)
	}
	// Documented direction for plan 0 with derived payloads: A pays the
	// prefix rent across 6 steps (~60% windows), B's 2 direct calls plus one
	// re-sent corpus stay cheaper -> negative (B cheaper than A).
	if b.deltaPct >= 0 {
		t.Errorf("plan 0 delta%% = %.4f, want negative (B cheaper than A)", b.deltaPct)
	}
	// Zero-total guard: an empty A plan must not produce a delta.
	_, b2 := simulateSession(sessionPlan{task: "empty", a: nil, b: []sessionStep{{level: "direct", payload: 100}}})
	if b2.hasDelta {
		t.Error("delta must not be set when A has no tokens")
	}
}

// TestSessionPlansAreWellFormed mirrors TestFixtureTaskMatrixNonDegenerate
// over the derived plans: plans must target existing fixture corpora, A must
// run 4-6 steps with exactly 2 level upgrades, B must run 2 direct steps,
// every derived payload must be positive, and the level ordering must be
// strictly increasing (L1 < L2 < L3 < direct on the same corpus).
func TestSessionPlansAreWellFormed(t *testing.T) {
	if len(sessionPlans) != len(sessionPlanSpecs) {
		t.Fatalf("built plans = %d, specs = %d (must match)", len(sessionPlans), len(sessionPlanSpecs))
	}
	if len(sessionPlans) < 2 || len(sessionPlans) > 3 {
		t.Fatalf("want 2-3 scripted session plans, got %d", len(sessionPlans))
	}
	if sessionTokensLabel != "exact o200k" && sessionTokensLabel != "estimator" {
		t.Errorf("sessionTokensLabel = %q, want exact o200k or estimator", sessionTokensLabel)
	}
	known := map[string]bool{}
	for _, f := range fixtures {
		known[f.name] = true
	}
	for _, p := range sessionPlans {
		if !known[p.fixture] {
			t.Errorf("plan %q targets unknown fixture %q (must reuse the harness corpus)", p.task, p.fixture)
		}
		if len(p.a) < 4 || len(p.a) > 6 {
			t.Errorf("plan %q: A step count = %d, want 4-6", p.task, len(p.a))
		}
		if len(p.b) != 2 {
			t.Errorf("plan %q: B step count = %d, want 2", p.task, len(p.b))
		}
		upgrades := 0
		prev := ""
		for _, s := range p.a {
			if sessionLevelRank(s.level) == 0 {
				t.Errorf("plan %q: A step has unknown level %q", p.task, s.level)
			}
			if prev != "" && sessionLevelRank(s.level) > sessionLevelRank(prev) {
				upgrades++
			}
			prev = s.level
		}
		if upgrades != 2 {
			t.Errorf("plan %q: A has %d level upgrades, want 2", p.task, upgrades)
		}
		// Every derived payload must be a positive measured token count.
		for _, s := range p.a {
			if s.payload <= 0 {
				t.Errorf("plan %q: derived payload for %s = %d, want positive", p.task, s.level, s.payload)
			}
		}
		var corpus string
		for _, f := range fixtures {
			if f.name == p.fixture {
				corpus = f.corpus
				break
			}
		}
		direct := sessionCount(o200kTokenizer(), corpus)
		for _, s := range p.b {
			if s.level != "direct" || s.payload != direct {
				t.Errorf("plan %q: B step = %+v, want direct with whole-corpus payload %d", p.task, s, direct)
			}
		}
		// Level ordering on the same corpus: L1 < L2 < L3 <= direct
		// (windows nest; the whole corpus is the largest view).
		var l1, l2, l3 int
		for _, s := range p.a {
			switch s.level {
			case "L1":
				l1 = s.payload
			case "L2":
				l2 = s.payload
			case "L3":
				if l3 == 0 {
					l3 = s.payload
				}
			}
		}
		if !(l1 < l2 && l2 < l3 && l3 <= direct) {
			t.Errorf("plan %q: payload ordering L1=%d L2=%d L3=%d direct=%d, want strictly increasing capped by direct",
				p.task, l1, l2, l3, direct)
		}
	}
}
