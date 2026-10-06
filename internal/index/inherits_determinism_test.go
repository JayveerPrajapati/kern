package index

import (
	"slices"
	"testing"
)

// TestInheritsEdgesDeterministicMultiInterface pins finding A1: the
// structural interface-implementation matcher (the finalize tail of
// computeCallers) appends "implements:" edges to ix.Inherits[concrete] in
// ifaceDir/concreteDir MAP-ITERATION order, which is nondeterministic per
// build — the same flakiness class the Client.roundTrip fix (Phase 3) and the
// InheritedBy reverse map addressed. The reverse map was dedupeSorted but the
// forward Inherits values never were, so a concrete type implementing >=2
// interfaces in its own package recorded its edge set in arbitrary order. The
// fix dedupeSorts the Inherits values in the same finalize pass; this fixture
// pins the exact sorted set AND that two independent builds agree on it. The
// fixture uses THREE interfaces declared in a deliberately non-alphabetical
// order (Pet, Farm, Wild) so the canary is deterministic: every rotation a
// 3-entry map can iterate is out of order, whereas the original 2-interface
// fixture only failed ~50% of runs without the fix.
func TestInheritsEdgesDeterministicMultiInterface(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"animals.go": `package animals

// Pet, Farm and Wild are same-package interfaces; Cat implements all three,
// so the structural matcher appends three "implements:" edges to
// Inherits["Cat"]. The declaration order (Pet, Farm, Wild) is deliberately
// NON-alphabetical (sorted: Farm, Pet, Wild): ifaceDir is populated in
// declaration order, and a 3-entry single-bucket map iterates as a ROTATION
// of insertion order — no rotation of (Pet, Farm, Wild) is sorted, so an
// unfixed engine deterministically records an out-of-order edge comparison
// on every run (the old 2-interface fixture failed only ~50% of runs,
// depending on which of the two rotations the map produced).
type Pet interface {
	Cuddle()
}

type Farm interface {
	Graze()
}

type Wild interface {
	Roar()
}

type Cat struct{}

func (Cat) Cuddle() {}
func (Cat) Graze()   {}
func (Cat) Roar()    {}
`,
	})
	a, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Inherits["Cat"]) == 0 {
		t.Fatal("fixture produced no implements: edges — the structural matcher must tag Cat")
	}
	want := []string{"implements:Farm", "implements:Pet", "implements:Wild"}
	if got := a.Inherits["Cat"]; !slices.Equal(got, want) {
		t.Fatalf("Inherits[Cat] = %v, want %v (sorted, deduped — finding A1)", got, want)
	}
	// Two independent builds must agree on the edge set: map-iteration order
	// must not leak into the persisted index.
	if !slices.Equal(a.Inherits["Cat"], b.Inherits["Cat"]) {
		t.Fatalf("Inherits[Cat] diverges across builds: %v vs %v", a.Inherits["Cat"], b.Inherits["Cat"])
	}
	// The reverse map agrees and stays sorted too.
	if got := a.InheritedBy["Pet"]; !slices.Equal(got, []string{"Cat"}) {
		t.Fatalf("InheritedBy[Pet] = %v, want [Cat]", got)
	}
}
