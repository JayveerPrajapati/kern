package index

import (
	"testing"

	"github.com/JayveerPrajapati/kern/internal/langparse"
)

func TestJavaCrossFileCallEdge(t *testing.T) {
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
	if !contains(CallEdgeTargets(ix.Calls["App.run"]), "Helper.doThing") {
		t.Errorf("Calls[App.run] = %v; want it to contain Helper.doThing (resolved cross-file edge)", ix.Calls["App.run"])
	}
	if !contains(ix.Callers["Helper.doThing"], "App.run") {
		t.Errorf("Callers[Helper.doThing] = %v; want App.run", ix.Callers["Helper.doThing"])
	}
}

// TestJavaUnresolvedCallee: calls whose receiver type cannot be known from an
// explicit declaration are left as-is — a `var`-declared receiver and a
// receiver produced by a call chain. No Type.method form may be fabricated.
// (A param with a declared interface type DOES resolve to that type's name,
// exactly as Go's collectLocalTypes resolves interface-typed params.)
func TestJavaUnresolvedCallee(t *testing.T) {
	src := `class Unresolved {
    void run() {
        var thing = new Helper();
        thing.doThing();
        getHelper().doThing();
    }
}`
	_, calls, _, _, err := langparse.ExtractForeign("Unresolved.java", []byte(src), "java", tsExtract)
	if err != nil {
		t.Fatal(err)
	}
	got := calls["Unresolved.run"]
	if contains(CallEdgeTargets(got), "Helper.doThing") {
		t.Errorf("calls[Unresolved.run] = %v; var-declared receiver must NOT resolve to Helper.doThing", got)
	}
	if !contains(CallEdgeTargets(got), "thing.doThing") {
		t.Errorf("calls[Unresolved.run] = %v; want the unresolved thing.doThing preserved as-is", got)
	}
	if !contains(CallEdgeTargets(got), "getHelper") || !contains(CallEdgeTargets(got), "doThing") {
		t.Errorf("calls[Unresolved.run] = %v; chained call must stay bare (getHelper / doThing)", got)
	}
}

// TestJavaPrecisionIsResolved: building a Java repo marks the language as
// "resolved" precision so kern guard / impact --precision strict trust its
// call edges.
func TestJavaPrecisionIsResolved(t *testing.T) {
	if treesitterEnabled() {
		t.Skip("tree-sitter build extracts Java calls without type qualification; precision stays ast")
	}
	dir := writeTree(t, map[string]string{
		"app/App.java": `package app;

public class App {
    public void run() {}
}
`,
	})
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := ix.PrecisionByLang["java"]; got != "resolved" {
		t.Errorf("PrecisionByLang[java] = %q; want resolved", got)
	}
}
