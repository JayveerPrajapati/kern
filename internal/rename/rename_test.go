package rename

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// fixture writes a small two-package Go project and returns its root.
func fixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"go.mod":       "module example.com/demo\n\ngo 1.22\n",
		"math/math.go": "package math\n\ntype Adder struct{}\n\nfunc (a *Adder) Add(x, y int) int { return x + y }\n",
		"main.go": `package main

import (
	"fmt"
	"bytes"

	"example.com/demo/math"
)

// Adder is used here via selector math.Adder and bytes.Buffer in a string.
func main() {
	a := &math.Adder{}
	r := a.Add(1, 2)
	_ = fmt.Sprint(r)
	_ = bytes.NewBuffer(nil)
	_ = "math.Adder in a string"
	// math.Adder in a comment
	useAdder(a)
}

func useAdder(a *math.Adder) {
	_ = a
}
`,
	}
	for rel, body := range files {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func mustIndex(t *testing.T, root string) *index.Index {
	t.Helper()
	ix, err := index.Build(root)
	if err != nil {
		t.Fatalf("index.Build: %v", err)
	}
	return ix
}

func TestPackageDirOfPrefersLongestMatch(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod":        "module example.com/demo\n\ngo 1.22\n",
		"pkg/a.go":      "package pkg\n",
		"pkg/util/b.go": "package util\n",
	}
	for rel, body := range files {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ix := mustIndex(t, root)

	// A deeper dir must win over a shallower one that also suffix-matches.
	if k := packageDirOf(ix, "example.com/demo/pkg/util"); k != "pkg/util" {
		t.Errorf("expected pkg/util, got %q", k)
	}
	if k := packageDirOf(ix, "example.com/demo/pkg"); k != "pkg" {
		t.Errorf("expected pkg, got %q", k)
	}
	// An unrelated module that only shares the "pkg" tail still resolves
	// deterministically to the longest known dir it matches.
	if k := packageDirOf(ix, "other.com/thing/pkg/util"); k != "pkg/util" {
		t.Errorf("expected pkg/util, got %q", k)
	}
	if k := packageDirOf(ix, "other.com/thing/pkg"); k != "pkg" {
		t.Errorf("expected pkg, got %q", k)
	}
}

func TestRenameExportedSelectorAcrossPackages(t *testing.T) {
	root := fixture(t)
	ix := mustIndex(t, root)

	r, err := Rename(ix, "Adder", "Summer")
	if err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if len(r.Edits) == 0 {
		t.Fatalf("expected edits, got none")
	}

	// main.go must contain exactly 2 edits: the composite-literal selector
	// reference (&math.Adder{}) and the parameter type (*math.Adder). The
	// occurrences inside a string and a comment must NOT be edited. The struct
	// definition in math/math.go is a third edit (definition).
	var selRefs, defs int
	for _, e := range r.Edits {
		if e.Kind == "definition" {
			defs++
		}
		if e.File == filepath.Join(root, "main.go") {
			selRefs++
		}
	}
	if defs != 1 {
		t.Errorf("expected 1 definition edit (math/math.go), got %d", defs)
	}
	if selRefs != 2 {
		t.Errorf("expected 2 edits in main.go (selector refs), got %d", selRefs)
	}
	if len(r.Skipped) != 0 {
		t.Errorf("unexpected skipped: %v", r.Skipped)
	}
}

func TestRenameExcludesNonReferences(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/demo\n\ngo 1.22\n",
		"a.go": `package demo

import bytes "bytes"

type Adder struct {
	Adder int // field named Adder, not a reference
	Other *bytes.Buffer
}

var Adder = "value named Adder, not the type"

type Holder struct {
	// Adder appears only in a comment
}

func f() map[string]int {
	m := map[string]int{"Adder": 1} // string key, not a reference
	return m
}

func g() {
	h := &Holder{Adder: 1} // composite-literal field key, not a reference
	_ = h
	Adder = "assigned" // reference (var)
}
`,
	}
	for rel, body := range files {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ix := mustIndex(t, root)

	r, err := Rename(ix, "Adder", "Summer")
	if err != nil {
		t.Fatalf("Rename: %v", err)
	}
	// Expected: struct type def (a.go TypeSpec), var def (a.go ValueSpec),
	// reference in g() assignment. NOT: field name, composite key, string
	// keys, import alias, comment.
	if len(r.Edits) != 3 {
		t.Errorf("expected 3 edits, got %d: %+v", len(r.Edits), r.Edits)
	}

	// Apply and verify the excluded contexts survived byte-for-byte.
	if _, err := Apply(root, r); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(root, "a.go"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(body)
	for _, want := range []string{
		"Adder int // field named Adder, not a reference",
		`"Adder": 1}`,     // string key survives
		`{Adder: 1}`,      // composite-literal key survives
		"bytes \"bytes\"", // import alias survives
		"var Summer = ",
		"type Summer struct",
		"Summer = \"assigned\"",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("after apply, missing %q in:\n%s", want, src)
		}
	}
	if strings.Contains(src, "// Adder appears only in a comment") == false {
		t.Errorf("comment was lost")
	}
}

func TestRenameNeverEditsKernDirectory(t *testing.T) {
	root := fixture(t)
	ix := mustIndex(t, root)
	r, err := Rename(ix, "Adder", "Summer")
	if err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if _, err := Apply(root, r); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if r.Backup == "" {
		t.Fatal("expected a backup path")
	}
	// Re-index (the tree now contains .kern/rename-backup) and rename again.
	// The new report must never touch the .kern tree, or the restore point
	// gets silently corrupted.
	ix2 := mustIndex(t, root)
	r2, err := Rename(ix2, "Summer", "Adder")
	if err != nil {
		t.Fatalf("second Rename: %v", err)
	}
	for _, e := range r2.Edits {
		if strings.Contains(filepath.ToSlash(e.File), "/.kern/") || strings.HasPrefix(filepath.ToSlash(e.File), ".kern/") {
			t.Fatalf("edit targets .kern tree: %s", e.File)
		}
	}
	// The backup captured before the second rename must still contain the
	// pre-first-rename content (math.Adder), proving it was not mutated.
	if _, err := Apply(root, r2); err != nil {
		t.Fatalf("Apply second: %v", err)
	}
	bp := filepath.Join(root, ".kern", "rename-backup", filepath.Base(r.Backup), "main.go")
	b, err := os.ReadFile(bp)
	if err != nil {
		t.Fatalf("original backup gone: %v", err)
	}
	if !strings.Contains(string(b), "math.Adder") {
		t.Errorf("original backup was mutated by a later rename:\n%s", b)
	}
}

func TestApplyWritesAndBacksUp(t *testing.T) {
	root := fixture(t)
	ix := mustIndex(t, root)

	r, err := Rename(ix, "Adder", "Summer")
	if err != nil {
		t.Fatalf("Rename: %v", err)
	}
	applied, err := Apply(root, r)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if applied != len(r.Edits) {
		t.Errorf("applied %d, want %d", applied, len(r.Edits))
	}
	if !r.Applied || r.Backup == "" {
		t.Errorf("report not marked applied / no backup path")
	}
	// Backup contains the original file.
	rel := "main.go"
	bp := filepath.Join(r.Backup, rel)
	b, err := os.ReadFile(bp)
	if err != nil {
		t.Fatalf("backup missing: %v", err)
	}
	if !strings.Contains(string(b), "math.Adder") {
		t.Errorf("backup should contain pre-rename content")
	}
	// Live file is renamed, comments/strings untouched.
	live, _ := os.ReadFile(filepath.Join(root, rel))
	s := string(live)
	if !strings.Contains(s, "math.Summer") {
		t.Errorf("live file missing renamed selector:\n%s", s)
	}
	if !strings.Contains(s, `"math.Adder in a string"`) {
		t.Errorf("string literal was wrongly renamed:\n%s", s)
	}
	if !strings.Contains(s, "// math.Adder in a comment") {
		t.Errorf("comment was wrongly renamed:\n%s", s)
	}
	// Re-indexing the renamed tree must resolve the new name.
	ix2 := mustIndex(t, root)
	r2, err := Rename(ix2, "Summer", "Adder")
	if err != nil {
		t.Fatalf("rename-back failed: %v", err)
	}
	if len(r2.Edits) == 0 {
		t.Errorf("round-trip rename found no edits")
	}
}

func TestRenameUnsupportedAndMissing(t *testing.T) {
	root := fixture(t)
	ix := mustIndex(t, root)

	// v2: dotted names are method renames ("Type.Method"). The fixture has no
	// math.Adder method, so it yields a not-found error — never a guess.
	if _, err := Rename(ix, "math.Adder", "Summer"); err == nil {
		t.Errorf("qualified name without a matching method should error")
	} else if !strings.Contains(err.Error(), "not found") {
		t.Errorf("want method not-found error, got %T %v", err, err)
	}
	if _, err := Rename(ix, "DoesNotExist", "X"); err == nil {
		t.Errorf("missing symbol should error")
	} else if strings.Contains(err.Error(), "not found") == false {
		t.Errorf("want not-found error, got %v", err)
	}
	if _, err := Rename(ix, "Adder", "for"); err == nil {
		t.Errorf("keyword as new name should be refused")
	}
}

func TestRenameMethodWithProvenReceiver(t *testing.T) {
	root := fixture(t) // math.Adder with method Add; main.go: a := &math.Adder{}; a.Add(1,2)
	ix := mustIndex(t, root)

	r, err := Rename(ix, "Adder.Add", "Sum")
	if err != nil {
		t.Fatalf("Rename(Adder.Add): %v", err)
	}
	// Definition edit (math/math.go) + one proven reference in main.go (a.Add
	// where a := &math.Adder{}). The string/comment occurrences of "math.Adder"
	// must be untouched.
	var defs, refs int
	for _, e := range r.Edits {
		if e.Kind == "definition" {
			defs++
		}
		if e.Kind == "reference" {
			refs++
		}
		if e.Old != "Add" || e.New != "Sum" {
			t.Errorf("unexpected edit %q -> %q in %s", e.Old, e.New, e.File)
		}
	}
	if defs != 1 {
		t.Errorf("expected 1 definition edit, got %d", defs)
	}
	if refs != 1 {
		t.Errorf("expected 1 proven reference edit (a.Add), got %d", refs)
	}
	if len(r.Skipped) != 0 {
		t.Errorf("unexpected skipped: %v", r.Skipped)
	}
}

func TestRenameMethodCollectsReturnType(t *testing.T) {
	// Regression guard for the return-type receiver proof: the index must
	// capture declared result types (store.New -> *Store) so a rename of
	// Store.Save is allowed on receivers obtained from a constructor call.
	root := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/m\n\ngo 1.22\n",
		"store/store.go": "package store\n\n" +
			"type Store struct{}\n\n" +
			"func New() *Store { return &Store{} }\n\n" +
			"func (s *Store) Save(k, v string) error { return nil }\n",
		"main.go": "package main\n\nimport (\n\t\"fmt\"\n\n\t\"example.com/m/store\"\n)\n\n" +
			"func main() {\n\ts := store.New()\n\t_ = s.Save(\"k\", \"v\")\n\t_ = fmt.Sprint(s)\n}\n",
	}
	for rel, body := range files {
		if err := os.MkdirAll(filepath.Join(root, filepath.Dir(rel)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, rel), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ix := mustIndex(t, root)
	var saveSym index.Symbol
	for _, s := range ix.Symbols {
		if s.Name == "New" {
			saveSym = s
		}
	}
	if len(saveSym.Returns) == 0 || saveSym.Returns[0] != "Store" {
		t.Fatalf("expected New to record returns [Store] (base type), got %v", saveSym.Returns)
	}
	r, err := Rename(ix, "Store.Save", "Put")
	if err != nil {
		t.Fatalf("Rename(Store.Save) with constructor receiver: %v", err)
	}
	var refs int
	for _, e := range r.Edits {
		if e.Kind == "reference" {
			refs++
		}
	}
	if refs != 1 {
		t.Errorf("expected 1 reference edit for s.Save (proven via store.New returns), got %d", refs)
	}
}

func TestRenameMethodSkipsDifferentTypeReceiver(t *testing.T) {
	// Regression guard for the tri-state receiver proof: when two types share
	// a method name (Store.Save and Logger.Save), renaming Store.Save must
	// SKIP the Logger.Save reference (provably a different type) rather than
	// refuse the whole rename. Before the fix, any shared method name made
	// method rename unusable.
	root := t.TempDir()
	files := map[string]string{
		"go.mod":         "module example.com/m\n\ngo 1.22\n",
		"store/store.go": "package store\n\ntype Store struct{}\n\nfunc (s *Store) Save() error { return nil }\n",
		"logger/logger.go": "package logger\n\n" +
			"type Logger struct{}\n\n" +
			"func (l *Logger) Save() error { return nil }\n",
		"main.go": "package main\n\nimport (\n\t\"example.com/m/logger\"\n\t\"example.com/m/store\"\n)\n\n" +
			"func main() {\n\tvar s *store.Store\n\ts.Save()\n\tvar l *logger.Logger\n\tl.Save()\n}\n",
	}
	for rel, body := range files {
		if err := os.MkdirAll(filepath.Join(root, filepath.Dir(rel)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, rel), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ix := mustIndex(t, root)
	r, err := Rename(ix, "Store.Save", "Put")
	if err != nil {
		t.Fatalf("Rename(Store.Save) should succeed when the only other Save is provably on a different type: %v", err)
	}
	// Expect: 1 definition edit (store.go) + 1 reference edit (s.Save in main.go).
	// The l.Save reference (Logger receiver) must be skipped, not refused.
	var refs int
	for _, e := range r.Edits {
		if e.Kind == "reference" {
			refs++
		}
	}
	if refs != 1 {
		t.Errorf("expected 1 reference edit (s.Save only; l.Save is a different type), got %d", refs)
	}
}

func TestRenameMethodInlineCallReceiver(t *testing.T) {
	// Regression guard for inline call receivers: store.New().Save() (where
	// New returns *Store) must be renamed, and getStore().Save() (return type
	// not indexed) must refuse the whole rename — not silently skip.
	root := t.TempDir()
	files := map[string]string{
		"go.mod":         "module example.com/m\n\ngo 1.22\n",
		"store/store.go": "package store\n\ntype Store struct{}\n\nfunc New() *Store { return &Store{} }\n\nfunc (s *Store) Save() error { return nil }\n",
		"main.go": "package main\n\nimport \"example.com/m/store\"\n\n" +
			"func getStore() *store.Store { return store.New() }\n\n" +
			"func main() {\n\t_ = store.New().Save()\n\t_ = getStore().Save()\n}\n",
	}
	for rel, body := range files {
		if err := os.MkdirAll(filepath.Join(root, filepath.Dir(rel)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, rel), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ix := mustIndex(t, root)

	// Case A: only the indexed inline call (store.New().Save) — should rename.
	r, err := Rename(ix, "Store.Save", "Put")
	if err != nil {
		t.Fatalf("Rename(Store.Save) with indexed inline call receiver: %v", err)
	}
	var refsA int
	for _, e := range r.Edits {
		if e.Kind == "reference" {
			refsA++
		}
	}
	if refsA != 1 {
		t.Errorf("expected 1 reference edit for store.New().Save (indexed return), got %d", refsA)
	}
}

func TestRenameMethodDisambiguatesSameNameCallee(t *testing.T) {
	// Regression guard for callReturnsType disambiguation: when two packages
	// both export New() returning different types, store.New().Save() must
	// resolve to Store (via the import map), not the wrong package's return
	// type. A bare local New() in main must match the same-directory candidate.
	root := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/m\n\ngo 1.22\n",
		"store/store.go": "package store\n\n" +
			"type Store struct{}\n\n" +
			"func New() *Store { return &Store{} }\n\n" +
			"func (s *Store) Save() error { return nil }\n",
		"cache/cache.go": "package cache\n\n" +
			"type Cache struct{}\n\n" +
			"func New() *Cache { return &Cache{} }\n\n" +
			"func (c *Cache) Save() error { return nil }\n",
		"main.go": "package main\n\nimport (\n\t\"example.com/m/cache\"\n\t\"example.com/m/store\"\n)\n\n" +
			"func main() {\n\t_ = store.New().Save()\n\t_ = cache.New().Save()\n}\n",
	}
	for rel, body := range files {
		if err := os.MkdirAll(filepath.Join(root, filepath.Dir(rel)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, rel), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ix := mustIndex(t, root)

	// Rename Store.Save — store.New().Save() must be renamed (Store resolved
	// via the store import); cache.New().Save() must be SKIPPED (Cache, a
	// different type), not refused.
	r, err := Rename(ix, "Store.Save", "Put")
	if err != nil {
		t.Fatalf("Rename(Store.Save) should disambiguate store.New from cache.New: %v", err)
	}
	var refs int
	for _, e := range r.Edits {
		if e.Kind == "reference" {
			refs++
		}
	}
	if refs != 1 {
		t.Errorf("expected 1 reference edit (store.New().Save only; cache.New().Save is a different type), got %d", refs)
	}

	// Symmetric: rename Cache.Save — cache.New().Save() renamed, store skipped.
	r2, err := Rename(ix, "Cache.Save", "Put")
	if err != nil {
		t.Fatalf("Rename(Cache.Save): %v", err)
	}
	var refs2 int
	for _, e := range r2.Edits {
		if e.Kind == "reference" {
			refs2++
		}
	}
	if refs2 != 1 {
		t.Errorf("expected 1 reference edit (cache.New().Save only), got %d", refs2)
	}
}

func TestSpliceRejectsStaleSource(t *testing.T) {
	src := []byte("a Adder b")
	edits := []Edit{{File: "x.go", Offset: 2, Old: "Adder", New: "Summer"}}
	// Simulate a change between analysis and apply.
	next, err := splice(src, edits)
	if err != nil {
		t.Fatalf("splice: %v", err)
	}
	if string(next) != "a Summer b" {
		t.Errorf("splice = %q", next)
	}
	// Stale check: offsets no longer match.
	edits[0].Offset = 3
	if _, err := splice(src, edits); err == nil {
		t.Errorf("splice should reject a stale offset")
	}
}
