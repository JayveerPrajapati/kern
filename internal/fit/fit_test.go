package fit

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFitContextFullSource(t *testing.T) {
	dir := t.TempDir()
	src := `package main

import "fmt"

func Hello() {
	fmt.Println("Hello world")
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := FitContext(context.Background(), Request{
		Root:      dir,
		MaxTokens: 5000,
		Files:     []string{"main.go"},
	})
	if err != nil {
		t.Fatalf("FitContext failed: %v", err)
	}

	if res.Tier != "full" {
		t.Errorf("expected tier full, got %s", res.Tier)
	}
	if !strings.Contains(res.Content, "Hello world") {
		t.Errorf("expected content to contain full source, got:\n%s", res.Content)
	}
}

func TestFitContextFoldedSource(t *testing.T) {
	dir := t.TempDir()
	var bigCode strings.Builder
	bigCode.WriteString("package main\n\n")
	for i := 0; i < 50; i++ {
		bigCode.WriteString("func LargeFunction" + string(rune('A'+i%26)) + "() {\n")
		for j := 0; j < 20; j++ {
			bigCode.WriteString("\tprintln(\"doing work inside body line\")\n")
		}
		bigCode.WriteString("}\n\n")
	}

	if err := os.WriteFile(filepath.Join(dir, "large.go"), []byte(bigCode.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	// Budget is tight (400 tokens), so full source won't fit, but folded signatures will fit
	res, err := FitContext(context.Background(), Request{
		Root:      dir,
		MaxTokens: 400,
		Files:     []string{"large.go"},
	})
	if err != nil {
		t.Fatalf("FitContext failed: %v", err)
	}

	if res.Tier != "folded" && res.Tier != "summary" {
		t.Errorf("expected tier folded or summary, got %s", res.Tier)
	}
	if res.SavingsPct <= 0 {
		t.Errorf("expected savings > 0, got %.2f%%", res.SavingsPct)
	}
}

func TestFitContextEmpty(t *testing.T) {
	dir := t.TempDir()
	res, err := FitContext(context.Background(), Request{
		Root:      dir,
		MaxTokens: 1000,
		Files:     []string{},
	})
	if err != nil {
		t.Fatalf("FitContext failed: %v", err)
	}
	if res.Tier != "empty" {
		t.Errorf("expected tier empty, got %s", res.Tier)
	}
}
