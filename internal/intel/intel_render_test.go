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
	gitArgs := append([]string{"-c", "gc.auto=0", "-c", "core.fsmonitor=false", "-c", "maintenance.auto=0"}, args...)
	cmd := exec.Command("git", gitArgs...)
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

// TestReviewRangedOverlaySeam pins the optional-overlay contract: an overlay
// renders one extra line per changed file after the blast-radius line, and
// the plain ReviewRanged call site is unaffected (no overlay line).
func TestReviewRangedOverlaySeam(t *testing.T) {
	root := buildTestProject(t)
	ix := buildIndex(t, root)
	changes := []FileChange{{File: "app.go"}}
	plain := ReviewRanged(ix, changes, 4000)
	if strings.Contains(plain, "runtime:") {
		t.Fatalf("plain review contains an overlay line:\n%s", plain)
	}
	withOverlay := ReviewRanged(ix, changes, 4000, func(file string) string {
		if file == "app.go" {
			return "runtime: svc \"demo\" · 2 events · 0.0% errors\n"
		}
		return ""
	})
	if !strings.Contains(withOverlay, `runtime: svc "demo" · 2 events · 0.0% errors`) {
		t.Fatalf("overlay line missing from review:\n%s", withOverlay)
	}
	// Multiple overlays render in order.
	multi := ReviewRanged(ix, changes, 4000,
		func(file string) string { return "first\n" },
		func(file string) string { return "second\n" },
	)
	if !strings.Contains(multi, "first\n") || !strings.Contains(multi, "second\n") {
		t.Fatalf("multi-overlay review missing lines:\n%s", multi)
	}
}

// TestReviewRangedWithLens pins the lensed review contract: the output is the
// standard ReviewRanged body with exactly one "lens: <name> (<priorities>)\n"
// header line prepended (the header the MCP kern_review handler emits), and
// the lensed variant carries overlays through unchanged.
func TestReviewRangedWithLens(t *testing.T) {
	root := buildTestProject(t)
	ix := buildIndex(t, root)
	changes := []FileChange{{File: "app.go"}}
	plain := ReviewRanged(ix, changes, 4000)
	priorities := "policy=1.00, runtime=0.80, graph=0.60, git=0.50, test=0.40, build=0.30, memory=0.20"
	header := "lens: security (" + priorities + ")"
	lensed := ReviewRangedWithLens(ix, changes, 4000, "security", priorities)
	if lensed != header+"\n"+plain {
		t.Fatalf("lensed review != header + plain review:\n--- lensed ---\n%s\n--- plain ---\n%s", lensed, plain)
	}
	if strings.Count(lensed, "lens: security (") != 1 {
		t.Fatalf("lensed review should have exactly one lens header line:\n%s", lensed)
	}
	// Overlays flow through the lensed variant unchanged.
	lensedOverlay := ReviewRangedWithLens(ix, changes, 4000, "security", priorities,
		func(file string) string { return "runtime: svc \"demo\" · 2 events · 0.0% errors\n" },
	)
	if !strings.Contains(lensedOverlay, "runtime: svc \"demo\"") {
		t.Fatalf("lensed review missing overlay line:\n%s", lensedOverlay)
	}
	if !strings.HasPrefix(lensedOverlay, header+"\n") {
		t.Fatalf("lensed overlay review missing header prefix:\n%s", lensedOverlay)
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

func TestDocCommentSingleLineBlock(t *testing.T) {
	dir := t.TempDir()
	src := `package foo

/* one-line doc */
func Bar() {}

/*
 * multi-line doc
 */
func Baz() {}
`
	if err := os.WriteFile(filepath.Join(dir, "app.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	// Bar is on line 4; the single-line block comment above it is its own
	// opening and closing line, so the doc must not overrun into the package
	// clause or blank lines.
	if got := docComment(dir, "app.go", 4); got != "one-line doc" {
		t.Errorf("single-line block doc = %q, want %q", got, "one-line doc")
	}
	if got := docComment(dir, "app.go", 9); got != "multi-line doc" {
		t.Errorf("multi-line block doc = %q, want %q", got, "multi-line doc")
	}
}

func TestWikiExport(t *testing.T) {
	root := buildTestProject(t)
	ix := buildIndex(t, root)
	outDir := t.TempDir()
	files, err := WikiExport(ix, outDir, false)
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

func TestDiscoverSubreposAndFederatedSearch(t *testing.T) {
	parent := t.TempDir()
	sub1 := filepath.Join(parent, "repo-alpha")
	sub2 := filepath.Join(parent, "repo-beta")

	_ = os.MkdirAll(filepath.Join(sub1, ".git"), 0o755)
	_ = os.MkdirAll(filepath.Join(sub2, ".git"), 0o755)
	_ = os.WriteFile(filepath.Join(sub1, "app.go"), []byte("package alpha\nfunc AlphaService() {}\n"), 0o644)
	_ = os.WriteFile(filepath.Join(sub2, "main.go"), []byte("package beta\nfunc BetaProcessor() {}\n"), 0o644)

	repos := DiscoverSubrepos(parent)
	if len(repos) < 2 {
		t.Fatalf("expected at least 2 discovered repos under parent, got %d", len(repos))
	}

	hits := SearchReposIn(parent, "Processor", 10)
	if len(hits) == 0 {
		t.Fatalf("expected search hit from discovered subrepo, got %v", hits)
	}
	if hits[0].Symbol.Name != "BetaProcessor" {
		t.Errorf("expected BetaProcessor hit, got %v", hits[0].Symbol.Name)
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
