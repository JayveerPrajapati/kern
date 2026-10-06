package intel

import (
	"strings"
	"testing"
)

// sameNameRepo builds the V1 graph-intelligence shape at the explore level:
// package-level funcs named "Shared" in packages a and b. b.Shared (the
// wrapper) calls a.Shared (the core) with a package qualifier; c.Caller also
// calls the core cross-package; each package's test calls its own Shared by
// bare name. Package a also declares a unique "Unique" and a local
// "Println" (shadowed call-wise by a foreign fmt.Println use in package b).
func sameNameRepo(t *testing.T) string {
	t.Helper()
	return writeTree(t, map[string]string{
		"go.mod": "module example.com/mod\n\ngo 1.22\n",
		"a/auth.go": `package a

// Shared is the "core".
func Shared() string { return helper() }

func helper() string { return "a" }

// Unique has no name twin anywhere in the index.
func Unique() string { return "u" }

// Println is a local symbol; nobody calls it.
func Println(s string) {}
`,
		"a/auth_test.go": `package a

func TestCallsCore() { _ = Shared() }

func TestCallsUnique() { _ = Unique() }
`,
		"b/b.go": `package b

import (
	"fmt"

	"example.com/mod/a"
)

// Shared is the "wrapper": it calls the core with a package qualifier, its
// own same-package helper, and a foreign fmt call.
func Shared() string { return a.Shared() + bHelper() + fmt.Sprintf("%d", 1) + fmt.Sprint(a.Unique()) }

func bHelper() string { return "b" }

func Emit() { fmt.Println("x") }
`,
		"b/b_test.go": `package b

func TestCallsWrapper() { _ = Shared() }
`,
		"c/c.go": `package c

import "example.com/mod/a"

// Caller reaches the core from a third package via the package qualifier.
func Caller() string { return a.Shared() }

// UsesUnique is the cross-package qualified caller of the unique symbol.
func UsesUnique() string { return a.Unique() }
`,
	})
}

// TestExploreSameNameFuncsDoNotMerge pins the V1 fix end to end: exploring
// the shared simple name resolves to the core, whose callers are its
// same-package bare callers plus the qualified cross-package callers — never
// the twin's bare-name callers, never itself. The twin's callees (bHelper,
// Sprintf) must not leak into the core's callee list.
func TestExploreSameNameFuncsDoNotMerge(t *testing.T) {
	dir := sameNameRepo(t)
	ix := buildIndex(t, dir)

	rep, err := Explore(ix, "Shared", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(rep.Definition.File, "a/auth.go") {
		t.Fatalf("explore Shared resolved to %s; want the a/auth.go core", rep.Definition.File)
	}
	// (a) callers do not merge; no self-caller; no cross-package bare edge.
	for _, want := range []string{"Caller", "Shared", "TestCallsCore"} {
		if !contains(rep.Callers, want) {
			t.Errorf("callers missing %q: %v", want, rep.Callers)
		}
	}
	for _, banned := range []string{"TestCallsWrapper"} {
		if contains(rep.Callers, banned) {
			t.Errorf("callers must not include %q (different package's bare caller): %v", banned, rep.Callers)
		}
	}
	// The wrapper caller must render at the wrapper's own definition (rule 3),
	// never at the core (which would look like a self-caller).
	if loc := rep.CallerLocs["Shared"]; !strings.Contains(loc, "b/b.go") {
		t.Errorf("wrapper caller location = %q; want b/b.go (not a self-caller)", loc)
	}
	// (c) qualified same-package target still attributed: Caller is present
	// above; its location must resolve to the c package.
	if loc := rep.CallerLocs["Caller"]; !strings.Contains(loc, "c/c.go") {
		t.Errorf("qualified caller location = %q; want c/c.go", loc)
	}
	// Callee side: the wrapper's callees must not leak into the core.
	if !contains(rep.Callees, "helper") {
		t.Errorf("callees missing helper: %v", rep.Callees)
	}
	for _, banned := range []string{"bHelper", "Sprintf", "Shared"} {
		if contains(rep.Callees, banned) {
			t.Errorf("callees must not include %q (same-named twin's callee): %v", banned, rep.Callees)
		}
	}
}

// TestExploreForeignQualifiedTargetNotAttributed pins rule (b): a local
// symbol whose name is also a foreign qualified target ("Println" vs
// "fmt.Println") never inherits the foreign target's callers.
func TestExploreForeignQualifiedTargetNotAttributed(t *testing.T) {
	dir := sameNameRepo(t)
	ix := buildIndex(t, dir)

	rep, err := Explore(ix, "Println", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Callers) != 0 {
		t.Errorf("explore Println callers = %v; foreign fmt.Println callers must not be attributed", rep.Callers)
	}
}

// TestExploreUnambiguousKeepsBareEdges pins rule (d): a symbol whose simple
// name is unique keeps every bare and qualified caller — same-package bare
// test callers and cross-package qualified callers alike.
func TestExploreUnambiguousKeepsBareEdges(t *testing.T) {
	dir := sameNameRepo(t)
	ix := buildIndex(t, dir)

	rep, err := Explore(ix, "Unique", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"TestCallsUnique", "UsesUnique"} {
		if !contains(rep.Callers, want) {
			t.Errorf("explore Unique callers missing %q: %v", want, rep.Callers)
		}
	}
	if loc := rep.CallerLocs["UsesUnique"]; !strings.Contains(loc, "c/c.go") {
		t.Errorf("UsesUnique caller location = %q; want c/c.go", loc)
	}
}
