package apply

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/refactor"
)

// newModule creates a temp Go module: go.mod + lib.go (Lib returns 1) and
// lib_test.go asserting Lib() == 1.
func newModule(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/m\n\ngo 1.27\n")
	write("lib.go", "package m\n\nfunc Lib() int { return 1 }\n")
	write("lib_test.go", "package m\n\nimport \"testing\"\n\nfunc TestLib(t *testing.T) {\n\tif Lib() != 1 {\n\t\tt.Fatal(\"Lib changed\")\n\t}\n}\n")
	return dir
}

func TestApplySuccess(t *testing.T) {
	dir := newModule(t)
	res, err := Run(context.Background(), Request{
		Root: dir,
		Edits: []refactor.FileEdit{{
			Path:    "lib.go",
			Content: "package m\n\nfunc Lib() int { return 1 }\nfunc Extra() int { return Lib() * 2 }\n",
		}},
		Test: true,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.Success || !res.Applied || res.RolledBack {
		t.Fatalf("want success+applied, got %+v", res)
	}
	if len(res.ModifiedFiles) != 1 || res.ModifiedFiles[0] != "lib.go" {
		t.Fatalf("modified files = %v", res.ModifiedFiles)
	}
	if res.Receipt == "" {
		t.Fatal("missing receipt")
	}
}

func TestApplyVerifyFailRollsBack(t *testing.T) {
	dir := newModule(t)
	orig, _ := os.ReadFile(filepath.Join(dir, "lib.go"))
	res, err := Run(context.Background(), Request{
		Root: dir,
		Edits: []refactor.FileEdit{{
			Path:    "lib.go",
			Content: "package m\n\nfunc Lib() int { return 2 }\n",
		}},
		Test: true,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Success || !res.RolledBack {
		t.Fatalf("want verify-fail + rollback, got %+v", res)
	}
	if res.Error == "" || !strings.Contains(res.Error, "verify failed") {
		t.Fatalf("error should explain the verify failure: %q", res.Error)
	}
	after, err := os.ReadFile(filepath.Join(dir, "lib.go"))
	if err != nil || string(after) != string(orig) {
		t.Fatalf("live tree not restored: %q (err %v)", string(after), err)
	}
	if res.TestOutput == "" || !strings.Contains(res.TestOutput, "FAIL") {
		t.Fatalf("test output should carry the failure: %q", res.TestOutput)
	}
}

func TestApplyNewFileRollsBack(t *testing.T) {
	dir := newModule(t)
	res, err := Run(context.Background(), Request{
		Root: dir,
		Edits: []refactor.FileEdit{{
			Path:    "broken_test.go",
			Content: "package m\n\nimport \"testing\"\n\nfunc TestBroken(t *testing.T) { t.Fatal(\"no\") }\n",
		}},
		Test: true,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Success || !res.RolledBack {
		t.Fatalf("want rollback, got %+v", res)
	}
	if _, err := os.Stat(filepath.Join(dir, "broken_test.go")); !os.IsNotExist(err) {
		t.Fatalf("new file should be removed by rollback (stat err = %v)", err)
	}
}

func TestApplyDryRun(t *testing.T) {
	dir := newModule(t)
	orig, _ := os.ReadFile(filepath.Join(dir, "lib.go"))
	res, err := Run(context.Background(), Request{
		Root: dir,
		Edits: []refactor.FileEdit{{
			Path:    "lib.go",
			Content: "package m\n\nfunc Lib() int { return 9 }\n",
		}},
		Test:   true,
		DryRun: true,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.Success || res.Applied {
		t.Fatalf("dry-run: want success without apply, got %+v", res)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "lib.go"))
	if string(after) != string(orig) {
		t.Fatal("dry-run must not touch the live tree")
	}
}

func TestApplyBuildFailLeavesTreeUntouched(t *testing.T) {
	dir := newModule(t)
	orig, _ := os.ReadFile(filepath.Join(dir, "lib.go"))
	res, err := Run(context.Background(), Request{
		Root: dir,
		Edits: []refactor.FileEdit{{
			Path:    "lib.go",
			Content: "package m\n\nfunc Lib() int { return syntax error here }\n",
		}},
		Test: true,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Success || res.RolledBack {
		t.Fatalf("build-fail: want failure without live rollback, got %+v", res)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "lib.go"))
	if string(after) != string(orig) {
		t.Fatal("build failure must leave the live tree untouched")
	}
}

func TestAffectedPackages(t *testing.T) {
	dir := t.TempDir()
	mk := func(rel string) {
		t.Helper()
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mk("go.mod")
	mk("a/x.go")
	mk("a/b/y.go")
	mk("a/notes.txt") // non-Go: skipped
	mk("vendor/z.go") // no go.mod above within root walk? vendor has none; still under root go.mod
	mk("nested/go.mod")
	mk("nested/n/m.go")
	got := affectedPackages(dir, []string{
		"a/x.go", "a/b/y.go", "a/notes.txt", "a/b/y.go", "vendor/z.go", "nested/n/m.go", "nowhere/deep/q.go",
	})
	want := []string{"./a", "./a/b", "./nowhere/deep", "./vendor"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("affectedPackages = %v, want %v", got, want)
	}
}

func TestRunRejectsEmptyEdits(t *testing.T) {
	if _, err := Run(context.Background(), Request{Root: t.TempDir()}); err == nil {
		t.Fatal("empty edits must be rejected")
	}
}
