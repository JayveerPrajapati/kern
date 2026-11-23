package testfixture

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateCreatesRequestedRepository(t *testing.T) {
	root := Generate(t, RepoSpec{
		Files:          3,
		SymbolsPerFile: 2,
		DeepCallGraph:  true,
		UnicodeNames:   false,
		BinaryFiles:    true,
		Symlinks:       true,
	})

	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("go.mod missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "file000.go")); err != nil {
		t.Fatalf("file000.go missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "binary.dat")); err != nil {
		t.Fatalf("binary.dat missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "symlink.go")); err != nil {
		t.Fatalf("symlink.go missing: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(root, "file000.go"))
	if err != nil {
		t.Fatalf("read file000.go: %v", err)
	}
	text := string(content)
	if !strings.Contains(text, "Symbol0000") || !strings.Contains(text, "Symbol0001") {
		t.Fatalf("expected generated symbols, got:\n%s", text)
	}
	if !strings.Contains(text, "Symbol0001()") {
		t.Fatalf("expected deep call edge, got:\n%s", text)
	}
}

func TestGenerateUnicodeNames(t *testing.T) {
	root := Generate(t, RepoSpec{Files: 1, SymbolsPerFile: 1, UnicodeNames: true})
	content, err := os.ReadFile(filepath.Join(root, "file000.go"))
	if err != nil {
		t.Fatalf("read file000.go: %v", err)
	}
	if !strings.Contains(string(content), "Şymbol0000") {
		t.Fatalf("expected unicode symbol name, got:\n%s", string(content))
	}
}
