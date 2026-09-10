package remove

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/rename"
)

// writeTree writes a fixture tree and returns its root.
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, content := range files {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func build(t *testing.T, root string) *index.Index {
	t.Helper()
	ix, err := index.Build(root)
	if err != nil {
		t.Fatal(err)
	}
	return ix
}

// TestPlanRefusesLiveSymbol pins the gate: a symbol with production callers
// must be refused before any edit is computed.
func TestPlanRefusesLiveSymbol(t *testing.T) {
	root := writeTree(t, map[string]string{
		"lib/lib.go": `package lib

func Live() {}

func Use() { Live() }
`,
	})
	_, err := Plan(build(t, root), "Live")
	var u *ErrUnsafe
	if !errors.As(err, &u) {
		t.Fatalf("Plan(Live) error = %v, want ErrUnsafe", err)
	}
}

// TestPlanRefusesTypeSymbol pins the shape guard: types/vars/consts have
// dependencies the call graph does not model (methods on a deleted type).
func TestPlanRefusesTypeSymbol(t *testing.T) {
	root := writeTree(t, map[string]string{
		"lib/lib.go": `package lib

type Thing struct{}

func (t Thing) M() {}
`,
	})
	_, err := Plan(build(t, root), "Thing")
	var u *ErrUnsupported
	if !errors.As(err, &u) {
		t.Fatalf("Plan(Thing) error = %v, want ErrUnsupported", err)
	}
}

// TestPlanRefusesTestNonCallRef pins the fail-closed test scan: a test file
// referencing the symbol outside a call position would survive the edit set
// and break the build, so the plan must refuse.
func TestPlanRefusesTestNonCallRef(t *testing.T) {
	root := writeTree(t, map[string]string{
		"lib/lib.go": `package lib

func dead() {}
`,
		"lib/dead_test.go": `package lib

var _ = dead
`,
	})
	_, err := Plan(build(t, root), "dead")
	var u *ErrUnsafe
	if !errors.As(err, &u) {
		t.Fatalf("Plan(dead) error = %v, want ErrUnsafe (test value reference)", err)
	}
	if !strings.Contains(err.Error(), "dead_test.go") {
		t.Fatalf("ErrUnsafe = %q, want the offending file named", err)
	}
}

// TestPlanAndApplyRemovesDeadSymbolAndTestCaller pins the happy path: the
// plan covers the symbol's declaration (with its doc comment) and the
// test-only caller's declaration; Apply removes exactly those lines, backs
// the files up, and leaves live code untouched.
func TestPlanAndApplyRemovesDeadSymbolAndTestCaller(t *testing.T) {
	root := writeTree(t, map[string]string{
		"lib/lib.go": `package lib

// deadHelper is long unused.
func deadHelper() {}

func Live() {}
`,
		"lib/lib_test.go": `package lib

import "testing"

func TestUsesDeadHelper(t *testing.T) {
	deadHelper()
}

func TestStillLive(t *testing.T) {
	Live()
}
`,
	})
	ix := build(t, root)
	rep, err := Plan(ix, "deadHelper")
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Edits) != 2 {
		t.Fatalf("Plan edits = %d, want 2 (declaration + test caller)", len(rep.Edits))
	}
	n, err := rename.Apply(root, rep)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("Apply edits = %d, want 2", n)
	}
	if rep.Backup == "" {
		t.Fatal("Apply did not record a backup path")
	}
	if _, err := os.Stat(rep.Backup); err != nil {
		t.Fatalf("backup dir missing: %v", err)
	}
	lib, err := os.ReadFile(filepath.Join(root, "lib", "lib.go"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(lib), "deadHelper") {
		t.Fatalf("lib.go still contains deadHelper:\n%s", lib)
	}
	if !strings.Contains(string(lib), "func Live()") {
		t.Fatalf("lib.go lost Live():\n%s", lib)
	}
	// The doc comment must go with the declaration.
	if strings.Contains(string(lib), "long unused") {
		t.Fatalf("lib.go still contains the doc comment:\n%s", lib)
	}
	testFile, err := os.ReadFile(filepath.Join(root, "lib", "lib_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(testFile), "TestUsesDeadHelper") {
		t.Fatalf("test file still contains the removed caller:\n%s", testFile)
	}
	if !strings.Contains(string(testFile), "TestStillLive") {
		t.Fatalf("test file lost an unrelated test:\n%s", testFile)
	}
}

// TestApplyRollsBackOnDrift pins the transactional contract: when the source
// changed since analysis, splice refuses and every backed-up file is
// restored.
func TestApplyRollsBackOnDrift(t *testing.T) {
	root := writeTree(t, map[string]string{
		"lib/lib.go": `package lib

func dead() {}

func Live() {}
`,
	})
	ix := build(t, root)
	rep, err := Plan(ix, "dead")
	if err != nil {
		t.Fatal(err)
	}
	// Simulate drift: the file changed after planning.
	libPath := filepath.Join(root, "lib", "lib.go")
	before, err := os.ReadFile(libPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(libPath, append([]byte("// drifted\n"), before...), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := rename.Apply(root, rep); err == nil {
		t.Fatal("Apply succeeded on drifted source, want failure")
	}
	// The drifted file must be restored to its pre-apply state (with the
	// drift intact — rollback restores the backup taken at apply time, which
	// happened after the drift).
	after, err := os.ReadFile(libPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(after), "// drifted\n") {
		t.Fatalf("file not restored after failed apply:\n%s", after)
	}
}
