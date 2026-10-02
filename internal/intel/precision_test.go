package intel

import (
	"reflect"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

func sliceContains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// TestGuardStrictSkipsHeuristicEdges: a foreign-language (TypeScript) call
// edge that crosses a forbidden boundary is reported in default precision mode
// but skipped in strict mode, where non-"resolved" edges are unknown rather
// than trusted — so they can never fabricate a violation.
func TestImpactStrictSkipsHeuristicEdges(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"svc/svc.go": `package svc

func Target() {}
`,
		"web/caller.ts": `import { Target } from "../svc/svc";

export function caller(): void {
	Target();
}
`,
	})
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Sanity: the index actually recorded the cross-language caller edge.
	if _, ok := ix.Callers["Target"]; !ok {
		t.Fatal("expected a recorded caller edge Target <- caller, got none")
	}

	defReach, _, _ := BlastRadiusPrecise(ix, []string{"Target"}, false)
	if !sliceContains(defReach, "caller") {
		t.Errorf("default mode: blast radius %v should include caller", defReach)
	}

	strictReach, _, skipped := BlastRadiusPrecise(ix, []string{"Target"}, true)
	if sliceContains(strictReach, "caller") {
		t.Errorf("strict mode: blast radius %v must exclude caller", strictReach)
	}
	if skipped != 1 {
		t.Errorf("strict mode: skipped = %d; want 1", skipped)
	}
}

// TestStrictModeGoStillResolved: on a Go-only index, strict mode produces
// results identical to default mode — Go edges are "resolved", so strict mode
// never skips them.
func TestStrictModeGoStillResolved(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"lib/lib.go": `package lib

func Public() {}
`,
		"client/client.go": `package client

import "lib"

func Caller() {
	lib.Public()
}
`,
	})
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	root := "lib.Public"
	defReach, defDist, _ := BlastRadiusPrecise(ix, []string{root}, false)
	strictReach, strictDist, skipped := BlastRadiusPrecise(ix, []string{root}, true)
	if skipped != 0 {
		t.Errorf("strict mode on Go-only index: skipped = %d; want 0", skipped)
	}
	if !reflect.DeepEqual(defReach, strictReach) || !reflect.DeepEqual(defDist, strictDist) {
		t.Errorf("strict mode changed Go blast radius:\ndefault  reach=%v dist=%v\nstrict   reach=%v dist=%v",
			defReach, defDist, strictReach, strictDist)
	}
}

// TestGuardJavaResolvedEdgeSurvivesStrict: Java now has "resolved" precision
// (local-type tracking + callee resolution in the regex build), so a
// cross-file Java call edge crossing a forbidden boundary is reported in
// strict mode too — the edge is a real, type-qualified binding (h.doThing ->
// Helper.doThing), not a heuristic guess that strict mode must distrust. This
// is the tier's value: the "ast" tier skipped Java edges under strict
// precision, the "resolved" tier trusts them.
