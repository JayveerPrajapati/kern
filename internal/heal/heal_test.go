package heal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/sandbox"
)

func TestParseReplacements(t *testing.T) {
	text := `intro

### FILE: a/b.go
package b
var x = 1

### FILE: c.go
package c
var y = 2
trailing`
	reps := ParseReplacements(text)
	if len(reps) != 2 {
		t.Fatalf("expected 2, got %d: %+v", len(reps), reps)
	}
	if reps[0].Path != "a/b.go" || !strings.Contains(reps[0].Content, "var x = 1") {
		t.Fatalf("bad first replacement: %+v", reps[0])
	}
	if reps[1].Path != "c.go" {
		t.Fatalf("bad second path: %+v", reps[1])
	}
}

func TestParseReplacementsNoBlocks(t *testing.T) {
	if got := ParseReplacements("no blocks here"); len(got) != 0 {
		t.Fatalf("expected none, got %+v", got)
	}
}

func TestApplyWritesFile(t *testing.T) {
	root := t.TempDir()
	if err := Apply(root, []Replacement{{Path: "sub/x.txt", Content: "hi"}}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, "sub", "x.txt"))
	if err != nil || string(b) != "hi" {
		t.Fatalf("read back: %q %v", b, err)
	}
}

func TestApplyRejectsEscape(t *testing.T) {
	root := t.TempDir()
	if err := Apply(root, []Replacement{{Path: "../evil.txt", Content: "x"}}); err == nil {
		t.Fatal("expected escape rejection")
	}
}

func TestSnapshotCopiesTreeSkipsVendor(t *testing.T) {
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "src"), 0o755)
	_ = os.MkdirAll(filepath.Join(root, "node_modules", "pkg"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "go.mod"), []byte("module x\n"), 0o644)
	_ = os.WriteFile(filepath.Join(root, "src", "a.go"), []byte("package a\n"), 0o644)
	_ = os.WriteFile(filepath.Join(root, "node_modules", "pkg", "big.js"), []byte("x"), 0o644)
	snap, err := sandbox.Snapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer snap.Close()
	if _, err := os.Stat(filepath.Join(snap.Tmp(), "go.mod")); err != nil {
		t.Fatal("go.mod missing")
	}
	if _, err := os.Stat(filepath.Join(snap.Tmp(), "src", "a.go")); err != nil {
		t.Fatal("a.go missing")
	}
	if _, err := os.Stat(filepath.Join(snap.Tmp(), "node_modules")); !os.IsNotExist(err) {
		t.Fatal("node_modules should be skipped")
	}
}

func TestFailingFilesExtractsValidPaths(t *testing.T) {
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "broken.go"), []byte("x"), 0o644)
	// chdir not needed; failingFiles uses cwd, so simulate with a temp cwd
	old, _ := os.Getwd()
	_ = os.Chdir(root)
	defer os.Chdir(old)
	files := failingFiles("main.go:1:1: expected\n./broken.go:3:14: syntax error\nnonexist.go:5: err")
	if len(files) != 1 || files[0] != "broken.go" {
		t.Fatalf("expected only broken.go, got %+v", files)
	}
}
