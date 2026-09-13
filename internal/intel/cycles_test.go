package intel

import (
	"strings"
	"testing"
)

// TestImportCyclesDetectsCrossPackageCycle: a -> b -> a is one cycle with
// per-edge file evidence.
func TestImportCyclesDetectsCrossPackageCycle(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"a/a.go": "package a\n\nimport \"demo/b\"\n\nfunc A() { b.B() }\n",
		"b/b.go": "package b\n\nimport \"demo/a\"\n\nfunc B() { a.A() }\n",
	})
	ix := buildIndex(t, dir)
	cycles := ImportCycles(ix)
	if len(cycles) != 1 {
		t.Fatalf("expected 1 cycle, got %d: %+v", len(cycles), cycles)
	}
	c := cycles[0]
	if len(c.Packages) != 2 {
		t.Fatalf("cycle packages = %v, want [a b]", c.Packages)
	}
	if c.Packages[0] != "a" || c.Packages[1] != "b" {
		t.Errorf("cycle packages = %v, want [a b]", c.Packages)
	}
	if len(c.Edges) != 2 {
		t.Fatalf("cycle edges = %+v, want 2", c.Edges)
	}
	if c.Edges[0].File == "" {
		t.Errorf("edge missing file evidence: %+v", c.Edges)
	}
}

// TestImportCyclesSelfImport: a package importing itself is a single-package
// cycle.
func TestImportCyclesSelfImport(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"x/x.go": "package x\n\nimport \"demo/x\"\n\nfunc X() {}\n",
	})
	ix := buildIndex(t, dir)
	cycles := ImportCycles(ix)
	if len(cycles) != 1 {
		t.Fatalf("expected 1 self-loop cycle, got %d: %+v", len(cycles), cycles)
	}
	if len(cycles[0].Packages) != 1 || cycles[0].Packages[0] != "x" {
		t.Errorf("self-loop packages = %v, want [x]", cycles[0].Packages)
	}
}

// TestImportCyclesAcyclic: a clean dependency chain reports no cycles.
func TestImportCyclesAcyclic(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"lib/lib.go": "package lib\n\nfunc Public() {}\n",
		"client/client.go": "package client\n\nimport \"demo/lib\"\n\nfunc Use() { lib.Public() }\n",
	})
	ix := buildIndex(t, dir)
	if cycles := ImportCycles(ix); len(cycles) != 0 {
		t.Fatalf("expected no cycles, got %+v", cycles)
	}
}

// TestRenderCycles: the report names the cycle members and evidence files.
func TestRenderCycles(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"a/a.go": "package a\n\nimport \"demo/b\"\n\nfunc A() { b.B() }\n",
		"b/b.go": "package b\n\nimport \"demo/a\"\n\nfunc B() { a.A() }\n",
	})
	ix := buildIndex(t, dir)
	out := RenderCycles(ImportCycles(ix))
	if !strings.Contains(out, "1 import cycle(s)") {
		t.Errorf("render missing count:\n%s", out)
	}
	if !strings.Contains(out, "a -> b") {
		t.Errorf("render missing cycle members:\n%s", out)
	}
	if !strings.Contains(out, "a/a.go") {
		t.Errorf("render missing evidence file:\n%s", out)
	}
}

// TestImportCyclesIgnoresTestFiles: imports that exist only in _test.go
// files are not part of the production package DAG and must not fabricate
// cycles (the config -> agents/context test-only edge case).
func TestImportCyclesIgnoresTestFiles(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"lib/lib.go":  "package lib\n\nfunc Public() {}\n",
		"app/app.go":  "package app\n\nimport \"demo/lib\"\n\nfunc Use() { lib.Public() }\n",
		"app/app_test.go": "package app\n\nimport \"demo/lib\"\n\nfunc TestX() {}\n",
		"lib/lib_test.go": "package lib\n\nimport \"demo/app\"\n\nfunc TestY() {}\n",
	})
	ix := buildIndex(t, dir)
	if cycles := ImportCycles(ix); len(cycles) != 0 {
		t.Fatalf("test-only imports must not create cycles: %+v", cycles)
	}
}

// TestImportCyclesLongestMatch: an import binds to the most specific local
// package — ".../blueprint/service" must not create an edge to the parent
// "blueprint" directory.
func TestImportCyclesLongestMatch(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"blueprint/root.go":    "package blueprint\n\nfunc R() {}\n",
		"blueprint/service/s.go": "package service\n\nfunc S() {}\n",
		"blueprint/service/s_test.go": "package service\n\nimport \"demo/blueprint\"\n\nfunc TestS() {}\n",
		"app/app.go": "package app\n\nimport \"demo/blueprint/service\"\n\nfunc Use() { service.S() }\n",
		"blueprint/root_imp.go": "package blueprint\n\nimport \"demo/blueprint/service\"\n\nfunc R2() { service.S() }\n",
	})
	ix := buildIndex(t, dir)
	g, _ := packageImportGraph(ix)
	// app and blueprint both import service; nothing imports blueprint as a
	// target, so the graph must be acyclic.
	if containsStr2(g["app"], "blueprint") {
		t.Errorf("app bound to parent dir blueprint instead of blueprint/service: %v", g["app"])
	}
	if cycles := ImportCycles(ix); len(cycles) != 0 {
		t.Fatalf("expected no cycles, got %+v", cycles)
	}
}

func containsStr2(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

// TestImportCycleWarnings: the guard gate warns only when a changed file's
// package is inside a cycle.
func TestImportCycleWarnings(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"a/a.go": "package a\n\nimport \"demo/b\"\n\nfunc A() { b.B() }\n",
		"b/b.go": "package b\n\nimport \"demo/a\"\n\nfunc B() { a.A() }\n",
		"c/c.go": "package c\n\nfunc C() {}\n",
	})
	ix := buildIndex(t, dir)

	inCycle := ImportCycleWarnings(ix, []string{"a/a.go"})
	if len(inCycle) != 1 {
		t.Fatalf("changed file in cycle should warn, got %v", inCycle)
	}
	if !strings.Contains(inCycle[0], "import cycle") {
		t.Errorf("warning malformed: %q", inCycle[0])
	}
	untouched := ImportCycleWarnings(ix, []string{"c/c.go"})
	if len(untouched) != 0 {
		t.Errorf("unrelated file should not warn: %v", untouched)
	}
	if got := ImportCycleWarnings(ix, nil); got != nil {
		t.Errorf("no files should warn: %v", got)
	}
}