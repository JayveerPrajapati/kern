package langparse

import (
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/asttypes"
)

// javaTestFFile analyzes src with the Java langSpec and returns the stripped
// file representation used by the regex extractor.
func javaTestFFile(t *testing.T, src string) *ffile {
	t.Helper()
	spec := specs["java"]
	if spec == nil {
		t.Fatal("no java langSpec registered")
	}
	return Analyze([]byte(src), spec)
}

// javaTestSigLine returns the line index of the method whose signature
// contains marker, or -1.
func javaTestSigLine(t *testing.T, f *ffile, marker string) int {
	t.Helper()
	for i, ln := range f.lines {
		if strings.Contains(ln, marker) {
			return i
		}
	}
	return -1
}

// TestJavaLocalTypeTracking: collectJavaLocalTypes must map every explicitly
// declared local to its simple type name — method parameters (plain, generic,
// final, array, dotted), local declarations with/without initializer, enhanced
// for loop variables, try-with-resources headers and dotted (fully-qualified)
// types. Primitive-typed params and the `var` inference keyword must NOT be
// tracked.
func TestJavaLocalTypeTracking(t *testing.T) {
	src := `package sample;

public class Sample {
    public void run(Foo param, List<String> genericParam, final Bar finalParam, String[] arrParam, int... rest, com.example.Deep deepParam) {
        String name = "x";
        Foo x = new Foo();
        List<String> items = new ArrayList<>();
        Helper h;
        final Baz locked = new Baz();
        Helper[] many = new Helper[3];
        com.example.Deep deep = new com.example.Deep();
        for (Thing t : things) {
            use(t);
        }
        try (Stream<String> stream = open()) {
            stream.count();
        }
    }
}`
	f := javaTestFFile(t, src)
	sigLine := javaTestSigLine(t, f, "void run(")
	if sigLine < 0 {
		t.Fatal("run method not found in fixture")
	}
	lt := collectJavaLocalTypes(f, sigLine, bodyEndFor(sigLine, f, specs["java"]), specs["java"])
	want := map[string]string{
		"param":        "Foo",
		"genericParam": "List",
		"finalParam":   "Bar",
		"arrParam":     "String",
		"deepParam":    "Deep",
		"name":         "String",
		"x":            "Foo",
		"items":        "List",
		"h":            "Helper",
		"locked":       "Baz",
		"many":         "Helper",
		"deep":         "Deep",
		"t":            "Thing",
		"stream":       "Stream",
	}
	for name, typ := range want {
		if got := lt[name]; got != typ {
			t.Errorf("lt[%s] = %q; want %q", name, got, typ)
		}
	}
	// `rest` is a primitive varargs param (int) — must not be tracked.
	if _, ok := lt["rest"]; ok {
		t.Errorf("lt[rest] = %q; want untracked (primitive varargs param)", lt["rest"])
	}
	if len(lt) != len(want) {
		t.Errorf("lt has %d entries; want exactly %d: %v", len(lt), len(want), lt)
	}
}

// TestJavaCalleeResolution: a method calling on a locally-declared receiver
// must record the type-qualified callee (Foo.bar) instead of the bare
// receiver form (x.bar).
func TestJavaCalleeResolution(t *testing.T) {
	src := `class Sample {
    void run() {
        Foo x = new Foo();
        x.bar();
        x.baz(1, 2);
    }
}`
	_, calls, _, _, err := ExtractForeign("Sample.java", []byte(src), "java", nil)
	if err != nil {
		t.Fatal(err)
	}
	got := calls["Sample.run"]
	for _, want := range []string{"Foo.bar", "Foo.baz"} {
		if !contains(asttypes.CallEdgeTargets(got), want) {
			t.Errorf("calls[Sample.run] = %v; want it to contain %s (resolved)", got, want)
		}
	}
	if contains(asttypes.CallEdgeTargets(got), "x.bar") || contains(asttypes.CallEdgeTargets(got), "x.baz") {
		t.Errorf("calls[Sample.run] = %v; must not contain unresolved x.bar/x.baz", got)
	}
}

// TestJavaCrossFileCallEdge: the whole point of "resolved". Two Java files —
// App.run calls h.doThing() on a local Helper h; the index must record the
// type-qualified edge Helper.doThing and reverse it into callers, binding the
// call across files exactly like Go's resolveCallee does.

// contains reports whether the slice contains the value.
func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}
