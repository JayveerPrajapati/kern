package index

import (
	"strings"
	"testing"
)

// TestCheckFileSymbolConflictsExactDuplicatesAllowed: two symbols with the
// same identity (name, kind, receiver, line, end) in one file are NOT a
// collision — the task allows exact duplicates to stay silently (the graph
// node and its line data are identical), so the guard must not fire.
func TestCheckFileSymbolConflictsExactDuplicatesAllowed(t *testing.T) {
	syms := []Symbol{
		{Kind: "func", Name: "New", File: "app.go", Line: 5, End: 10, Lang: "go"},
		{Kind: "func", Name: "New", File: "app.go", Line: 5, End: 10, Lang: "go"},
	}
	if err := checkFileSymbolConflicts("app.go", syms); err != nil {
		t.Fatalf("exact duplicates must not conflict, got error: %v", err)
	}
}

// TestCheckFileSymbolConflictsConflictFailsLoud: same identity on different
// lines is the SAME graph node (FullName key) defined twice — an extractor
// bug that must fail loudly instead of silently merging into first-wins line
// data. Type kinds (struct here) carry no receiver, so the guard's
// type-kind path is what fires.
func TestCheckFileSymbolConflictsConflictFailsLoud(t *testing.T) {
	syms := []Symbol{
		{Kind: "struct", Name: "New", File: "app.go", Line: 5, End: 10, Lang: "go"},
		{Kind: "struct", Name: "New", File: "app.go", Line: 41, End: 50, Lang: "go"},
	}
	err := checkFileSymbolConflicts("app.go", syms)
	if err == nil {
		t.Fatal("conflicting duplicate (same identity, different line) must fail loudly")
	}
	for _, want := range []string{"app.go", "New", "lines 5 and 41"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q is missing %q", err.Error(), want)
		}
	}
	// A receiver-bearing method with the same name on the same receiver in
	// one file is a genuine duplicate too — the receiver path also fires.
	syms = []Symbol{
		{Kind: "method", Name: "list", Receiver: "A", File: "models.py", Line: 5, End: 9, Lang: "python"},
		{Kind: "method", Name: "list", Receiver: "A", File: "models.py", Line: 20, End: 24, Lang: "python"},
	}
	if err := checkFileSymbolConflicts("models.py", syms); err == nil {
		t.Fatal("same receiver + same method name on different lines must fail loudly")
	}
}

// TestCheckFileSymbolConflictsAmongThree: the conflict must be found even
// when a third symbol sorts between the colliding pair.
func TestCheckFileSymbolConflictsAmongThree(t *testing.T) {
	syms := []Symbol{
		{Kind: "func", Name: "alpha", File: "a.go", Line: 1},
		{Kind: "struct", Name: "New", File: "a.go", Line: 5},
		{Kind: "struct", Name: "New", File: "a.go", Line: 9},
	}
	if err := checkFileSymbolConflicts("a.go", syms); err == nil {
		t.Fatal("conflict between New@5 and New@9 must be detected despite alpha sorting between")
	}
}

// TestCheckFileSymbolConflictsDifferentReceiverIsNotACollision: A.list and
// B.list in one file are distinct graph nodes (FullName includes the
// receiver), so they must not trip the conflict check — the ambiguity
// machinery handles them.
func TestCheckFileSymbolConflictsDifferentReceiverIsNotACollision(t *testing.T) {
	syms := []Symbol{
		{Kind: "method", Name: "list", Receiver: "A", File: "models.py", Line: 5, End: 9, Lang: "python"},
		{Kind: "method", Name: "list", Receiver: "B", File: "models.py", Line: 20, End: 24, Lang: "python"},
	}
	if err := checkFileSymbolConflicts("models.py", syms); err != nil {
		t.Fatalf("different receivers must not conflict, got: %v", err)
	}
}

// TestCheckFileSymbolConflictsMarkerKindsExempt: entry (route heuristics —
// several routes can name the same handler) and heading (repeated doc
// titles) markers legitimately repeat in one file and are exempt from the
// conflict check.
func TestCheckFileSymbolConflictsMarkerKindsExempt(t *testing.T) {
	syms := []Symbol{
		{Kind: "entry", Name: "index", File: "routes.rb", Line: 4, Lang: "ruby", Entry: true},
		{Kind: "entry", Name: "index", File: "routes.rb", Line: 9, Lang: "ruby", Entry: true},
		{Kind: "heading", Name: "Setup", File: "README.md", Line: 3, Lang: "markdown"},
		{Kind: "heading", Name: "Setup", File: "README.md", Line: 31, Lang: "markdown"},
	}
	if err := checkFileSymbolConflicts("routes.rb", syms); err != nil {
		t.Fatalf("marker kinds must be exempt from the conflict check, got: %v", err)
	}
}

// TestCheckFileSymbolConflictsNilAndSingleton: empty and single-symbol files
// never conflict (the cheap path).
func TestCheckFileSymbolConflictsNilAndSingleton(t *testing.T) {
	if err := checkFileSymbolConflicts("a.go", nil); err != nil {
		t.Fatalf("nil symbols must not conflict, got: %v", err)
	}
	if err := checkFileSymbolConflicts("a.go", []Symbol{{Kind: "func", Name: "Only"}}); err != nil {
		t.Fatalf("single symbol must not conflict, got: %v", err)
	}
}

// TestCheckFileSymbolConflictsDataPropKindsExempt: JSON/YAML property keys
// (kind "prop") legitimately repeat — every object in a JSON array
// re-declares the same keys ("from"/"to" per edge object) on different
// lines — so they are exempt from the conflict check just like the
// entry/heading markers. Receiver-less code symbols (scoped const/var/func,
// shell re-assignment) legitimately repeat too. A repeated type kind in the
// same file still fails loudly.
func TestCheckFileSymbolConflictsDataPropKindsExempt(t *testing.T) {
	syms := []Symbol{
		{Kind: "prop", Name: "from", File: "edges-authsvc.json", Line: 6, Lang: "json"},
		{Kind: "prop", Name: "from", File: "edges-authsvc.json", Line: 7, Lang: "json"},
		{Kind: "prop", Name: "to", File: "edges-authsvc.json", Line: 6, Lang: "json"},
		{Kind: "prop", Name: "to", File: "edges-authsvc.json", Line: 7, Lang: "json"},
	}
	if err := checkFileSymbolConflicts("edges-authsvc.json", syms); err != nil {
		t.Fatalf("repeated JSON-prop symbols must not conflict, got: %v", err)
	}
	// Shell variables legitimately re-assign (ASSUME_YES=0 then =1 in the
	// same script).
	if err := checkFileSymbolConflicts("install.sh", []Symbol{
		{Kind: "var", Name: "ASSUME_YES", File: "install.sh", Line: 79, Lang: "shell"},
		{Kind: "var", Name: "ASSUME_YES", File: "install.sh", Line: 83, Lang: "shell"},
	}); err != nil {
		t.Fatalf("repeated shell vars must not conflict, got: %v", err)
	}
	// Scoped locals with empty receivers (the TS const g in two different
	// functions of fixtures.ts; Go's blank `_` var) legitimately repeat.
	if err := checkFileSymbolConflicts("fixtures.ts", []Symbol{
		{Kind: "const", Name: "g", File: "fixtures.ts", Line: 26, Lang: "typescript"},
		{Kind: "const", Name: "g", File: "fixtures.ts", Line: 36, Lang: "typescript"},
	}); err != nil {
		t.Fatalf("scoped consts must not conflict, got: %v", err)
	}
	if err := checkFileSymbolConflicts("services.go", []Symbol{
		{Kind: "var", Name: "_", File: "services.go", Line: 197, Lang: "go"},
		{Kind: "var", Name: "_", File: "services.go", Line: 198, Lang: "go"},
	}); err != nil {
		t.Fatalf("repeated blank vars must not conflict, got: %v", err)
	}
	// A repeated type kind in the same file still fails loudly.
	if err := checkFileSymbolConflicts("a.go", []Symbol{
		{Kind: "struct", Name: "New", File: "a.go", Line: 5},
		{Kind: "struct", Name: "New", File: "a.go", Line: 41},
	}); err == nil {
		t.Fatal("repeated type must still fail loudly")
	}
}
