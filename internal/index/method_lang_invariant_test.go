package index

import (
	"strings"
	"testing"
)

// TestMethodLangInvariantFixesUpUnambiguousFiles pins the ADV-3 fixup half of
// the Receiver⇒Lang invariant: a method symbol whose extractor forgot Lang
// but whose file language is unambiguous (.go ⇒ "go") is repaired by the
// finalize pass — the A5 bare-callee-never-methods guard
// (internal/intel/queries.go calleeIsTarget) stays trustworthy because every
// Go method ends up carrying Lang "go".
func TestMethodLangInvariantFixesUpUnambiguousFiles(t *testing.T) {
	ix := &Index{
		Symbols: []Symbol{
			{Kind: "type", Name: "Store", File: "store.go", Line: 1},
			// Lang deliberately omitted — the invariant must repair it.
			{Kind: "method", Name: "Get", Receiver: "Store", File: "store.go", Line: 2},
			{Kind: "func", Name: "load", File: "store.go", Line: 5},
		},
		Calls:       map[string][]CallEdge{},
		Callers:     map[string][]string{},
		Inherits:    map[string][]string{},
		InheritedBy: map[string][]string{},
	}
	ix.computeCallers()
	for _, s := range ix.Symbols {
		if s.Name == "Get" {
			if s.Lang != "go" {
				t.Fatalf("method Store.Get Lang = %q, want %q after unambiguous .go fixup (ADV-3)", s.Lang, "go")
			}
			return
		}
	}
	t.Fatal("fixture method Store.Get missing after computeCallers")
}

// TestMethodLangInvariantFailsLoudlyOnAmbiguous pins the fail-loud half: a
// method symbol whose language cannot be recovered from the file path alone
// (unknown extension, content-dependent .vue) panics with a count naming the
// offenders instead of silently guessing — silent mutation could mis-scope
// the Go-only bare-callee guard and re-open the over-attribution bug.
func TestMethodLangInvariantFailsLoudlyOnAmbiguous(t *testing.T) {
	ix := &Index{
		Symbols: []Symbol{
			{Kind: "type", Name: "Thing", File: "thing.xyz", Line: 1},
			{Kind: "method", Name: "Do", Receiver: "Thing", File: "thing.xyz", Line: 2},
			{Kind: "type", Name: "Other", File: "other.vue", Line: 1},
			{Kind: "method", Name: "Run", Receiver: "Other", File: "other.vue", Line: 2},
		},
		Calls:       map[string][]CallEdge{},
		Callers:     map[string][]string{},
		Inherits:    map[string][]string{},
		InheritedBy: map[string][]string{},
	}
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("computeCallers must panic on method symbols with an ambiguous language (ADV-3)")
		}
		msg, ok := r.(string)
		if !ok {
			t.Fatalf("panic value = %T %v, want a string message", r, r)
		}
		if !strings.Contains(msg, "2 method symbol(s)") {
			t.Errorf("panic message must carry the offender count: %q", msg)
		}
		if !strings.Contains(msg, "Thing.Do @ thing.xyz") || !strings.Contains(msg, "Other.Run @ other.vue") {
			t.Errorf("panic message must name the offending symbols: %q", msg)
		}
	}()
	ix.computeCallers()
}
