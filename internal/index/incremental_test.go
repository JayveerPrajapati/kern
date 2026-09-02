package index

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// fixtureTree writes a small multi-language tree and returns its root.
func fixtureTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a/one.go", "package a\n\nfunc One() int { return 1 }\n\nfunc helper() {}\n")
	write("a/two.go", "package a\n\nfunc Two() int { return One() }\n")
	write("b/three.go", "package b\n\nimport \"a\"\n\nfunc Three() int { return a.One() }\n")
	write("b/extra.py", "def three():\n    return 3\n")
	write("b/generated_gen.go", "package b\n\n// Code generated. DO NOT EDIT.\nfunc Gen() {}\n")
	return root
}

// canonical zeroes the fields that legitimately differ between two
// equivalent builds (timestamps, diagnostics, non-serialized caches) so
// reflect.DeepEqual can compare the rest.
func canonical(t *testing.T, ix *Index) *Index {
	t.Helper()
	c := *ix
	c.UpdatedAt = time.Time{}
	c.reusedResults = 0
	c.fileResults = nil
	c.Identity = nil // captured per-build (timestamps inside)
	return &c
}

func assertEquivalent(t *testing.T, label string, a, b *Index) {
	t.Helper()
	if !reflect.DeepEqual(canonical(t, a), canonical(t, b)) {
		t.Errorf("%s: incremental build diverged from full rebuild\nlen(symbols): inc=%d full=%d\nMaxMtime: inc=%d full=%d",
			label, len(a.Symbols), len(b.Symbols), a.MaxMtime, b.MaxMtime)
	}
}

func TestBuildWithOptionsNoPriorMatchesBuild(t *testing.T) {
	root := fixtureTree(t)
	full, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	opts, err := BuildWithOptions(root)
	if err != nil {
		t.Fatal(err)
	}
	if opts.ReusedResults() != 0 {
		t.Errorf("no-prior build reused %d results, want 0", opts.ReusedResults())
	}
	assertEquivalent(t, "no-prior", opts, full)
}

func TestBuildWithOptionsIncrementalEquivalence(t *testing.T) {
	root := fixtureTree(t)
	prior, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}

	// Mutate one file (add a symbol), add a new file, touch an unchanged
	// file (new mtime, same content), and delete one.
	if err := os.WriteFile(filepath.Join(root, "a", "two.go"),
		[]byte("package a\n\nfunc Two() int { return One() }\n\nfunc TwoExtra() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "b", "four.go"),
		[]byte("package b\n\nfunc Four() int { return 4 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	touchTime := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(filepath.Join(root, "b", "extra.py"), touchTime, touchTime); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "b", "generated_gen.go")); err != nil {
		t.Fatal(err)
	}

	inc, err := BuildWithOptions(root, WithPriorIndex(prior))
	if err != nil {
		t.Fatal(err)
	}
	full, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}

	if inc.ReusedResults() == 0 {
		t.Errorf("incremental build reused nothing")
	}
	assertEquivalent(t, "mutated tree", inc, full)

	// The prior index must be untouched by the incremental build (its
	// Pkgs entries are shared-then-copied; a missing copy would leak the
	// new build's merges back into the live prior).
	for path, pkg := range prior.Pkgs {
		for _, imp := range pkg.Imports {
			if path == "b" && imp == "a" && len(pkg.Files) < 2 {
				t.Errorf("prior Pkgs[%s] mutated by incremental build: %+v", path, pkg)
			}
		}
	}

	// The deleted file's symbols must be gone from both.
	for _, s := range inc.Symbols {
		if s.File == "b/generated_gen.go" {
			t.Errorf("deleted file still present in incremental index: %+v", s)
		}
	}
	// The new symbol must be present in both.
	foundExtra := false
	for _, s := range inc.Symbols {
		if s.Name == "TwoExtra" {
			foundExtra = true
		}
	}
	if !foundExtra {
		t.Errorf("TwoExtra missing from incremental index")
	}
}

func TestBuildWithOptionsSerialPathEquivalence(t *testing.T) {
	t.Setenv("KERN_INDEX_SERIAL", "1")
	root := fixtureTree(t)
	prior, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a", "one.go"),
		[]byte("package a\n\nfunc One() int { return 1 }\n\nfunc Another() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	inc, err := BuildWithOptions(root, WithPriorIndex(prior))
	if err != nil {
		t.Fatal(err)
	}
	full, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	if inc.ReusedResults() == 0 {
		t.Errorf("serial incremental build reused nothing")
	}
	assertEquivalent(t, "serial path", inc, full)
}

func TestBuildWithOptionsIgnoresForeignRootPrior(t *testing.T) {
	root := fixtureTree(t)
	other := fixtureTree(t)
	prior, err := Build(other)
	if err != nil {
		t.Fatal(err)
	}
	inc, err := BuildWithOptions(root, WithPriorIndex(prior))
	if err != nil {
		t.Fatal(err)
	}
	if inc.ReusedResults() != 0 {
		t.Errorf("prior from a different root reused %d results, want 0", inc.ReusedResults())
	}
}

func TestBuildWithOptionsLoadedPriorFallsBack(t *testing.T) {
	// A prior with no fileResults (e.g. deserialized from disk) must
	// simply parse everything — no error, no reuse.
	root := fixtureTree(t)
	prior, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	prior.fileResults = nil
	inc, err := BuildWithOptions(root, WithPriorIndex(prior))
	if err != nil {
		t.Fatal(err)
	}
	if inc.ReusedResults() != 0 {
		t.Errorf("nil-fileResults prior reused %d results, want 0", inc.ReusedResults())
	}
}
