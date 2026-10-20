package graphcli

import (
	"os"
	"path/filepath"
	"testing"
)

// contextSymbolFixture writes a tiny Go module carrying the given extra
// methods on type Bus, so `kern context` resolution (F12) has symbols to
// fuzzy-match against. (Copy of the cmd/kern test helper, moved with the
// suggestSymbols tests.)
func contextSymbolFixture(t *testing.T, methods string) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"go.mod": "module ctxfuzzy\n\ngo 1.20\n",
		"bus.go": `package main

// Bus is a message bus.
type Bus struct{}

` + methods + `
func main() {
	b := Bus{}
	b.enqueueDeadLetter("boom")
}
`,
	}
	for rel, content := range files {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// TestSuggestSymbolsCaseVariantCamelCase locks F2: `kern explore
// sanitizeDocName` (lookup fails) must suggest the case-variant camelCase
// symbol `SanitizeDocName` — the ranked-search hits added at the top of
// suggestSymbols catch what the anchored Search misses.
func TestSuggestSymbolsCaseVariantCamelCase(t *testing.T) {
	root := contextSymbolFixture(t, `// SanitizeDocName cleans a doc name.
func SanitizeDocName(s string) string { return s }
// sandboxExecPath resolves the sandbox binary.
func sandboxExecPath() string { return "" }
`)
	ix, err := loadOrBuild(root)
	if err != nil {
		t.Fatalf("loadOrBuild: %v", err)
	}
	got := suggestSymbols(ix, "sanitizeDocName")
	found := false
	for _, s := range got {
		if s == "SanitizeDocName" {
			found = true
		}
	}
	if !found {
		t.Fatalf("suggestSymbols(%q) = %v, want SanitizeDocName included", "sanitizeDocName", got)
	}
}

// TestSuggestSymbolsLoadOrBuildIndex keeps a loadOrBuild smoke check in the
// package that owns the helper: building the fixture index must succeed and
// yield the expected symbol.
func TestSuggestSymbolsLoadOrBuildIndex(t *testing.T) {
	root := contextSymbolFixture(t, `// SanitizeDocName cleans a doc name.
func SanitizeDocName(s string) string { return s }
`)
	ix, err := loadOrBuild(root)
	if err != nil {
		t.Fatalf("loadOrBuild: %v", err)
	}
	if ix == nil {
		t.Fatal("loadOrBuild returned a nil index")
	}
	if _, ok := ix.ResolveName("SanitizeDocName"); !ok {
		t.Fatal("built index missing SanitizeDocName")
	}
}
