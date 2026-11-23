package index

import (
	"os"
	"path/filepath"
	"testing"
)

// TestBuildQuarantinesDuplicateIdentities pins the availability-over-strictness
// policy (2026-11-04 campaign): a file with a genuine same-identity duplicate
// (an extractor anomaly or a legal-language pattern the gate cannot tell
// apart) must NOT fail the whole repo's build. The first declaration is kept,
// later duplicates are dropped, and the index counts the quarantine so
// `kern index --status` can surface it. Before the policy, this build died
// with "duplicate symbol conflict ... refusing to index".
func TestBuildQuarantinesDuplicateIdentities(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"app.py": `class Greeter:
    def hello(self):
        return "first"

class Greeter:
    def hello(self):
        return "second"

def main():
    return Greeter().hello()
`,
	})
	ix, err := Build(dir)
	if err != nil {
		t.Fatalf("duplicate identities must quarantine, not fail the build: %v", err)
	}
	if ix.DroppedSymbolConflicts != 2 {
		t.Fatalf("DroppedSymbolConflicts = %d, want 2 (the repeated class AND its repeated method identity)", ix.DroppedSymbolConflicts)
	}
	var greeters []Symbol
	for _, s := range ix.Symbols {
		if s.Kind == "class" && s.Name == "Greeter" {
			greeters = append(greeters, s)
		}
	}
	if len(greeters) != 1 {
		t.Fatalf("got %d Greeter class symbols, want 1 (first declaration kept): %+v", len(greeters), greeters)
	}
	if greeters[0].Line != 1 {
		t.Fatalf("kept Greeter line = %d, want 1 (the first declaration)", greeters[0].Line)
	}
	// The rest of the file is unaffected.
	if len(ix.Search("class Greeter", 10)) != 1 {
		t.Fatalf("search for the class must find exactly one Greeter")
	}
}

// TestDropDuplicateIdentities mirrors checkFileSymbolConflicts' equality
// contract: only same (name, kind, receiver, outer, params) on the
// conflict-checked kinds is dropped, first occurrence wins; overloads
// (different params) and different receivers are NOT duplicates.
func TestDropDuplicateIdentities(t *testing.T) {
	syms := []Symbol{
		{Kind: "class", Name: "A", Line: 1},
		{Kind: "class", Name: "A", Line: 10},
		{Kind: "class", Name: "A", Line: 20},
		{Kind: "method", Name: "add", Receiver: "U", Line: 30, Params: []string{"int a"}},
		{Kind: "method", Name: "add", Receiver: "U", Line: 40, Params: []string{"int a", "int b"}}, // overload: kept
		{Kind: "method", Name: "add", Receiver: "V", Line: 50, Params: []string{"int a"}},          // other receiver: kept
		{Kind: "method", Name: "add", Receiver: "U", Line: 60, Params: []string{"int a"}},          // duplicate of :30: dropped
		{Kind: "func", Name: "local", Line: 70},                                                    // scoped local, empty receiver: exempt
		{Kind: "func", Name: "local", Line: 80},                                                    // kept
	}
	kept, dropped := dropDuplicateIdentities(syms)
	if len(dropped) != 3 {
		t.Fatalf("dropped %d symbols, want 3 (the line-10 and line-20 A, and the U.add repeat): %+v", len(dropped), dropped)
	}
	if dropped[0].Name != "A" || dropped[0].Line != 10 {
		t.Errorf("dropped[0] = %+v, want the line-10 A", dropped[0])
	}
	if dropped[1].Name != "A" || dropped[1].Line != 20 {
		t.Errorf("dropped[1] = %+v, want the line-20 A", dropped[1])
	}
	if dropped[2].Name != "add" || dropped[2].Line != 60 {
		t.Errorf("dropped[2] = %+v, want the line-60 U.add", dropped[2])
	}
	if len(kept) != 6 {
		t.Fatalf("kept %d symbols, want 6: %+v", len(kept), kept)
	}
	if kept[0].Line != 1 {
		t.Errorf("first A must be kept (line 1), got line %d", kept[0].Line)
	}
	var addOverload, addOtherRecv, secondLocal bool
	for _, s := range kept {
		switch {
		case s.Kind == "method" && s.Name == "add" && s.Receiver == "U" && s.Line == 40:
			addOverload = true
		case s.Kind == "method" && s.Name == "add" && s.Receiver == "V":
			addOtherRecv = true
		case s.Kind == "func" && s.Name == "local" && s.Line == 80:
			secondLocal = true
		}
	}
	if !addOverload || !addOtherRecv || !secondLocal {
		t.Errorf("overload (line 40) / other-receiver add (V) / second local must all be kept: %+v", kept)
	}
}

// TestQuarantineCounterPersistedRoundTrip: the counter survives Save/Load
// (SQLite store) and is surfaced by StatusReport — mirroring the
// BuildStats round-trip pin.
func TestQuarantineCounterPersistedRoundTrip(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "app.py"), []byte("class A:\n    pass\n\nclass A:\n    pass\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ix, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	if ix.DroppedSymbolConflicts != 1 {
		t.Fatalf("DroppedSymbolConflicts = %d, want 1", ix.DroppedSymbolConflicts)
	}
	if err := ix.Save(); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(root)
	if err != nil || loaded == nil {
		t.Fatalf("Load: %v, %v", loaded, err)
	}
	if loaded.DroppedSymbolConflicts != 1 {
		t.Fatalf("loaded DroppedSymbolConflicts = %d, want 1 (persisted via meta)", loaded.DroppedSymbolConflicts)
	}
	st, err := StatusReport(root, false)
	if err != nil || st == nil {
		t.Fatalf("StatusReport: %v, %v", st, err)
	}
	if st.DroppedSymbolConflicts != 1 {
		t.Fatalf("status DroppedSymbolConflicts = %d, want 1", st.DroppedSymbolConflicts)
	}
}

// TestBuildCleanConflictFreeKeepsZeroCounter: the quarantine counter stays
// zero on a conflict-free build (no false positives from the drop pass).
func TestBuildCleanConflictFreeKeepsZeroCounter(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"main.go": "package main\n\nfunc main() {}\n",
	})
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	if ix.DroppedSymbolConflicts != 0 {
		t.Fatalf("DroppedSymbolConflicts = %d, want 0 on a clean build", ix.DroppedSymbolConflicts)
	}
}
