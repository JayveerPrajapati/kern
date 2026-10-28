package index

import "testing"

// TestImportBindingResolvesModuleInSubdirectory pins the module-root fix:
// when go.mod lives below the index root (src/, backend/, services/x/), an
// import path shares only a suffix with the repo-relative package directory.
// Before the fix the catalog mapped the import to "" and every consumer
// (explore/graph/impact/plan/review) dropped the cross-package caller.
func TestImportBindingResolvesModuleInSubdirectory(t *testing.T) {
	root := writeTree(t, map[string]string{
		"src/go.mod":    "module example.com/m\n\ngo 1.22\n",
		"src/pkga/a.go": "package pkga\n\nfunc Target() int { return 1 }\n",
		"src/pkgb/b.go": "package pkgb\n\nimport \"example.com/m/pkga\"\n\nfunc Caller() int { return pkga.Target() }\n",
	})
	ix, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}

	catalog := newExportCatalog(ix)
	if got := catalog.importDirs["example.com/m/pkga"]; got != "src/pkga" {
		t.Fatalf("import example.com/m/pkga mapped to %q, want src/pkga", got)
	}

	target, ok := ix.FindSymbol("Target")
	if !ok {
		t.Fatal("expected pkga.Target")
	}
	if callers := ix.CallersFor(target); !contains(callers, "Caller") {
		t.Fatalf("cross-package caller dropped: got %v, want Caller", callers)
	}
}

// TestImportBindingRefusesAmbiguousSuffix guards the fail-closed half of the
// same rule: two packages with the same name and the same shared suffix must
// not be bound by guesswork.
// TestStrayGoFileOutsideModuleNotIndexed pins the other half of the
// subdirectory-module shape: a .go file at a root that has no go.mod (while
// src/ does) is not buildable and must not invent a duplicate "." package.
func TestStrayGoFileOutsideModuleNotIndexed(t *testing.T) {
	root := writeTree(t, map[string]string{
		"src/go.mod":    "module example.com/m\n\ngo 1.22\n",
		"src/pkga/a.go": "package pkga\n\nfunc Target() int { return 1 }\n",
		"stray.go":      "package main\n\nfunc StrayThing() {}\n",
	})
	ix, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := ix.FindSymbol("Target"); !ok {
		t.Fatal("module symbol Target missing")
	}
	if _, ok := ix.FindSymbol("StrayThing"); ok {
		t.Fatal("stray .go file outside every module was indexed")
	}
}

func TestImportBindingRefusesAmbiguousSuffix(t *testing.T) {
	directories := map[string]bool{
		"src/team-a/actions": true,
		"src/team-b/actions": true,
	}
	pkgNames := map[string]string{
		"src/team-a/actions": "actions",
		"src/team-b/actions": "actions",
	}
	if got := matchImportByPackage("example.com/m/actions", directories, pkgNames); got != "" {
		t.Fatalf("ambiguous import resolved to %q, want unresolved", got)
	}
}
