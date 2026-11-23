package index

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func writeDeltaFixture(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{
		"lib/a.go": `package lib

type Widget struct{ N int }

func NewWidget() Widget { return Widget{} }

func (w Widget) Value() int { return w.N }
`,
		"lib/b.go": `package lib

func helper() int { return 1 }

func Caller() int { return helper() }
`,
		"app/main.go": `package main

import "example/lib"

func main() { _ = lib.NewWidget().Value() }
`,
	}
	writeDeltaFiles(t, root, files)
	return files
}

func writeDeltaFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, body := range files {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func symbolNames(ix *Index) []string {
	out := make([]string, 0, len(ix.Symbols))
	for _, s := range ix.Symbols {
		out = append(out, s.FullName())
	}
	sort.Strings(out)
	return out
}

// TestDeltaSaveEquivalentToFullSave pins the persistence contract: a store
// written by the delta path (one edited file, one added file, one removed
// file) loads identically to the same in-memory index written by a full save.
