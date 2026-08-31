//go:build treesitter

package index

import (
	"testing"
)

// TestTreeSitterJavaCalleeResolution mirrors the regex path's
// TestJavaCalleeResolution for the tree-sitter extractor: a method calling on
// a locally-declared receiver must record the type-qualified callee (Foo.bar)
// instead of the bare receiver form (x.bar), so cross-file edges bind.
func TestTreeSitterJavaCalleeResolution(t *testing.T) {
	src := `class Sample {
void run() {
Foo x = new Foo();
x.bar();
x.baz(1, 2);
}
}`
	_, calls, _, _, err := tsExtract("Sample.java", []byte(src), "java")
	if err != nil {
		t.Fatal(err)
	}
	got := calls["Sample.run"]
	for _, want := range []string{"Foo.bar", "Foo.baz"} {
		if !contains(got, want) {
			t.Errorf("calls[Sample.run] = %v; want it to contain %s (resolved)", got, want)
		}
	}
	if contains(got, "x.bar") || contains(got, "x.baz") {
		t.Errorf("calls[Sample.run] = %v; must not contain unresolved x.bar/x.baz", got)
	}
}

// TestTreeSitterJavaCalleeUnresolvedReceiver: a receiver whose type cannot be
// known from an explicit local declaration stays unresolved — nothing is
// fabricated (mirrors resolveJavaCallee's fallback).
func TestTreeSitterJavaCalleeUnresolvedReceiver(t *testing.T) {
	src := `class Sample {
void run(Unknown y) {
y.bar();
}
}`
	_, calls, _, _, err := tsExtract("Sample.java", []byte(src), "java")
	if err != nil {
		t.Fatal(err)
	}
	got := calls["Sample.run"]
	if !contains(got, "Unknown.bar") && !contains(got, "y.bar") {
		t.Errorf("calls[Sample.run] = %v; want either Unknown.bar or the bare y.bar", got)
	}
}

// TestTreeSitterJavaCrossFileCallEdge: two Java files — App.run calls
// h.doThing() on a local Helper h; the tree-sitter index must record the
// type-qualified edge Helper.doThing and bind it across files (mirrors
// TestJavaCrossFileCallEdge on the regex path).
func TestTreeSitterJavaCrossFileCallEdge(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"app/App.java": `package app;
public class App {
public void run() {
Helper h = new Helper();
h.doThing();
}
}
`,
		"app/Helper.java": `package app;
public class Helper {
public void doThing() {}
}
`,
	})
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(ix.Calls["App.run"], "Helper.doThing") {
		t.Errorf("Calls[App.run] = %v; want it to contain Helper.doThing (resolved cross-file edge)", ix.Calls["App.run"])
	}
	if !contains(ix.Callers["Helper.doThing"], "App.run") {
		t.Errorf("Callers[Helper.doThing] = %v; want App.run", ix.Callers["Helper.doThing"])
	}
}
