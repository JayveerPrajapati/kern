package index

import (
	"slices"
	"testing"
)

// mergeSplitFixture builds a prev index exercising every splitPrevEdges
// branch in one shot:
//   - "Foo" is defined in exactly one file, so its call edges must be
//     bucketed under "a.go" — with the builtin "append" filtered (noise)
//     and the synthesized dispatch edge "Impl.M" subtracted. The genuine
//     interface call "Svc.M" (the pre-synthesis source form) MUST survive.
//     dispatchEdgeSet derives the virtual pair "Foo->Impl.M" from
//     InheritedBy[Svc]={Impl} plus the Impl.M symbol.
//   - "Both" is defined in two files (c.go and d.go), so its edges must
//     join the unattributable keep-verbatim list, never a single-file bucket.
//   - "Sub" (single-file subtype) must bucket under e.go; "Multi" (defined
//     in f.go and g.go) must join unattributableInherits; "Ghost" (no
//     symbol at all) must drop entirely.
func mergeSplitFixture(t *testing.T) *Index {
	t.Helper()
	prev := &Index{
		Symbols: []Symbol{
			{Kind: "func", Name: "Foo", File: "a.go"},
			{Kind: "func", Name: "Bar", File: "b.go"},
			{Kind: "func", Name: "Both", File: "c.go"},
			{Kind: "func", Name: "Both", File: "d.go"},
			{Kind: "type", Name: "Sub", File: "e.go"},
			{Kind: "type", Name: "Multi", File: "f.go"},
			{Kind: "type", Name: "Multi", File: "g.go"},
			{Kind: "method", Name: "M", Receiver: "Impl", File: "impl.go"},
		},
		Calls: map[string][]CallEdge{
			"Foo": {
				{Target: "helper", SourceFile: "a.go"},
				{Target: "append", SourceFile: "a.go"},
				{Target: "Svc.M", SourceFile: "a.go"},
				{Target: "Impl.M", SourceFile: "a.go", Synth: "dispatch"},
			},
			"Both": {
				{Target: "x", SourceFile: ""},
			},
		},
		InheritedBy: map[string][]string{
			"Svc": {"Impl"},
		},
		Inherits: map[string][]string{
			"Sub":   {"extends:Base"},
			"Multi": {"extends:Base2"},
			"Ghost": {"extends:Base3"},
		},
	}
	return prev
}

// TestSplitPrevEdgesBucketsSingleFileCalls pins the core attribution: a
// caller defined in exactly one file sends its surviving edges to that
// file's bucket — after the builtin noise filter drops "append" and the
// dispatch subtraction drops the synthesized "Impl.M" edge. The genuine
// interface call "Svc.M" stays.
func TestSplitPrevEdgesBucketsSingleFileCalls(t *testing.T) {
	prev := mergeSplitFixture(t)
	_, _, _, callsByFile, _, _, _ := splitPrevEdges(prev)

	bucket := callsByFile["a.go"]["Foo"]
	if len(bucket) != 2 {
		t.Fatalf("Foo bucket = %+v, want exactly helper + Svc.M", bucket)
	}
	targets := map[string]bool{}
	for _, ce := range bucket {
		targets[ce.Target] = true
	}
	if !targets["helper"] {
		t.Fatal("helper edge missing from the Foo bucket")
	}
	if !targets["Svc.M"] {
		t.Fatal("genuine interface call Svc.M missing from the Foo bucket")
	}
	if targets["append"] {
		t.Fatal("builtin noise callee append survived the merge split")
	}
	if targets["Impl.M"] {
		t.Fatal("synthesized dispatch edge Impl.M survived the merge split")
	}
}

// TestSplitPrevEdgesDispatchSubtractionRecoversPristineCalls pins the
// dispatch subtraction through dispatchEdgeSet: the synthesized virtual edge
// "Foo -> Impl.M" (added by addDispatchEdges, Synth="dispatch") must not be
// copied into the pristine per-file bucket, because finalizeUpdate re-adds
// exactly the valid dispatch edges after the merge.
func TestSplitPrevEdgesDispatchSubtractionRecoversPristineCalls(t *testing.T) {
	prev := mergeSplitFixture(t)
	_, _, dispatch, callsByFile, _, _, _ := splitPrevEdges(prev)

	if !dispatch["Foo->Impl.M"] {
		t.Fatal("dispatchEdgeSet did not derive the virtual callee Foo->Impl.M")
	}
	bucket := callsByFile["a.go"]["Foo"]
	for _, ce := range bucket {
		if ce.Synth == "dispatch" || ce.Target == "Impl.M" {
			t.Fatalf("dispatch edge %q leaked into the pristine bucket", ce.Target)
		}
	}
}

// TestSplitPrevEdgesMultiDefinedOwnerGoesUnattributable pins the collision
// rule: an owner defined in more than one file must NEVER be attributed to a
// single bucket (a later file change would silently delete the other
// definers' edges) — it joins the keep-verbatim list instead.
func TestSplitPrevEdgesMultiDefinedOwnerGoesUnattributable(t *testing.T) {
	prev := mergeSplitFixture(t)
	_, _, _, _, _, unattributable, _ := splitPrevEdges(prev)

	if !slices.Contains(unattributable, "Both") {
		t.Fatalf("multi-defined owner Both not in unattributable: %v", unattributable)
	}
}

// TestSplitPrevEdgesSoleDefiningFileHelper pins the helper contract: exactly
// one definer resolves, zero and multiple definers resolve to "".
func TestSplitPrevEdgesSoleDefiningFileHelper(t *testing.T) {
	prev := mergeSplitFixture(t)
	_, sole, _, _, _, _, _ := splitPrevEdges(prev)

	if got := sole("Foo"); got != "a.go" {
		t.Fatalf("sole(Foo) = %q, want a.go", got)
	}
	if got := sole("Both"); got != "" {
		t.Fatalf("sole(Both) = %q, want \"\" (multi-defined)", got)
	}
	if got := sole("Ghost"); got != "" {
		t.Fatalf("sole(Ghost) = %q, want \"\" (no symbol)", got)
	}
}

// TestSplitPrevEdgesInheritsBucketing pins the inherits split: a
// single-file subtype buckets under its file; a multi-defined subtype joins
// unattributableInherits; a zero-file subtype drops (nothing to re-attach
// it to).
func TestSplitPrevEdgesInheritsBucketing(t *testing.T) {
	prev := mergeSplitFixture(t)
	_, _, _, _, inheritsByFile, _, unattributableInherits := splitPrevEdges(prev)

	if got := inheritsByFile["e.go"]["Sub"]; len(got) != 1 || got[0] != "extends:Base" {
		t.Fatalf("inheritsByFile[e.go][Sub] = %v, want [extends:Base]", got)
	}
	if !slices.Contains(unattributableInherits, "Multi") {
		t.Fatalf("multi-defined subtype Multi not unattributable: %v", unattributableInherits)
	}
	if slices.Contains(unattributableInherits, "Ghost") {
		t.Fatal("zero-file subtype Ghost must drop, not join unattributableInherits")
	}
}
