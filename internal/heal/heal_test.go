package heal

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// TestParseReplacementsWithFallbackFenced: agent-style replies (prose + a
// fenced code block, no ### FILE: markers) are recovered when exactly one
// failing file is known — the first fenced block replaces it.
func TestParseReplacementsWithFallbackFenced(t *testing.T) {
	text := `Here is the fixed file:

\` + "```go" + `
package main

func Answer() int { return 42 }
` + "```" + `

That should do it.`
	reps := ParseReplacementsWithFallback(text, []string{"main.go"})
	if len(reps) != 1 {
		t.Fatalf("expected 1 replacement from the fenced fallback, got %d: %+v", len(reps), reps)
	}
	if reps[0].Path != "main.go" {
		t.Errorf("path = %q, want main.go", reps[0].Path)
	}
	if !strings.Contains(reps[0].Content, "return 42") {
		t.Errorf("content missing the fix: %q", reps[0].Content)
	}
}

// TestParseReplacementsWithFallbackRules: the fallback stays conservative —
// FILE blocks win; multiple failing files or missing fences remain ambiguous
// (empty result, the caller keeps the loud error).
func TestParseReplacementsWithFallbackRules(t *testing.T) {
	// FILE blocks win over fences: the replacement is the FILE block's
	// content (which may itself contain fence markers as plain text).
	text := "### FILE: a.go\npackage a\n\n```go\nignored\n```"
	reps := ParseReplacementsWithFallback(text, []string{"a.go"})
	if len(reps) != 1 || reps[0].Path != "a.go" || !strings.HasPrefix(reps[0].Content, "package a") {
		t.Fatalf("FILE blocks must win over fences: %+v", reps)
	}
	// Two failing files + one fence: ambiguous.
	if got := ParseReplacementsWithFallback("```go\nx\n```", []string{"a.go", "b.go"}); len(got) != 0 {
		t.Fatalf("ambiguous fallback must be empty, got %+v", got)
	}
	// No fence at all: ambiguous.
	if got := ParseReplacementsWithFallback("just prose", []string{"a.go"}); len(got) != 0 {
		t.Fatalf("prose-only reply must be empty, got %+v", got)
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
	files := failingFiles(root, "main.go:1:1: expected\n./broken.go:3:14: syntax error\nnonexist.go:5: err")
	if len(files) != 1 || files[0] != "broken.go" {
		t.Fatalf("expected only broken.go, got %+v", files)
	}
}

// TestFailingFilesSkipsPackageHeaders pins the go-build header case: the
// `# demo` package line must not fuse with the following file:line (the
// path class excludes newlines), or repair targets — and the P2 gate that
// reads them — go blind on ordinary build failures.
func TestFailingFilesSkipsPackageHeaders(t *testing.T) {
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "hub.go"), []byte("x"), 0o644)
	files := failingFiles(root, "# demo\n./hub.go:3:25: undefined: undefinedName\n")
	if len(files) != 1 || files[0] != "hub.go" {
		t.Fatalf("expected hub.go past the package header, got %+v", files)
	}
}

// TestFailingFilesNeverProbesOutsideRoot verifies absolute paths and ".."
// escapes in tool output are ignored: untrusted output must not become a
// filesystem oracle .
func TestFailingFilesNeverProbesOutsideRoot(t *testing.T) {
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "broken.go"), []byte("x"), 0o644)
	outside := filepath.Join(t.TempDir(), "secret.go")
	_ = os.WriteFile(outside, []byte("x"), 0o644)
	files := failingFiles(root, outside+":1:1: err\n../secret.go:1:1: err\n./broken.go:1:1: err")
	if len(files) != 1 || files[0] != "broken.go" {
		t.Fatalf("expected only broken.go (no absolute/escape probes), got %+v", files)
	}
}

func TestEvaluateCandidates(t *testing.T) {
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "go.mod"), []byte("module sample\n\ngo 1.22\n"), 0o644)
	_ = os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc main() {\n\tinvalidSyntax\n}\n"), 0o644)

	candidates := []Candidate{
		{
			ID:      "cand-1-bad",
			Summary: "invalid repair",
			Replacements: []Replacement{
				{Path: "main.go", Content: "package main\n\nfunc main() {\n\tstillBad(\n}\n"},
			},
		},
		{
			ID:      "cand-2-good",
			Summary: "valid repair",
			Replacements: []Replacement{
				{Path: "main.go", Content: "package main\n\nfunc main() {}\n"},
			},
		},
	}

	best, all := EvaluateCandidates(context.Background(), root, candidates, "", 10*time.Second)
	if best == nil {
		t.Fatal("expected a passing candidate, got nil")
	}
	if best.Candidate.ID != "cand-2-good" {
		t.Fatalf("best candidate ID = %q, want cand-2-good", best.Candidate.ID)
	}
	if len(all) != 2 {
		t.Fatalf("evaluated count = %d, want 2", len(all))
	}
	if !best.Passed {
		t.Fatal("best candidate must have Passed = true")
	}
}
