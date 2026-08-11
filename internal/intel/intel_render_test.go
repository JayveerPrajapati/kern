package intel

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

func buildIndex(t *testing.T, root string) *index.Index {
	t.Helper()
	ix, err := index.Build(root)
	if err != nil {
		t.Fatal(err)
	}
	return ix
}

func buildTestProject(t *testing.T) string {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "go.mod"), []byte("module demo\n\ngo 1.22\n"), 0o644)
	src := "package main\n\n// Greet says hello.\nfunc Greet() { println(\"hi\") }\n\n// helper is unused.\nfunc helper() {}\n\nfunc main() { Greet() }\n"
	_ = os.WriteFile(filepath.Join(root, "app.go"), []byte(src), 0o644)
	return root
}

func execGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", args, err, out)
	}
}

func containsStr(s, substr string) bool {
	return strings.Contains(s, substr)
}

func TestRenderArch(t *testing.T) {
	root := buildTestProject(t)
	ix := buildIndex(t, root)
	out := RenderArch(AnalyzeArchitecture(ix))
	if !containsStr(out, "communities") || !containsStr(out, "coupling") {
		t.Fatalf("expected architecture overview, got %q", out)
	}
}

func TestRenderChangesAndReview(t *testing.T) {
	root := buildTestProject(t)
	ix := buildIndex(t, root)
	changes := []FileChange{{File: "app.go"}}
	review := ReviewRanged(ix, changes, 4000)
	if review == "" {
		t.Fatal("expected review output")
	}
	rendered := RenderChanges(AnalyzeChangesRanged(ix, changes))
	if !containsStr(rendered, "app.go") {
		t.Fatalf("expected rendered changes, got %q", rendered)
	}
}

func TestRenderCommunities(t *testing.T) {
	root := buildTestProject(t)
	ix := buildIndex(t, root)
	out := RenderCommunities(Communities(ix))
	if !containsStr(out, "community") && !containsStr(out, "cluster") {
		t.Fatalf("expected communities output, got %q", out)
	}
}

func TestRenderDead(t *testing.T) {
	root := buildTestProject(t)
	ix := buildIndex(t, root)
	out := RenderDead(DeadCode(ix))
	if out == "" {
		t.Fatal("expected dead code render")
	}
}

func TestRenderFlows(t *testing.T) {
	root := buildTestProject(t)
	ix := buildIndex(t, root)
	out := RenderFlows(Flows(ix, 20, 5))
	if !containsStr(out, "entry") {
		t.Fatalf("expected flows from entries, got %q", out)
	}
}

func TestRenderHubsAndBridges(t *testing.T) {
	root := buildTestProject(t)
	ix := buildIndex(t, root)
	hubs := RenderHubs(Hubs(ix, 10))
	if !containsStr(hubs, "hub") {
		t.Fatalf("expected hubs output, got %q", hubs)
	}
	bridges := RenderBridges(Bridges(ix, 15))
	if bridges == "" {
		t.Fatal("expected bridges output")
	}
}

func TestRenderLarge(t *testing.T) {
	root := buildTestProject(t)
	ix := buildIndex(t, root)
	out := RenderLarge(LargeFunctions(ix, 1))
	if !containsStr(out, "lines") {
		t.Fatalf("expected large functions list, got %q", out)
	}
}

func TestRenderPath(t *testing.T) {
	root := buildTestProject(t)
	ix := buildIndex(t, root)
	out := RenderPath(ix, ShortestPath(ix, "main", "Greet"))
	if !containsStr(out, "main") || !containsStr(out, "Greet") {
		t.Fatalf("expected path render, got %q", out)
	}
}

func TestRenderTrace(t *testing.T) {
	root := buildTestProject(t)
	ix := buildIndex(t, root)
	report := Trace(ix, "Greet\npanic in Greet\nGreet\n", "test", 10)
	out := RenderTrace(report)
	if !containsStr(out, "Greet") {
		t.Fatalf("expected trace with resolved symbol Greet, got %q", out)
	}
}

func TestWhyAndFormatWhy(t *testing.T) {
	root := buildTestProject(t)
	ix := buildIndex(t, root)
	info, ok := Why(ix, "Greet")
	if !ok {
		t.Fatal("Why should find Greet")
	}
	out := FormatWhy(info)
	if !containsStr(out, "Greet") || !containsStr(out, "app.go") {
		t.Fatalf("expected why format, got %q", out)
	}
}

func TestWikiExport(t *testing.T) {
	root := buildTestProject(t)
	ix := buildIndex(t, root)
	outDir := t.TempDir()
	files, err := WikiExport(ix, outDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("wiki export should create files")
	}
}

func TestCoverageRender(t *testing.T) {
	root := buildTestProject(t)
	ix := buildIndex(t, root)
	out := AnalyzeCoverage(ix).Render()
	if !containsStr(out, "test") || !containsStr(out, "coverage") {
		t.Fatalf("expected coverage render, got %q", out)
	}
}

func TestReposRegistry(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	reg, err := LoadRepos()
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.Add(dir, "demo"); err != nil {
		t.Fatal(err)
	}
	if err := reg.Save(); err != nil {
		t.Fatal(err)
	}
	reg2, err := LoadRepos()
	if err != nil {
		t.Fatal(err)
	}
	if len(reg2.Repos) == 0 {
		t.Fatal("repo not saved")
	}
	got, ok := reg2.Get("demo")
	if !ok || got.Root != dir {
		t.Fatalf("Get failed: %+v %v", got, ok)
	}
	if !reg2.Remove("demo") {
		t.Fatal("Remove should report true for existing repo")
	}
	if err := reg2.Save(); err != nil {
		t.Fatal(err)
	}
	if _, ok := reg2.Get("demo"); ok {
		t.Fatal("repo not removed")
	}
}

func TestSearchRepos(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module demo\n\ngo 1.22\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "app.go"), []byte("package main\nfunc Foo() {}\n"), 0o644)
	ix := buildIndex(t, dir)
	_ = ix.Save()
	reg, err := LoadRepos()
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.Add(dir, "demo"); err != nil {
		t.Fatal(err)
	}
	if err := reg.Save(); err != nil {
		t.Fatal(err)
	}
	hits := SearchRepos("Foo", 10)
	if len(hits) == 0 {
		t.Fatal("expected search hit in repo")
	}
	fmtRepo := FormatRepoHits(hits)
	if !containsStr(fmtRepo, "Foo") {
		t.Fatalf("expected formatted repo hit, got %q", fmtRepo)
	}
}

func TestFilesForRangeLAndChangedFiles(t *testing.T) {
	root := buildTestProject(t)
	execGit(t, root, "init", "-q")
	execGit(t, root, "config", "user.email", "t@t.t")
	execGit(t, root, "config", "user.name", "t")
	execGit(t, root, "add", ".")
	execGit(t, root, "commit", "-q", "-m", "init")

	// Clean tree -> no working-tree changes.
	changes, err := FilesForRangeL(root, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 0 {
		t.Fatalf("clean tree should have 0 changes, got %d", len(changes))
	}

	// Modify a tracked file -> working-tree diff picks it up.
	_ = os.WriteFile(filepath.Join(root, "app.go"), []byte("package main\nfunc New() {}\n"), 0o644)
	changes, err = FilesForRangeL(root, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) == 0 {
		t.Fatal("expected changed files after modification")
	}
}

func TestChangedFilesAndFilesForRange(t *testing.T) {
	root := buildTestProject(t)
	execGit(t, root, "init", "-q")
	execGit(t, root, "config", "user.email", "t@t.t")
	execGit(t, root, "config", "user.name", "t")
	execGit(t, root, "add", ".")
	execGit(t, root, "commit", "-q", "-m", "init")

	cf, err := ChangedFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(cf) != 0 {
		t.Fatalf("clean tree: %d", len(cf))
	}

	_ = os.WriteFile(filepath.Join(root, "app.go"), []byte("package main\nfunc modified() {}\n"), 0o644)
	cf, err = ChangedFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(cf) == 0 {
		t.Fatal("expected tracked modification to show in diff")
	}
	files, err := FilesForRange(root, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("expected files in working-tree range")
	}
}

func TestParsePorcelain(t *testing.T) {
	out := " M app.go\x00?? new.go\x00 D old.go\x00"
	files := parsePorcelain(out)
	if len(files) != 3 {
		t.Fatalf("expected 3 files, got %d: %v", len(files), files)
	}
}

func TestIntelError(t *testing.T) {
	err := &GitError{Op: "test", Err: fmt.Errorf("boom")}
	if err.Error() == "" {
		t.Fatal("GitError should stringify")
	}
	if !strings.Contains(err.Error(), "test") {
		t.Fatalf("expected Op in error: %s", err.Error())
	}
}
