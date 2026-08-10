package index

import (
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

const helperSrc = `package lib

func Public() string {
	return inner()
}

func inner() string {
	return "x"
}

func Deep() {
	Public()
}
`

func TestDedupeSorted(t *testing.T) {
	cases := []struct {
		in   []string
		want []string
	}{
		{nil, nil},
		{[]string{"a"}, []string{"a"}},
		{[]string{"b", "a", "b", "a"}, []string{"a", "b"}},
		{[]string{"x", "x", "x"}, []string{"x"}},
		{[]string{}, []string{}},
	}
	for _, c := range cases {
		if got := dedupeSorted(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("dedupeSorted(%v) = %v; want %v", c.in, got, c.want)
		}
	}
}

func TestResolveName(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"lib/lib.go": helperSrc,
	})
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		want bool
	}{
		{"Public", true},
		{"inner", true},
		{"index.Build", false}, // package-qualified, not defined here
		{"definitelyMissing", false},
		{"", false},
	}
	for _, c := range cases {
		_, ok := resolveName(ix, c.name)
		if ok != c.want {
			t.Errorf("resolveName(%q) ok=%v; want %v", c.name, ok, c.want)
		}
	}
	// Package-qualified target falls back to the bare name.
	if s, ok := resolveName(ix, "lib.Public"); !ok || s.Name != "Public" {
		t.Errorf("resolveName(\"lib.Public\") = %+v, %v; want Public", s, ok)
	}
}

func TestQualifiedNameEntryPoints(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"lib/lib.go": helperSrc,
	})
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	// All entry points must resolve a package-qualified reference to the def.
	if g := ix.Graph("lib.Public"); !strings.Contains(g, "def ") || strings.Contains(g, "no symbol found") {
		t.Fatalf("Graph(lib.Public) failed:\n%s", g)
	}
	if m := ix.Mermaid("lib.Public"); !strings.Contains(m, "flowchart LR") {
		t.Fatalf("Mermaid(lib.Public) failed:\n%s", m)
	}
	if _, ok := ix.Neighborhood("lib.Public"); !ok {
		t.Fatal("Neighborhood(lib.Public) not found")
	}
	if c := ix.Context("lib.Public", 4); c == "" {
		t.Fatal("Context(lib.Public) empty")
	}
}

func TestEdgeConfidence(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"lib/lib.go": helperSrc,
	})
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := edgeConfidence(ix, "lib/lib.go", "Public"); got != "high" {
		t.Errorf("edgeConfidence(Public) = %q; want high", got)
	}
	if got := edgeConfidence(ix, "lib/lib.go", "NeverDefined"); got != "low" {
		t.Errorf("edgeConfidence(NeverDefined) = %q; want low", got)
	}
	if got := confidenceLabel("high"); got != "EXTRACTED" {
		t.Errorf("confidenceLabel(high) = %q; want EXTRACTED", got)
	}
	if got := confidenceLabel("medium"); got != "INFERRED" {
		t.Errorf("confidenceLabel(medium) = %q; want INFERRED", got)
	}
	if got := confidenceLabel("low"); got != "AMBIGUOUS" {
		t.Errorf("confidenceLabel(low) = %q; want AMBIGUOUS", got)
	}
	if got := confidenceLabel(""); got != "AMBIGUOUS" {
		t.Errorf("confidenceLabel(empty) = %q; want AMBIGUOUS", got)
	}
}

func TestEdgeConfidenceCrossPackage(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"lib/lib.go": helperSrc,
		"other/other.go": `package other
import "lib"
func Cross() {
	lib.Public()
}
`,
	})
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Cross-package resolved call should be "medium".
	if got := edgeConfidence(ix, "other/other.go", "lib.Public"); got != "medium" {
		t.Errorf("edgeConfidence(cross-pkg Public) = %q; want medium", got)
	}
	// Same-package reference still "high".
	if got := edgeConfidence(ix, "lib/lib.go", "Public"); got != "high" {
		t.Errorf("edgeConfidence(same-pkg Public) = %q; want high", got)
	}
}

func TestMergeNode(t *testing.T) {
	node := GraphNode{ID: "a", Role: "def"}
	got := mergeNode(node, GraphNode{ID: "x", Role: "callee"})
	if got.ID != "a" || got.Role != "def" {
		t.Errorf("mergeNode kept wrong node: %+v", got)
	}
	// Empty first arg returns the second.
	fromEmpty := mergeNode(GraphNode{}, GraphNode{ID: "y", Role: "def"})
	if fromEmpty.ID != "y" {
		t.Errorf("mergeNode(empty, y) = %+v; want y", fromEmpty)
	}
	// caller + callee stays caller.
	got2 := mergeNode(GraphNode{ID: "a", Role: "caller"}, GraphNode{ID: "b", Role: "callee"})
	if got2.Role != "caller" {
		t.Errorf("caller+callee should stay caller: %+v", got2)
	}
}

func TestSymbolRegex(t *testing.T) {
	re, kind := symbolRegex("func")
	if kind != "" {
		t.Errorf("bare pattern should have no kind, got %q", kind)
	}
	if !re.MatchString("func") {
		t.Error("bare pattern should match its literal name")
	}
	if re.MatchString("main") {
		t.Error("bare pattern should not be a kind keyword")
	}
	re, kind = symbolRegex("func Foo")
	if kind != "func" {
		t.Errorf("kind = %q; want func", kind)
	}
	if !re.MatchString("Foo") || re.MatchString("FooBar") {
		t.Error("func Foo should anchor exact match")
	}
	re, _ = symbolRegex("method *")
	if !re.MatchString("anything.withdots") {
		t.Error("wildcard pattern should match dotted names")
	}
	re, kind = symbolRegex("struct User")
	if kind != "struct" {
		t.Errorf("struct kind = %q", kind)
	}
	if !re.MatchString("User") {
		t.Error("struct User should match User")
	}
}

func TestSymbolMatches(t *testing.T) {
	s := Symbol{Kind: "method", Name: "Login", Receiver: "User", Entry: false, Route: ""}
	if !symbolMatches(s, "", regexp.MustCompile("^Login$")) {
		t.Error("name match should succeed")
	}
	if !symbolMatches(s, "", regexp.MustCompile("^User.Login$")) {
		t.Error("receiver-qualified match should succeed")
	}
	if symbolMatches(s, "func", regexp.MustCompile("^Login$")) {
		t.Error("kind mismatch should fail")
	}
	if symbolMatches(s, "method", regexp.MustCompile("^Logout$")) {
		t.Error("name mismatch should fail")
	}
	entry := Symbol{Kind: "func", Name: "Home", Entry: true, Route: "/"}
	if !symbolMatches(entry, "entry", regexp.MustCompile("^/$")) {
		t.Error("entry route match should succeed")
	}
	if !symbolMatches(entry, "entry", regexp.MustCompile("^Home$")) {
		t.Error("entry name match should succeed")
	}
	if !symbolMatches(entry, "", regexp.MustCompile("^Home$")) {
		t.Error("plain name should still match entry symbols")
	}
	if symbolMatches(entry, "entry", regexp.MustCompile("^NotFound$")) {
		t.Error("entry mismatch should fail")
	}
}

func TestXMLEsc(t *testing.T) {
	in := `a & b <tag> "q" 's'`
	want := `a &amp; b &lt;tag&gt; &quot;q&quot; &apos;s&apos;`
	if got := xmlEsc(in); got != want {
		t.Errorf("xmlEsc(%q) = %q; want %q", in, got, want)
	}
	if xmlEsc("plain") != "plain" {
		t.Error("non-special text should pass through")
	}
}

func TestKWSet(t *testing.T) {
	kw := kwSet("a", "b")
	if len(kw) != 2 || !kw["a"] || !kw["b"] {
		t.Errorf("kwSet = %v", kw)
	}
	if kwSet()["x"] {
		t.Error("kwSet() should not contain x")
	}
}

func TestLoadFileRoundTrip(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"lib/lib.go": helperSrc,
	})
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := ix.Save(); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadFile(StorePath(ix.Root))
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if loaded == nil || len(loaded.Symbols) < 2 {
		t.Errorf("loaded index should contain lib symbols, got %+v", loaded)
	}
	if loaded.Version != indexVersion {
		t.Errorf("loaded version = %d; want %d", loaded.Version, indexVersion)
	}

	// Missing file.
	if _, err := LoadFile(filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Error("expected error for missing file")
	}
}
