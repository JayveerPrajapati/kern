package prose

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

func TestLookup(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module demo\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	appGo := `package main

// Greet says hello.
func Greet() {}

func SecretB() {}
`
	if err := os.WriteFile(filepath.Join(root, "app.go"), []byte(appGo), 0o644); err != nil {
		t.Fatal(err)
	}

	ix, err := index.Build(root)
	if err != nil {
		t.Fatalf("index.Build failed: %v", err)
	}

	// Prose lookup
	out, err := Lookup(ix, "greet", 20)
	if err != nil {
		t.Fatalf("Lookup(greet) error: %v", err)
	}
	if !strings.Contains(out, "Greet") {
		t.Errorf("Lookup(greet) = %q, want Greet", out)
	}

	// Miss
	miss, err := Lookup(ix, "bogusxyz", 20)
	if err != nil {
		t.Fatalf("Lookup(bogusxyz) error: %v", err)
	}
	if miss != "no prose matches: bogusxyz" {
		t.Errorf("Lookup miss = %q, want miss line", miss)
	}

	// Empty query
	if _, err := Lookup(ix, "", 20); err == nil {
		t.Error("expected error on empty query")
	}
}
