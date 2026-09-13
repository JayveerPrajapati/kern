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

// TestSessionTotalsAreDocumentedSums pins the simulation arithmetic for the
// first scripted plan (medium monolith trace): the totals must equal the
// hand-computed sums for the documented constants (L1=120, L2=400, L3=1800,
// direct=3200).
func TestSessionTotalsAreDocumentedSums(t *testing.T) {
	a, b := simulateSession(sessionPlans[0])
	if a.steps != 6 || b.steps != 2 {
		t.Fatalf("plan 0 step counts = %d/%d, want 6 (A) / 2 (B)", a.steps, b.steps)
	}
	// A: 6 steps of 120,400,1800,1800,1800,1800.
	if a.payloadTokens != 7720 {
		t.Errorf("A payload tokens = %d, want 7720 (120+400+4x1800)", a.payloadTokens)
	}
	if a.prefixRent != 13000 {
		t.Errorf("A prefix rent = %d, want 13000 (0+120+520+2320+4120+5920)", a.prefixRent)
	}
	if a.totalTokens != 20720 {
		t.Errorf("A total tokens = %d, want 20720 (payload 7720 + rent 13000)", a.totalTokens)
	}
	// B: 2 direct steps of 3200.
	if b.payloadTokens != 6400 {
		t.Errorf("B payload tokens = %d, want 6400 (2x3200)", b.payloadTokens)
	}
	if b.prefixRent != 3200 {
		t.Errorf("B prefix rent = %d, want 3200 (step 2 re-sends step 1)", b.prefixRent)
	}
	if b.totalTokens != 9600 {
		t.Errorf("B total tokens = %d, want 9600 (payload 6400 + rent 3200)", b.totalTokens)
	}
	// Invariant: total = payload + rent for both strategies.
	if a.totalTokens != a.payloadTokens+a.prefixRent {
		t.Errorf("A total != payload+rent: %d != %d+%d", a.totalTokens, a.payloadTokens, a.prefixRent)
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
	// Documented value for plan 0: A=20720, B=9600 -> negative (B cheaper than A).
	if b.deltaPct >= 0 {
		t.Errorf("plan 0 delta%% = %.4f, want negative (B cheaper than A)", b.deltaPct)
	}
	// Zero-total guard: an empty A plan must not produce a delta.
	_, b2 := simulateSession(sessionPlan{task: "empty", a: nil, b: []sessionStep{{level: "direct", payload: sessionDirect}}})
	if b2.hasDelta {
		t.Error("delta must not be set when A has no tokens")
	}
}

// TestSessionPlansAreWellFormed mirrors TestFixtureTaskMatrixNonDegenerate:
// plans must target existing fixture corpora, A must run 4-6 steps with
// exactly 2 level upgrades, B must run 2 direct steps, and payload constants
// must stay strictly increasing and representative.
func TestSessionPlansAreWellFormed(t *testing.T) {
	if len(sessionPlans) < 2 || len(sessionPlans) > 3 {
		t.Fatalf("want 2-3 scripted session plans, got %d", len(sessionPlans))
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
		for _, s := range p.b {
			if s.level != "direct" || s.payload != sessionDirect {
				t.Errorf("plan %q: B step = %+v, want direct with payload %d", p.task, s, sessionDirect)
			}
		}
	}
	// Payload constants must stay representative and strictly increasing.
	if !(sessionL1 < sessionL2 && sessionL2 < sessionL3 && sessionL3 < sessionDirect) {
		t.Errorf("payload constants not strictly increasing: L1=%d L2=%d L3=%d direct=%d",
			sessionL1, sessionL2, sessionL3, sessionDirect)
	}
}
