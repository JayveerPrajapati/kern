package app

import (
	"strings"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/memory"
)

// TestDogfoodPatternsFailureThenPassInfers proves the core meta-loop
// inference: a (gate, signature) pair that failed twice and then passed once
// becomes exactly one INFERENCE pattern with the deterministic statement,
// scope and count — the "this class of gate failure recurs; it was fixed
// before" claim the next self-refactor reads.
func TestDogfoodPatternsFailureThenPassInfers(t *testing.T) {
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	records := []domain.DogfoodRecord{
		{Gate: "check", Signature: "architecture", Failed: true, At: base},
		{Gate: "check", Signature: "architecture", Failed: true, At: base.Add(time.Hour)},
		{Gate: "check", Signature: "architecture", Failed: false, At: base.Add(2 * time.Hour)},
	}
	patterns := DogfoodPatterns(records, DefaultDogfoodMinFailures)
	if len(patterns) != 1 {
		t.Fatalf("patterns = %d, want 1", len(patterns))
	}
	p := patterns[0]
	if p.Key != "dogfood:check:architecture" {
		t.Errorf("Key = %q, want dogfood:check:architecture", p.Key)
	}
	if p.ClaimType != domain.ClaimInference {
		t.Errorf("ClaimType = %q, want INFERENCE", p.ClaimType)
	}
	if p.Count != 3 {
		t.Errorf("Count = %d, want 3 (2 failures + 1 pass)", p.Count)
	}
	want := "gate check failed on architecture 2 time(s), then passed 1 time(s) — recurring breakage class; failure+fix recorded for next refactor context"
	if p.Statement != want {
		t.Errorf("Statement = %q, want %q", p.Statement, want)
	}
	if len(p.Scopes) != 1 || p.Scopes[0] != "dogfood:check:architecture" {
		t.Errorf("Scopes = %v, want [dogfood:check:architecture]", p.Scopes)
	}
	if len(p.Provenance.Sources) != 3 {
		t.Errorf("Provenance.Sources = %v, want 3 deduped timestamps", p.Provenance.Sources)
	}
}

// TestDogfoodPatternsFailureWithoutPassNothing proves the honesty rule: a
// pair with failures but no observed pass contributes nothing — the fix has
// not been observed yet, so no "recurring breakage class" claim is made.
func TestDogfoodPatternsFailureWithoutPassNothing(t *testing.T) {
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	records := []domain.DogfoodRecord{
		{Gate: "check", Signature: "architecture", Failed: true, At: base},
		{Gate: "check", Signature: "architecture", Failed: true, At: base.Add(time.Hour)},
		// another pair with failures but no pass either
		{Gate: "doctor", Signature: "arch-drift:internal/web:loc-over-cap", Failed: true, At: base},
	}
	if patterns := DogfoodPatterns(records, DefaultDogfoodMinFailures); len(patterns) != 0 {
		t.Fatalf("patterns = %d, want 0 (no pass observed)", len(patterns))
	}
}

// TestDogfoodPatternsPassOnlyNothing proves that pass-only groups contribute
// nothing — there is no failure class to learn from.
func TestDogfoodPatternsPassOnlyNothing(t *testing.T) {
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	records := []domain.DogfoodRecord{
		{Gate: "check", Signature: "architecture", Failed: false, At: base},
		{Gate: "check", Signature: "architecture", Failed: false, At: base.Add(time.Hour)},
		{Gate: "doctor", Signature: "arch-drift", Failed: false, At: base},
	}
	if patterns := DogfoodPatterns(records, DefaultDogfoodMinFailures); len(patterns) != 0 {
		t.Fatalf("patterns = %d, want 0 (passes only)", len(patterns))
	}
}

// TestDogfoodPatternsBelowThresholdNothing proves the minFailures guard: one
// failure plus a pass is below the default threshold of 2, so nothing is
// proposed until the failure recurs.
func TestDogfoodPatternsBelowThresholdNothing(t *testing.T) {
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	records := []domain.DogfoodRecord{
		{Gate: "check", Signature: "architecture", Failed: true, At: base},
		{Gate: "check", Signature: "architecture", Failed: false, At: base.Add(time.Hour)},
	}
	if patterns := DogfoodPatterns(records, DefaultDogfoodMinFailures); len(patterns) != 0 {
		t.Fatalf("patterns = %d, want 0 (1 failure < threshold 2)", len(patterns))
	}
}

// TestDogfoodPatternsDeterministic proves input ordering never changes the
// output: the same records in shuffled order yield byte-identical patterns,
// and mixed (gate, signature) pairs are ordered deterministically.
func TestDogfoodPatternsDeterministic(t *testing.T) {
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	records := []domain.DogfoodRecord{
		{Gate: "doctor", Signature: "arch-drift:internal/web:loc-over-cap", Failed: true, At: base},
		{Gate: "check", Signature: "architecture", Failed: true, At: base},
		{Gate: "check", Signature: "architecture", Failed: false, At: base.Add(2 * time.Hour)},
		{Gate: "doctor", Signature: "arch-drift:internal/web:loc-over-cap", Failed: false, At: base.Add(3 * time.Hour)},
		{Gate: "check", Signature: "architecture", Failed: true, At: base.Add(time.Hour)},
		{Gate: "doctor", Signature: "arch-drift:internal/web:loc-over-cap", Failed: true, At: base.Add(4 * time.Hour)},
	}
	want := DogfoodPatterns(records, DefaultDogfoodMinFailures)
	if len(want) != 2 {
		t.Fatalf("patterns = %d, want 2", len(want))
	}
	// Shuffle deterministically: reverse order.
	shuffled := make([]domain.DogfoodRecord, len(records))
	for i := range records {
		shuffled[i] = records[len(records)-1-i]
	}
	got := DogfoodPatterns(shuffled, DefaultDogfoodMinFailures)
	if len(got) != len(want) {
		t.Fatalf("shuffled patterns = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Key != want[i].Key || got[i].Statement != want[i].Statement || got[i].Count != want[i].Count {
			t.Errorf("pattern %d differs after shuffle: got %+v, want %+v", i, got[i], want[i])
		}
	}
	// Deterministic ordering: check gate before doctor gate, statements stable.
	if want[0].Key != "dogfood:check:architecture" || want[1].Key != "dogfood:doctor:arch-drift:internal/web:loc-over-cap" {
		t.Errorf("pattern order = [%s, %s], want check then doctor", want[0].Key, want[1].Key)
	}
}

// TestRecordDogfoodWritesInference proves the full learning pass: a pair with
// 2 failures and a later pass writes exactly one INFERENCE typed-claim memory
// through the learning path (deterministic statement, "dogfood:..." scope),
// and a second identical batch upserts the same scope instead of duplicating
// (idempotent).
func TestRecordDogfoodWritesInference(t *testing.T) {
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	records := []domain.DogfoodRecord{
		{Gate: "check", Signature: "architecture", Failed: true, At: base},
		{Gate: "check", Signature: "architecture", Failed: true, At: base.Add(time.Hour)},
		{Gate: "check", Signature: "architecture", Failed: false, At: base.Add(2 * time.Hour)},
	}
	root := t.TempDir()
	mem := memory.NewMemoryStore(root)
	n, err := RecordDogfood(records, mem, DefaultDogfoodMinFailures)
	if err != nil {
		t.Fatalf("RecordDogfood: %v", err)
	}
	if n != 1 {
		t.Fatalf("written = %d, want 1", n)
	}
	mems, err := mem.List(domain.MemoryConstraint)
	if err != nil {
		t.Fatalf("memory List: %v", err)
	}
	if len(mems) != 1 {
		t.Fatalf("memories = %d, want 1", len(mems))
	}
	m := mems[0]
	if m.ClaimType != domain.ClaimInference {
		t.Errorf("ClaimType = %q, want INFERENCE", m.ClaimType)
	}
	if m.Scope != "dogfood:check:architecture" {
		t.Errorf("Scope = %q, want dogfood:check:architecture", m.Scope)
	}
	if !strings.Contains(m.Content, "gate check failed on architecture 2 time(s), then passed 1 time(s)") {
		t.Errorf("Content = %q, want the recurring-breakage statement", m.Content)
	}
	// Second identical batch: the accumulator grows, so the same scope is
	// upserted — no duplicate memory.
	n2, err := RecordDogfood(records, mem, DefaultDogfoodMinFailures)
	if err != nil {
		t.Fatalf("RecordDogfood (2nd): %v", err)
	}
	if n2 != 1 {
		t.Fatalf("written (2nd) = %d, want 1 (upsert)", n2)
	}
	mems, err = mem.List(domain.MemoryConstraint)
	if err != nil {
		t.Fatalf("memory List (2nd): %v", err)
	}
	if len(mems) != 1 {
		t.Fatalf("memories (2nd) = %d, want 1 (idempotent upsert, no duplicate)", len(mems))
	}
}

// TestRecordDogfoodNilGuard proves a nil memory store is a no-op that never
// panics (0, nil) — unwired paths keep their zero behavior change.
func TestRecordDogfoodNilGuard(t *testing.T) {
	records := []domain.DogfoodRecord{
		{Gate: "check", Signature: "architecture", Failed: true, At: time.Now()},
	}
	n, err := RecordDogfood(records, nil, DefaultDogfoodMinFailures)
	if err != nil {
		t.Fatalf("nil mem returned error: %v", err)
	}
	if n != 0 {
		t.Fatalf("nil mem written = %d, want 0", n)
	}
}

// TestPriorDogfoodFailureHelpers proves the pass-site guard helpers: a store
// with a prior failure reports it through HasPriorDogfoodFailure and lists it
// in PriorDogfoodFailures, and a nil store is a no-op.
func TestPriorDogfoodFailureHelpers(t *testing.T) {
	root := t.TempDir()
	mem := memory.NewMemoryStore(root)
	if HasPriorDogfoodFailure(mem, "check", "architecture") {
		t.Fatal("fresh store reported a prior failure")
	}
	if prior := PriorDogfoodFailures(mem); len(prior) != 0 {
		t.Fatalf("fresh store prior failures = %v, want none", prior)
	}
	records := []domain.DogfoodRecord{
		{Gate: "check", Signature: "architecture", Failed: true, At: time.Now()},
		{Gate: "doctor", Signature: "arch-drift:internal/web:loc-over-cap", Failed: true, At: time.Now()},
	}
	if _, err := RecordDogfood(records, mem, DefaultDogfoodMinFailures); err != nil {
		t.Fatalf("RecordDogfood: %v", err)
	}
	if !HasPriorDogfoodFailure(mem, "check", "architecture") {
		t.Error("HasPriorDogfoodFailure(check, architecture) = false, want true")
	}
	if HasPriorDogfoodFailure(mem, "check", "secrets") {
		t.Error("HasPriorDogfoodFailure(check, secrets) = true, want false")
	}
	if HasPriorDogfoodFailure(mem, "doctor", "arch-drift") {
		t.Error("HasPriorDogfoodFailure(doctor, arch-drift) = true, want false (signature mismatch)")
	}
	prior := PriorDogfoodFailures(mem)
	if len(prior) != 2 {
		t.Fatalf("PriorDogfoodFailures = %d, want 2", len(prior))
	}
	if prior[0].Gate != "check" || prior[0].Signature != "architecture" {
		t.Errorf("prior[0] = %+v, want check/architecture (sorted first)", prior[0])
	}
	if prior[1].Gate != "doctor" || prior[1].Signature != "arch-drift:internal/web:loc-over-cap" {
		t.Errorf("prior[1] = %+v, want doctor/arch-drift:internal/web:loc-over-cap", prior[1])
	}
	// Nil-guard.
	if HasPriorDogfoodFailure(nil, "check", "architecture") {
		t.Error("HasPriorDogfoodFailure(nil) = true, want false")
	}
	if prior := PriorDogfoodFailures(nil); len(prior) != 0 {
		t.Errorf("PriorDogfoodFailures(nil) = %v, want none", prior)
	}
}
