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
func TestGuardStrictSkipsHeuristicEdges(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"core/service.ts": `export function service(): void {
}
`,
		"api/handler.ts": `import { service } from "../core/service";

export function handler(): void {
	service();
}
`,
	})
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	b := &Boundaries{Rules: []BoundaryRule{{From: "api", To: "core", Action: "forbid"}}}
	files := []string{"api/handler.ts"}

	// Default precision: the heuristic cross-file call edge is trusted and
	// violates the boundary.
	if v := CheckBoundaries(ix, b, files); len(v) == 0 {
		t.Fatal("default mode: expected a violation for api -> core call, got none")
	}

	// Strict precision: the caller's language (typescript) is not
	// "resolved"-precision, so the edge is skipped entirely.
	violations, skipped := CheckBoundariesPrecise(ix, b, files, true)
	if len(violations) != 0 {
		t.Fatalf("strict mode: expected no violations (edge skipped), got %+v", violations)
	}
	if got := skipped["typescript"]; got != 1 {
		t.Errorf("strict mode: skipped[typescript] = %d; want 1", got)
	}
}

// TestImpactStrictSkipsHeuristicEdges: blast radius includes foreign-language
// callers in default mode and excludes them in strict mode, reporting how many
// heuristic edges were skipped.
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
func TestGuardJavaResolvedEdgeSurvivesStrict(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"core/Helper.java": `package core;

public class Helper {
    public void doThing() {}
}
`,
		"api/App.java": `package api;

import core.Helper;

public class App {
    public void run() {
        Helper h = new Helper();
        h.doThing();
    }
}
`,
	})
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	if p := ix.PrecisionByLang["java"]; p != "resolved" {
		t.Fatalf("PrecisionByLang[java] = %q; want resolved", p)
	}
	// Sanity: the index bound the call across files by type name.
	if !sliceContains(ix.Calls["App.run"], "Helper.doThing") {
		t.Fatalf("Calls[App.run] = %v; want resolved Helper.doThing edge", ix.Calls["App.run"])
	}
	b := &Boundaries{Rules: []BoundaryRule{{From: "api", To: "core", Action: "forbid"}}}
	files := []string{"api/App.java"}
	// Default precision trusts the edge and reports the violation.
	if v := CheckBoundaries(ix, b, files); len(v) == 0 {
		t.Fatal("default mode: expected a violation for api -> core Java call, got none")
	}
	// Strict precision must ALSO report it: Java edges are resolved, so they
	// are never skipped the way TypeScript heuristic edges are.
	violations, skipped := CheckBoundariesPrecise(ix, b, files, true)
	if len(violations) == 0 {
		t.Fatalf("strict mode: expected a violation for the resolved Java edge, got none (skipped=%v)", skipped)
	}
	if got := skipped["java"]; got != 0 {
		t.Errorf("strict mode: skipped[java] = %d; want 0 (resolved edges must not be skipped)", got)
	}
}
