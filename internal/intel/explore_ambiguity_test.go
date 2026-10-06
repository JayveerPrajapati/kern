package intel

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/index"
)

func TestAmbiguousAlternativesListsOtherProductionDefinitions(t *testing.T) {
	ix := fuzzyQualifiedIndex()
	picked := ix.Symbols[0] // internal/bpcli/mcp/server.go:81

	got := ambiguousAlternatives(ix, "NewServer", picked)
	if len(got) != 1 || !strings.Contains(got[0], "internal/mcp/server.go:446") {
		t.Fatalf("want exactly the other production NewServer, got %v", got)
	}
	for _, g := range got {
		if strings.Contains(g, "_test.go") || strings.Contains(g, "TestNewServerHelper") {
			t.Errorf("test symbols must not be listed as alternatives: %v", got)
		}
	}
}

func TestAmbiguousAlternativesSkipsQualifiedAndUnambiguous(t *testing.T) {
	ix := fuzzyQualifiedIndex()
	picked := ix.Symbols[0]

	if got := ambiguousAlternatives(ix, "bpcli/mcp.NewServer", picked); got != nil {
		t.Errorf("qualified query names its target; got %v", got)
	}
	if got := ambiguousAlternatives(ix, "g5NewServer", ix.Symbols[3]); got != nil {
		t.Errorf("unambiguous name must have no alternatives; got %v", got)
	}
	if got := ambiguousAlternatives(nil, "NewServer", picked); got != nil {
		t.Errorf("nil index must be safe; got %v", got)
	}
}

func TestAmbiguousAlternativesCapped(t *testing.T) {
	ix := fuzzyQualifiedIndex()
	for i := 0; i < 10; i++ {
		ix.Symbols = append(ix.Symbols, sym("func", "Build", fmt.Sprintf("pkg%d/build.go", i), 10))
	}
	got := ambiguousAlternatives(ix, "Build", ix.Symbols[len(ix.Symbols)-1])
	if len(got) != maxAlternatives+1 || !strings.HasPrefix(got[len(got)-1], "+") {
		t.Fatalf("want %d entries plus a +N more marker, got %v", maxAlternatives, got)
	}
}

func fixtureVsProductionIndex() *index.Index {
	method := sym("method", "dispatch", "internal/mcp/server.go", 952)
	method.Receiver = "Server"
	return &index.Index{
		Root: "/kern",
		Symbols: []index.Symbol{
			// The free-function fixtures sort first and match the bare name exactly.
			sym("func", "dispatch", "internal/tasklife/testdata/resolve_prio/a/a.go", 5),
			sym("func", "dispatch", "internal/tasklife/testdata/resolve_prio/b/b.go", 5),
			sym("func", "dispatch", "pkg/fixtures/sample.go", 3),
			sym("func", "dispatch", "internal/x/x_test.go", 9),
			method,
		},
		UpdatedAt: time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC),
	}
}

func TestExplorePrefersProductionOverFixtureForBareName(t *testing.T) {
	ix := fixtureVsProductionIndex()
	rep, err := Explore(ix, "dispatch", 1, 5)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Resolved != "Server.dispatch" || rep.Definition.File != "internal/mcp/server.go" {
		t.Fatalf("bare name must resolve to the production method, got %q in %s", rep.Resolved, rep.Definition.File)
	}
	for _, a := range rep.Alternatives {
		if strings.Contains(a, "testdata") || strings.Contains(a, "fixtures") || strings.Contains(a, "_test.go") {
			t.Errorf("fixture/test symbols must not be listed as alternatives: %v", rep.Alternatives)
		}
	}
}

func TestExploreKeepsFixtureSymbolWhenNoProductionExists(t *testing.T) {
	ix := fixtureVsProductionIndex()
	ix.Symbols = ix.Symbols[:3] // fixtures only
	rep, err := Explore(ix, "dispatch", 1, 5)
	if err != nil {
		t.Fatal(err)
	}
	if !isFixtureFile(rep.Definition.File) {
		t.Fatalf("with no production symbol the fixture must still resolve, got %s", rep.Definition.File)
	}
}

func TestExploreHonorsPackageQualifier(t *testing.T) {
	ix := &index.Index{
		Root: "/kern",
		Symbols: []index.Symbol{
			sym("func", "checkRule", "internal/bpcli/check.go", 10),
			sym("func", "checkRule", "internal/verifycmd/rules.go", 20),
		},
		UpdatedAt: time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC),
	}
	for _, q := range []string{"verifycmd.checkRule", "internal/verifycmd.checkRule"} {
		rep, err := Explore(ix, q, 1, 5)
		if err != nil {
			t.Fatal(err)
		}
		if rep.Definition.File != "internal/verifycmd/rules.go" {
			t.Errorf("%s resolved to %s, want internal/verifycmd/rules.go", q, rep.Definition.File)
		}
	}
	rep, err := Explore(ix, "verifycmd.checkRule", 1, 5)
	if err != nil {
		t.Fatal(err)
	}
	if out := RenderExplore(rep); !strings.Contains(out, "(package-qualified match)") || strings.Contains(out, "fuzzy match") {
		t.Errorf("qualified query must not be labelled fuzzy:\n%s", out)
	}
	// A bare name keeps the first definition and still reports the other.
	rep, err = Explore(ix, "checkRule", 1, 5)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Definition.File != "internal/bpcli/check.go" || len(rep.Alternatives) != 1 {
		t.Errorf("bare name: got %s alts=%v", rep.Definition.File, rep.Alternatives)
	}
}

func TestIsFixtureFile(t *testing.T) {
	for file, want := range map[string]bool{
		"internal/tasklife/testdata/a/a.go": true,
		"testdata/x.go":                     true,
		"pkg/fixtures/sample.go":            true,
		"internal/mcp/server.go":            false,
		"internal/mytestdata/x.go":          false,
	} {
		if got := isFixtureFile(file); got != want {
			t.Errorf("isFixtureFile(%q) = %v, want %v", file, got, want)
		}
	}
}

func TestRenderExploreNotesAmbiguity(t *testing.T) {
	ix := fuzzyQualifiedIndex()
	r := &ExploreReport{
		Symbol:       "NewServer",
		Resolved:     "NewServer",
		Alternatives: ambiguousAlternatives(ix, "NewServer", ix.Symbols[0]),
		Definition:   ix.Symbols[0],
	}
	out := RenderExplore(r)
	if !strings.Contains(out, `note: "NewServer" is ambiguous`) || !strings.Contains(out, "internal/mcp/server.go:446") {
		t.Fatalf("render must surface the ambiguity and the alternative:\n%s", out)
	}
	r.Alternatives = nil
	if out := RenderExplore(r); strings.Contains(out, "ambiguous") {
		t.Fatalf("unambiguous report must not carry the note:\n%s", out)
	}
}
