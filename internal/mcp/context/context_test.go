package context

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

const sampleGo = `package main

// Hello greets the world.
func Hello(name string) string {
	return "hello " + name
}
`

func writeSample(t *testing.T, dir string) string {
	t.Helper()
	p := filepath.Join(dir, "main.go")
	if err := os.WriteFile(p, []byte(sampleGo), 0o644); err != nil {
		t.Fatalf("write sample: %v", err)
	}
	return p
}

func TestCompactRequiresPath(t *testing.T) {
	_, err := Compact(context.Background(), Hooks{}, map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "path is required") {
		t.Fatalf("expected 'path is required' error, got: %v", err)
	}
}

func TestCompactSummaryTier(t *testing.T) {
	dir := t.TempDir()
	writeSample(t, dir)
	res, err := Compact(context.Background(), Hooks{}, map[string]any{
		"root": dir,
		"path": "main.go",
	})
	if err != nil {
		t.Fatalf("Compact failed: %v", err)
	}
	if !strings.Contains(res, "Hello") {
		t.Errorf("summary tier should name the Hello func, got:\n%s", res)
	}
	if strings.Contains(res, "return \"hello \" + name") {
		t.Errorf("summary tier must not include the body, got:\n%s", res)
	}
}

func TestCompactFullTier(t *testing.T) {
	dir := t.TempDir()
	writeSample(t, dir)
	res, err := Compact(context.Background(), Hooks{}, map[string]any{
		"root": dir,
		"path": "main.go",
		"tier": "full",
	})
	if err != nil {
		t.Fatalf("Compact failed: %v", err)
	}
	if !strings.Contains(res, "return \"hello \" + name") {
		t.Errorf("full tier should include the body, got:\n%s", res)
	}
}

func TestCompactFoldedTier(t *testing.T) {
	dir := t.TempDir()
	writeSample(t, dir)
	res, err := Compact(context.Background(), Hooks{}, map[string]any{
		"root": dir,
		"path": "main.go",
		"tier": "folded",
	})
	if err != nil {
		t.Fatalf("Compact failed: %v", err)
	}
	if !strings.Contains(res, "Hello") {
		t.Errorf("folded tier should keep the signature, got:\n%s", res)
	}
	if strings.Contains(res, "return \"hello \" + name") {
		t.Errorf("folded tier must elide the body, got:\n%s", res)
	}
}

func TestCompactInvalidTier(t *testing.T) {
	dir := t.TempDir()
	writeSample(t, dir)
	_, err := Compact(context.Background(), Hooks{}, map[string]any{
		"root": dir,
		"path": "main.go",
		"tier": "bogus",
	})
	if err == nil {
		t.Fatal("expected an error for an invalid tier, got nil")
	}
}

func TestCompactAbsolutePathViaRoots(t *testing.T) {
	dir := t.TempDir()
	abs := writeSample(t, dir)
	res, err := Compact(context.Background(), Hooks{
		Roots: func() []string { return []string{dir} },
	}, map[string]any{
		"path": abs,
	})
	if err != nil {
		t.Fatalf("Compact with absolute path via Roots hook failed: %v", err)
	}
	if !strings.Contains(res, "Hello") {
		t.Errorf("expected summary of %s, got:\n%s", abs, res)
	}
}

func TestCompactEscapesRoot(t *testing.T) {
	dir := t.TempDir()
	writeSample(t, dir)
	_, err := Compact(context.Background(), Hooks{}, map[string]any{
		"root": dir,
		"path": "../main.go",
	})
	if err == nil || !strings.Contains(err.Error(), "escapes project root") {
		t.Fatalf("expected escapes-project-root error, got: %v", err)
	}
}

func TestCompactCannotRead(t *testing.T) {
	dir := t.TempDir()
	_, err := Compact(context.Background(), Hooks{}, map[string]any{
		"root": dir,
		"path": "nope.go",
	})
	if err == nil || !strings.Contains(err.Error(), "cannot read nope.go") {
		t.Fatalf("expected cannot-read error, got: %v", err)
	}
}

func TestOnboardReportsIndexed(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir()) // isolate the repo registry
	ctx := context.Background()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("# agents\n"), 0o644); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}
	ix := &index.Index{
		Symbols: []index.Symbol{
			{Name: "Hello", Kind: "func", File: "main.go", Line: 3},
			{Name: "main", Kind: "func", File: "main.go", Line: 9},
		},
		Calls: map[string][]index.CallEdge{
			"main": {{Target: "Hello"}},
		},
		FileHashes: map[string]string{
			"main.go": "hash1",
			"util.go": "hash2",
		},
	}
	res, err := Onboard(ctx, Hooks{
		LoadIndex: func(ctx context.Context, root string) (*index.Index, error) { return ix, nil },
	}, map[string]any{"root": dir})
	if err != nil {
		t.Fatalf("Onboard failed: %v", err)
	}
	for _, want := range []string{
		"registered: added",
		"indexed:    2 symbols, 1 call edges, 2 files",
		"AGENTS.md:  present",
	} {
		if !strings.Contains(res, want) {
			t.Errorf("expected %q in Onboard output, got:\n%s", want, res)
		}
	}
	if !strings.Contains(res, "timing:     stale files: 2") {
		t.Errorf("expected stale-files timing line, got:\n%s", res)
	}
}

func TestOnboardLoadIndexError(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir()) // isolate the repo registry
	ctx := context.Background()
	dir := t.TempDir()
	res, err := Onboard(ctx, Hooks{
		LoadIndex: func(ctx context.Context, root string) (*index.Index, error) {
			return nil, os.ErrNotExist
		},
	}, map[string]any{"root": dir})
	if err != nil {
		t.Fatalf("Onboard failed: %v", err)
	}
	if !strings.Contains(res, "indexed:    error:") {
		t.Errorf("expected indexed error line, got:\n%s", res)
	}
}

func TestProjectMapTempDir(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"a.go", "b.go"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("package p\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", f, err)
		}
	}
	res, err := ProjectMap(context.Background(), map[string]any{"root": dir})
	if err != nil {
		t.Fatalf("ProjectMap failed: %v", err)
	}
	for _, want := range []string{"a.go", "b.go"} {
		if !strings.Contains(res, want) {
			t.Errorf("expected %q in project map, got:\n%s", want, res)
		}
	}
}

func TestProjectMapInvalidMaxFiles(t *testing.T) {
	dir := t.TempDir()
	_, err := ProjectMap(context.Background(), map[string]any{
		"root":      dir,
		"max_files": "not-a-number",
	})
	if err == nil || !strings.Contains(err.Error(), "invalid integer") {
		t.Fatalf("expected invalid-integer error, got: %v", err)
	}
}
