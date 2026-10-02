package mutation

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestRunIsolatedNeverTouchesRealTree pins the Isolate contract: with
// Isolate: true, mutants are evaluated inside a worktree copy — the real
// tree's files stay byte-identical throughout the run, mutants are still
// evaluated (killed by the good test), and the report's Root is translated
// back to the real root, not the worktree temp dir.
func TestRunIsolatedNeverTouchesRealTree(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real-execution mutation testing in -short mode")
	}
	dir := t.TempDir()

	goMod := "module example.com/iso\n\ngo 1.21\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	code := `package iso

func Greater(a, b int) bool {
	return a > b
}
`
	testCode := `package iso

import "testing"

func TestGreater(t *testing.T) {
	if !Greater(5, 3) {
		t.Errorf("expected 5 > 3 to be true")
	}
	if Greater(3, 5) {
		t.Errorf("expected 3 > 5 to be false")
	}
}
`
	if err := os.WriteFile(filepath.Join(dir, "iso.go"), []byte(code), 0644); err != nil {
		t.Fatalf("write iso.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "iso_test.go"), []byte(testCode), 0644); err != nil {
		t.Fatalf("write iso_test.go: %v", err)
	}

	report, err := Run(context.Background(), Options{
		Root:        dir,
		Files:       []string{"iso.go"},
		MaxMutants:  10,
		TestCommand: "go test . -count=1",
		Isolate:     true,
	})
	if err != nil {
		t.Fatalf("isolated Run failed: %v", err)
	}

	if report.KilledCount == 0 {
		t.Errorf("expected at least 1 killed mutant, got report: %+v", report)
	}
	if report.Root != dir {
		t.Errorf("report Root = %q, want the real root %q (not the worktree)", report.Root, dir)
	}
	for _, f := range []string{"iso.go", "iso_test.go", "go.mod"} {
		got, rerr := os.ReadFile(filepath.Join(dir, f))
		if rerr != nil {
			t.Fatalf("read %s after isolated run: %v", f, rerr)
		}
		var want []byte
		switch f {
		case "iso.go":
			want = []byte(code)
		case "iso_test.go":
			want = []byte(testCode)
		default:
			want = []byte(goMod)
		}
		if string(got) != string(want) {
			t.Errorf("real tree file %s was modified by an isolated run — isolation broken", f)
		}
	}
	// No journal or backup residue in the real root either.
	if _, serr := os.Stat(filepath.Join(dir, ".kern", "mutation-journal-")); serr == nil {
		t.Error("isolated run must not leave journal machinery in the real root")
	}
}
