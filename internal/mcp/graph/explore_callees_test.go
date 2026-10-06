package graph

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// TestExploreQualifiedCallees pins the qualified-callee contract
// (remediation): the main "== callees ==" list carries pkg-qualified names
// ("strings.Join", not a bare "Join") so the agent can disambiguate, and the
// unqualified simple-name echo is gone.
func TestExploreQualifiedCallees(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module demo\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := `package main

import "strings"

// Greet joins a greeting.
func Greet() {
	println(strings.Join([]string{"hi"}, ","))
}
`
	if err := os.WriteFile(filepath.Join(root, "app.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	ix, err := index.Build(root)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Explore(context.Background(), ix, rawGVC(), map[string]any{"root": root, "symbol": "Greet"})
	if err != nil {
		t.Fatal(err)
	}
	// Scope the assertions to the main callee list section (before the
	// source block, which legitimately names strings.Join in code).
	start := strings.Index(out, "== callees")
	end := strings.Index(out, "== blast radius")
	if start < 0 || end < 0 || end <= start {
		t.Fatalf("explore output missing callees/blast-radius sections:\n%s", out)
	}
	section := out[start:end]
	if !strings.Contains(section, "strings.Join") {
		t.Errorf("main callee list must carry the qualified name, got:\n%s", section)
	}
	for _, line := range strings.Split(section, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "Join" || trimmed == "Join [EXTRACTED]" || trimmed == "Join [INFERRED]" {
			t.Errorf("unqualified callee row must not appear, got %q:\n%s", line, section)
		}
	}
}
