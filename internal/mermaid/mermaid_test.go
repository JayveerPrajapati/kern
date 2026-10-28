package mermaid

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, body := range files {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const srcMain = `package main

type Public struct{}

func (p Public) Run() int { return helper() }

func helper() int { return 1 }

func main() { var p Public; _ = p.Run() }
`

// TestRenderRendersCallersAndCallees pins the Graph(mermaid) output: a symbol
// with a caller and a callee renders both edges as a flowchart.
func TestRenderRendersCallersAndCallees(t *testing.T) {
	ix, err := index.Build(writeTree(t, map[string]string{"main.go": srcMain}))
	if err != nil {
		t.Fatal(err)
	}
	out := Render(ix, "helper")
	if !strings.Contains(out, "flowchart LR") {
		t.Fatalf("missing flowchart header:\n%s", out)
	}
	if !strings.Contains(out, `"helper"`) || !strings.Contains(out, `"Public.Run"`) {
		t.Fatalf("missing caller edge to helper:\n%s", out)
	}
	if got := Render(ix, "no-such-symbol"); got != "" {
		t.Fatalf("unknown symbol rendered %q, want empty", got)
	}
}

// TestRenderStalenessBanner pins the banner contract: editing a cited file
// after the build prepends a mermaid comment.
func TestRenderStalenessBanner(t *testing.T) {
	dir := writeTree(t, map[string]string{"main.go": srcMain})
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	if out := Render(ix, "helper"); strings.Contains(out, "%% ") {
		t.Fatalf("fresh index rendered a stale banner:\n%s", out)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(srcMain+"\n// edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out := Render(ix, "helper"); !strings.Contains(out, "%% ") {
		t.Fatalf("edited index did not render the stale banner:\n%s", out)
	}
}
