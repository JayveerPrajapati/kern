package brief

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

func write(t *testing.T, root, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, filepath.Dir(name)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestBuildIncludesSections(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	write(t, dir, "main.go", `package main

func helper() int { return 1 }

func main() {
	_ = helper()
}
`)
	if err := Warm(dir); err != nil {
		t.Fatal(err)
	}
	out, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"# kern buddy briefing",
		"## Project map",
		"## Index",
		"Languages: go",
		"main.go",
		"## kern savings",
		"## How to use kern",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("briefing missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "{{") {
		t.Fatalf("unfilled placeholders:\n%s", out)
	}
}

func TestBuildEntryPoints(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	write(t, dir, "app.go", `package main

func init() {}

func Run() {}
`)
	if err := Warm(dir); err != nil {
		t.Fatal(err)
	}
	out, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Entry points:") || !strings.Contains(out, "Run") {
		t.Fatalf("entry points missing:\n%s", out)
	}
}

func TestBuildColdSkipsIndexSections(t *testing.T) {
	// A cold cache must never trigger a synchronous full index build; the
	// digest degrades to a hint instead (the kern_buddy ~85s cold-start fix).
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	write(t, dir, "main.go", `package main

func main() {}
`)
	out, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "not built yet") {
		t.Fatalf("expected cold-cache hint, got:\n%s", out)
	}
	if strings.Contains(out, "Languages:") || strings.Contains(out, "Symbols:") {
		t.Fatalf("cold build must not render index sections:\n%s", out)
	}
	// The warm path turns the next build into the full digest.
	if err := Warm(dir); err != nil {
		t.Fatal(err)
	}
	out, err = Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Languages: go") || !strings.Contains(out, "Symbols:") {
		t.Fatalf("warm build must render index sections:\n%s", out)
	}
}

func TestBuildMapBudgetCapped(t *testing.T) {
	// A repo whose project map exceeds the digest budget must drop whole
	// file summaries, keep the later sections, and stay under the MCP
	// output sandbox (24KB default) so the index/architecture render.
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	write(t, dir, "go.mod", "module demo\n\ngo 1.22\n")
	for i := 0; i < 80; i++ {
		write(t, dir, fmt.Sprintf("f%02d.go",
			i), fmt.Sprintf("package main\n\n// file %d with unique filler words for the summary.\n\nfunc F%d() int { return %d }\n", i, i, i))
	}
	if err := Warm(dir); err != nil {
		t.Fatal(err)
	}
	out, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "more files — full map via `kern project_map`") {
		t.Fatalf("expected truncation note:\n%s", out)
	}
	if !strings.Contains(out, "## Index") || !strings.Contains(out, "Symbols:") {
		t.Fatalf("index section must survive the map cap:\n%s", out)
	}
	if len(out) > 24<<10 {
		t.Fatalf("digest %d bytes exceeds the 24KB MCP sandbox", len(out))
	}
}

func TestWarmBuildsAndPersists(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	write(t, dir, "go.mod", "module demo\n\ngo 1.22\n")
	write(t, dir, "main.go", "package main\n\nfunc main() {}\n")
	if err := Warm(dir); err != nil {
		t.Fatal(err)
	}
	ix, err := index.Load(dir)
	if err != nil {
		t.Fatalf("Warm must persist the index: %v", err)
	}
	if ix.Stale() {
		t.Fatal("Warm must produce a fresh index")
	}
	if len(ix.Symbols) == 0 {
		t.Fatal("expected symbols in warmed index")
	}
}

func TestBuildNoGo(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	write(t, dir, "notes.txt", "just notes\n")
	out, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "# kern buddy briefing") {
		t.Fatalf("briefing header missing:\n%s", out)
	}
}
