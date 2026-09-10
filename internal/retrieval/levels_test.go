package retrieval

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

const testMainSrc = `package sample

func main() {
	helperA()
}

func helperA() {
	helperB()
	testHelper()
}

func helperB() string {
	return "b"
}
`

const testHelperSrc = `package sample

func testHelper() string {
	return "th"
}
`

// buildTestIndex writes a tiny two-file module (one production file, one
// *_test.go file) and builds an in-memory index over it. helperA calls both a
// production function (helperB) and a function defined in helper_test.go
// (testHelper), which exercises the L2 callers/callees/tests/files logic.
func buildTestIndex(t *testing.T) *index.Index {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"main.go":        testMainSrc,
		"helper_test.go": testHelperSrc,
	}
	for rel, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	return ix
}

func containsStr(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func TestRetrieveL1(t *testing.T) {
	ix := buildTestIndex(t)
	r, err := Retrieve(ix, Options{Level: L1, Query: "helperA"})
	if err != nil {
		t.Fatal(err)
	}
	if r.Level != L1 || len(r.Items) == 0 {
		t.Fatalf("L1 returned no items: %+v", r)
	}
	for _, it := range r.Items {
		if it.Handle == nil || it.Handle.ID == "" || it.Name == "" {
			t.Fatalf("item missing handle/name: %+v", it)
		}
		if it.TokenCost <= 0 {
			t.Fatalf("item TokenCost = %d, want > 0: %+v", it.TokenCost, it)
		}
	}
	// IDs are stable across two calls.
	r2, err := Retrieve(ix, Options{Level: L1, Query: "helperA"})
	if err != nil {
		t.Fatal(err)
	}
	if len(r2.Items) != len(r.Items) {
		t.Fatalf("item count changed between calls: %d vs %d", len(r2.Items), len(r.Items))
	}
	for i := range r.Items {
		if r.Items[i].Handle.ID != r2.Items[i].Handle.ID {
			t.Fatalf("handle ID unstable across calls: %q vs %q",
				r.Items[i].Handle.ID, r2.Items[i].Handle.ID)
		}
	}
	if r.Tokens <= 0 {
		t.Fatalf("Tokens = %d, want > 0", r.Tokens)
	}
}

func TestRetrieveL1RequiresQuery(t *testing.T) {
	ix := buildTestIndex(t)
	if _, err := Retrieve(ix, Options{Level: L1}); err == nil {
		t.Fatal("expected error for empty query")
	}
}

func TestRetrieveL2(t *testing.T) {
	ix := buildTestIndex(t)
	r, err := Retrieve(ix, Options{Level: L2, Symbol: "helperA"})
	if err != nil {
		t.Fatal(err)
	}
	if r.Level != L2 || r.Detail == nil {
		t.Fatalf("L2 result missing detail: %+v", r)
	}
	if !containsStr(r.Detail.Callers, "main") {
		t.Errorf("callers missing main: %v", r.Detail.Callers)
	}
	if !containsStr(r.Detail.Callees, "helperB") {
		t.Errorf("callees missing helperB: %v", r.Detail.Callees)
	}
	if !containsStr(r.Detail.Tests, "testHelper") {
		t.Errorf("tests missing testHelper: %v", r.Detail.Tests)
	}
	if len(r.Detail.Files) == 0 {
		t.Errorf("files empty: %v", r.Detail.Files)
	}
	if r.Detail.Handle == nil || r.Detail.Handle.Name != "helperA" {
		t.Fatalf("L2 handle wrong: %+v", r.Detail.Handle)
	}
	if r.Tokens <= 0 {
		t.Fatalf("Tokens = %d, want > 0", r.Tokens)
	}
}

func TestRetrieveL2UnknownSymbol(t *testing.T) {
	ix := buildTestIndex(t)
	if _, err := Retrieve(ix, Options{Level: L2, Symbol: "doesNotExist"}); err == nil {
		t.Fatal("expected error for unknown symbol")
	}
}

func TestRetrieveL3(t *testing.T) {
	ix := buildTestIndex(t)
	r, err := Retrieve(ix, Options{Level: L3, Symbol: "helperA"})
	if err != nil {
		t.Fatal(err)
	}
	if r.Level != L3 || r.Source == nil {
		t.Fatalf("L3 result missing source: %+v", r)
	}
	if !strings.Contains(r.Source.Text, "func helperA") {
		t.Errorf("source missing function definition:\n%s", r.Source.Text)
	}
	if r.Source.Handle == nil || r.Source.Handle.Name != "helperA" {
		t.Fatalf("L3 handle wrong: %+v", r.Source.Handle)
	}
	if r.Tokens <= 0 {
		t.Fatalf("Tokens = %d, want > 0", r.Tokens)
	}
}

func TestRetrieveL3Truncated(t *testing.T) {
	ix := buildTestIndex(t)
	r, err := Retrieve(ix, Options{Level: L3, Symbol: "helperA", MaxTokens: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !r.Truncated {
		t.Fatalf("expected Truncated=true with MaxTokens=1")
	}
	if r.Tokens <= 0 {
		t.Fatalf("Tokens = %d, want > 0", r.Tokens)
	}
}

func TestRetrieveL3UnknownSymbol(t *testing.T) {
	ix := buildTestIndex(t)
	if _, err := Retrieve(ix, Options{Level: L3, Symbol: "nope"}); err == nil {
		t.Fatal("expected error for unknown symbol")
	}
}

func TestRetrieveRequiresSymbol(t *testing.T) {
	ix := buildTestIndex(t)
	for _, lvl := range []Level{L2, L3} {
		if _, err := Retrieve(ix, Options{Level: lvl}); err == nil {
			t.Fatalf("level %d: expected error for empty symbol", lvl)
		}
	}
}

func TestRenderOutput(t *testing.T) {
	ix := buildTestIndex(t)
	for _, lvl := range []Level{L1, L2, L3} {
		opts := Options{Level: lvl}
		if lvl == L1 {
			opts.Query = "helperA"
		} else {
			opts.Symbol = "helperA"
		}
		r, err := Retrieve(ix, opts)
		if err != nil {
			t.Fatal(err)
		}
		rendered := Render(r)
		if rendered == "" {
			t.Fatalf("level %d: Render returned empty", lvl)
		}
		if !strings.Contains(rendered, "level") {
			t.Fatalf("level %d: render missing 'level':\n%s", lvl, rendered)
		}
	}
}

func TestRetrieveForTask(t *testing.T) {
	ix := buildTestIndex(t)
	// documentation → L1: search-based packet listing matches for the symbol.
	doc, err := RetrieveForTask(ix, "helperA", "documentation", 0)
	if err != nil {
		t.Fatalf("RetrieveForTask(documentation): %v", err)
	}
	if doc.Level != L1 {
		t.Errorf("documentation level = %d, want L1", doc.Level)
	}
	found := false
	for _, it := range doc.Items {
		if it.Name == "helperA" {
			found = true
		}
	}
	if !found {
		t.Errorf("documentation L1 items missing helperA: %+v", doc.Items)
	}
	// refactor → L3: verbatim source for the symbol.
	ref, err := RetrieveForTask(ix, "helperA", "refactor", 0)
	if err != nil {
		t.Fatalf("RetrieveForTask(refactor): %v", err)
	}
	if ref.Level != L3 {
		t.Errorf("refactor level = %d, want L3", ref.Level)
	}
	if ref.Source == nil || !strings.Contains(ref.Source.Text, "helperA") {
		t.Errorf("refactor L3 source missing helperA: %+v", ref.Source)
	}
	// fix_bug → L2: neighborhood packet.
	fix, err := RetrieveForTask(ix, "helperA", "fix_bug", 0)
	if err != nil {
		t.Fatalf("RetrieveForTask(fix_bug): %v", err)
	}
	if fix.Level != L2 {
		t.Errorf("fix_bug level = %d, want L2", fix.Level)
	}
	if fix.Detail == nil {
		t.Error("fix_bug L2 detail is nil")
	}
	// Unknown symbol → not-found error.
	if _, err := RetrieveForTask(ix, "NoSuchSymbol", "refactor", 0); err == nil || !strings.Contains(err.Error(), `symbol "NoSuchSymbol" not found in index`) {
		t.Errorf("unknown symbol err = %v, want not-found error", err)
	}
}
