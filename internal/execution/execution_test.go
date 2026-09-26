package execution

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newTempProject writes a minimal valid Go module into a fresh temp dir and
// returns its root. It is used to exercise ExecuteBuild/ExecuteTests and the
// worktree round-trip deterministically.
func newTempProject(t *testing.T, mainBody string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/testproj\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if mainBody == "" {
		mainBody = "package main\nfunc main() {}\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(mainBody), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestNewExecutor(t *testing.T) {
	ex := NewExecutor("/some/root")
	if ex == nil {
		t.Fatal("NewExecutor returned nil")
	}
	if ex.Root() != "/some/root" {
		t.Fatalf("Root() = %q, want %q", ex.Root(), "/some/root")
	}
}

func TestExecuteEcho(t *testing.T) {
	t.Setenv("KERN_ALLOW_UNISOLATED", "1") // fail-closed gate: opt into unisolated runs on hosts without netns (darwin)
	dir := t.TempDir()
	ex := NewExecutor(dir)
	res := ex.Execute("echo", []string{"hello-kern"}, 10*time.Second)

	if !res.OK {
		t.Fatalf("OK = false, want true (err=%v)", res.Err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0", res.ExitCode)
	}
	if !strings.Contains(res.Output, "hello-kern") {
		t.Fatalf("Output = %q, want it to contain %q", res.Output, "hello-kern")
	}
	if res.Duration <= 0 {
		t.Fatalf("Duration = %v, want > 0", res.Duration)
	}
	if res.Restored {
		t.Fatal("Restored = true, want false for a successful run")
	}
	if res.Root != dir {
		t.Fatalf("Root = %q, want %q", res.Root, dir)
	}
}

// TestExecuteGovernanceDenies: a configured executor gate refuses a disallowed
// command before it runs (fail closed).
func TestExecuteGovernanceDenies(t *testing.T) {
	t.Setenv("KERN_ALLOW_UNISOLATED", "1") // fail-closed gate: opt into unisolated runs on hosts without netns (darwin)
	dir := newTempProject(t, "")
	marker := filepath.Join(dir, "marker.txt")

	ex := NewExecutor(dir).WithGovernance(func(command string, args []string) error {
		if command == "sh" {
			return errors.New("governance: command 'sh' is disallowed")
		}
		return nil
	})
	res := ex.Execute("sh", []string{"-c", "touch marker.txt"}, 10*time.Second)

	if res.OK {
		t.Fatal("OK = true, want false for a disallowed command")
	}
	if res.Err == nil {
		t.Fatal("Err = nil, want the governance denial")
	}
	if !strings.Contains(res.Err.Error(), "disallowed") {
		t.Fatalf("Err = %v, want the governance denial message", res.Err)
	}
	// The command must not have run at all.
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("marker present even though the command was denied (err=%v)", err)
	}
}

// TestExecuteGovernanceAllows: a configured gate that approves a command lets
// it run normally.
func TestExecuteGovernanceAllows(t *testing.T) {
	t.Setenv("KERN_ALLOW_UNISOLATED", "1") // fail-closed gate: opt into unisolated runs on hosts without netns (darwin)
	dir := t.TempDir()
	ex := NewExecutor(dir).WithGovernance(func(command string, args []string) error {
		return nil
	})
	res := ex.Execute("echo", []string{"allowed"}, 10*time.Second)
	if !res.OK {
		t.Fatalf("OK = false, want true for an allowed command (err=%v)", res.Err)
	}
	if !strings.Contains(res.Output, "allowed") {
		t.Fatalf("Output = %q, want it to contain %q", res.Output, "allowed")
	}
}

func TestExecuteFailureRollsBack(t *testing.T) {
	t.Setenv("KERN_ALLOW_UNISOLATED", "1") // fail-closed gate: opt into unisolated runs on hosts without netns (darwin)
	dir := newTempProject(t, "")
	marker := filepath.Join(dir, "marker.txt")

	ex := NewExecutor(dir)
	// A command that creates a file and then exits non-zero. The snapshot is
	// taken before the run, so the marker did not exist at snapshot time and
	// the rollback must remove it.
	res := ex.Execute("sh", []string{"-c", "touch marker.txt; exit 1"}, 10*time.Second)

	if res.OK {
		t.Fatal("OK = true, want false for a failing command")
	}
	if res.ExitCode == 0 {
		t.Fatal("ExitCode = 0, want non-zero for a failing command")
	}
	if !res.Restored {
		t.Fatal("Restored = false, want true for a failing command")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("marker still present after rollback, want removed (err=%v)", err)
	}
}

func TestExecuteBuildGoProject(t *testing.T) {
	t.Setenv("KERN_ALLOW_UNISOLATED", "1") // fail-closed gate: opt into unisolated runs on hosts without netns (darwin)
	dir := newTempProject(t, "")
	ex := NewExecutor(dir)
	res := ex.ExecuteBuild(30 * time.Second)

	if !res.OK {
		t.Fatalf("ExecuteBuild failed: OK=%v Err=%v Output=%s", res.OK, res.Err, res.Output)
	}
	if res.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0", res.ExitCode)
	}
	if res.Root != dir {
		t.Fatalf("Root = %q, want %q", res.Root, dir)
	}
}

func TestExecuteBuildNoCommand(t *testing.T) {
	t.Setenv("KERN_ALLOW_UNISOLATED", "1") // fail-closed gate: opt into unisolated runs on hosts without netns (darwin)
	// A dir with neither go.mod nor Makefile => clean detection error.
	dir := t.TempDir()
	ex := NewExecutor(dir)
	res := ex.ExecuteBuild(10 * time.Second)

	if res.OK {
		t.Fatal("OK = true, want false when no build command is detected")
	}
	if res.Err == nil {
		t.Fatal("Err = nil, want a detection error")
	}
	if !strings.Contains(res.Err.Error(), "no build command") {
		t.Fatalf("Err = %v, want 'no build command'", res.Err)
	}
}

func TestExecuteTestsGoProject(t *testing.T) {
	t.Setenv("KERN_ALLOW_UNISOLATED", "1") // fail-closed gate: opt into unisolated runs on hosts without netns (darwin)
	dir := newTempProject(t, "package main\nimport \"testing\"\nfunc TestAlwaysPass(t *testing.T) {}\n")
	ex := NewExecutor(dir)
	res := ex.ExecuteTests(30 * time.Second)

	if !res.OK {
		t.Fatalf("ExecuteTests failed: OK=%v Err=%v Output=%s", res.OK, res.Err, res.Output)
	}
	if res.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0", res.ExitCode)
	}
}

func TestExecuteTestsNoCommand(t *testing.T) {
	t.Setenv("KERN_ALLOW_UNISOLATED", "1") // fail-closed gate: opt into unisolated runs on hosts without netns (darwin)
	dir := t.TempDir()
	ex := NewExecutor(dir)
	res := ex.ExecuteTests(10 * time.Second)

	if res.OK {
		t.Fatal("OK = true, want false when no test command is detected")
	}
	if res.Err == nil {
		t.Fatal("Err = nil, want a detection error")
	}
}

func TestNewWorktreeCopiesAndSkips(t *testing.T) {
	src := newTempProject(t, "")
	// Add files to assert copy + skip behavior.
	if err := os.MkdirAll(filepath.Join(src, "node_modules", "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "node_modules", "pkg", "dep.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(src, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	w, err := NewWorktree(src)
	if err != nil {
		t.Fatalf("NewWorktree: %v", err)
	}
	defer func() { _ = w.Cleanup() }()

	if w.Dir() == "" || w.Dir() == src {
		t.Fatalf("Dir() = %q, want a distinct temp copy", w.Dir())
	}
	if w.SourceRoot() != src {
		t.Fatalf("SourceRoot() = %q, want %q", w.SourceRoot(), src)
	}
	if _, err := os.Stat(filepath.Join(w.Dir(), "main.go")); err != nil {
		t.Fatalf("copied project should contain main.go: %v", err)
	}
	if _, err := os.Stat(filepath.Join(w.Dir(), "go.mod")); err != nil {
		t.Fatalf("copied project should contain go.mod: %v", err)
	}
	if _, err := os.Stat(filepath.Join(w.Dir(), "node_modules")); !os.IsNotExist(err) {
		t.Fatalf("node_modules should be skipped in the worktree (err=%v)", err)
	}
	if _, err := os.Stat(filepath.Join(w.Dir(), ".git")); !os.IsNotExist(err) {
		t.Fatalf(".git should be skipped in the worktree (err=%v)", err)
	}
}

// TestWorktreeDiffSkipsSkippedDirs: the worktree snapshot excludes
// sandbox.SkipDirs (.git, node_modules, ...), so the worktree diff must not
// report those files as deleted — otherwise every execute result diff is
// polluted with VCS-internal noise.
func TestWorktreeDiffSkipsSkippedDirs(t *testing.T) {
	src := newTempProject(t, "")
	// Simulate a repo with VCS metadata and vendored deps in the source tree.
	if err := os.MkdirAll(filepath.Join(src, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(src, "node_modules", "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "node_modules", "pkg", "dep.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	w, err := NewWorktree(src)
	if err != nil {
		t.Fatalf("NewWorktree: %v", err)
	}
	defer func() { _ = w.Cleanup() }()

	// Make a real change so the diff is non-empty.
	mod := filepath.Join(w.Dir(), "main.go")
	if err := os.WriteFile(mod, []byte("package main\nfunc main() { println(\"changed\") }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	diff, err := w.Diff()
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if !strings.Contains(diff, "main.go") {
		t.Fatalf("Diff should mention main.go:\n%s", diff)
	}
	for _, bad := range []string{".git/", "node_modules/"} {
		if strings.Contains(diff, bad) {
			t.Fatalf("Diff must not mention skipped dir %q:\n%s", bad, diff)
		}
	}
}

func TestWorktreeApplyRoundTrip(t *testing.T) {
	src := newTempProject(t, "")
	w1, err := NewWorktree(src)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w1.Cleanup() }()

	// Make changes inside the first worktree: add a new file and modify an
	// existing one, so the round trip exercises both the "a/<rel>" and
	// "b/<rel>" header rewrites.
	added := filepath.Join(w1.Dir(), "added.txt")
	if err := os.WriteFile(added, []byte("new content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mod := filepath.Join(w1.Dir(), "main.go")
	if err := os.WriteFile(mod, []byte("package main\nfunc main() { println(\"changed\") }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	diff, err := w1.Diff()
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if !strings.Contains(diff, "added.txt") {
		t.Fatalf("Diff does not mention added.txt:\n%s", diff)
	}
	if !strings.Contains(diff, "main.go") {
		t.Fatalf("Diff does not mention main.go:\n%s", diff)
	}

	// Apply the patch to a fresh worktree.
	w2, err := NewWorktree(src)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w2.Cleanup() }()

	if err := w2.Apply(diff); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(w2.Dir(), "added.txt"))
	if err != nil {
		t.Fatalf("patched file missing: %v", err)
	}
	if string(got) != "new content\n" {
		t.Fatalf("patched content = %q, want %q", string(got), "new content\n")
	}
	// The pre-existing file should carry the modification from w1.
	gotMain, err := os.ReadFile(filepath.Join(w2.Dir(), "main.go"))
	if err != nil {
		t.Fatalf("modified file missing: %v", err)
	}
	wantMain := "package main\nfunc main() { println(\"changed\") }\n"
	if string(gotMain) != wantMain {
		t.Fatalf("patched main.go = %q, want %q", string(gotMain), wantMain)
	}
}

func TestWorktreeApplyInvalidPatch(t *testing.T) {
	src := newTempProject(t, "")
	w, err := NewWorktree(src)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Cleanup() }()

	if err := w.Apply("this is not a patch\n"); err == nil {
		t.Fatal("Apply accepted a garbage patch, want an error")
	}
}

func TestWorktreeCleanup(t *testing.T) {
	src := newTempProject(t, "")
	w, err := NewWorktree(src)
	if err != nil {
		t.Fatal(err)
	}
	dir := w.Dir()
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("worktree dir missing before cleanup: %v", err)
	}
	if err := w.Cleanup(); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("worktree dir still exists after Cleanup (err=%v)", err)
	}
}

func TestCollectArtifacts(t *testing.T) {
	dir := t.TempDir()
	// Interesting, small files.
	write := func(rel, content string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("patch.diff", "--- a\n+++ b\n")
	write("sub/results.txt", "ok")
	write("report.log", "line")
	// Files that must be excluded.
	write("big.json", strings.Repeat("x", 2*1024*1024)) // > 1 MiB
	write("code.go", "package main")
	write("notes.md", "markdown")
	write("image.png", "binary")

	arts, err := CollectArtifacts(dir)
	if err != nil {
		t.Fatalf("CollectArtifacts: %v", err)
	}

	byName := map[string]Artifact{}
	for _, a := range arts {
		byName[a.Name] = a
	}
	if _, ok := byName["patch.diff"]; !ok {
		t.Errorf("missing patch.diff; got %v", byName)
	}
	if _, ok := byName["results.txt"]; !ok {
		t.Errorf("missing results.txt; got %v", byName)
	}
	if _, ok := byName["big.json"]; ok {
		t.Errorf("big.json (>1MiB) should have been excluded")
	}
	if _, ok := byName["code.go"]; ok {
		t.Errorf("code.go (no matching ext) should have been excluded")
	}
	if _, ok := byName["image.png"]; ok {
		t.Errorf("image.png (no matching ext) should have been excluded")
	}
	if _, ok := byName["notes.md"]; ok {
		t.Errorf("notes.md (no matching ext) should have been excluded")
	}
}

func TestWriteArtifact(t *testing.T) {
	root := t.TempDir()
	a := Artifact{
		Name:    "patch.diff",
		Content: "--- a\n+++ b\n",
		Path:    "patch.diff",
	}
	path, err := WriteArtifact(root, a)
	if err != nil {
		t.Fatalf("WriteArtifact: %v", err)
	}

	want := filepath.Join(root, ".kern", "artifacts", "patch.diff")
	if path != want {
		t.Fatalf("returned path = %q, want %q", path, want)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read written artifact: %v", err)
	}
	if string(data) != a.Content {
		t.Fatalf("written content = %q, want %q", string(data), a.Content)
	}
}

// ---- F1: nested go.mod discovery (bounded, ≤2 levels) ----

// TestGoModuleRoot pins the discovery rules: root go.mod wins; a single
// nested go.mod is found (2 levels deep); multiple nested modules are
// ambiguous and yield ""; a module-less root yields "".
func TestGoModuleRoot(t *testing.T) {
	t.Run("root module", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module m\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := goModuleRoot(dir); got != dir {
			t.Errorf("goModuleRoot = %q, want root %q", got, dir)
		}
	})
	t.Run("nested single module", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "src", "go.mod"), []byte("module m\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := goModuleRoot(dir); got != filepath.Join(dir, "src") {
			t.Errorf("goModuleRoot = %q, want %q", got, filepath.Join(dir, "src"))
		}
	})
	t.Run("nested two levels deep", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, "a", "b"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "a", "b", "go.mod"), []byte("module m\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := goModuleRoot(dir); got != filepath.Join(dir, "a", "b") {
			t.Errorf("goModuleRoot = %q, want %q", got, filepath.Join(dir, "a", "b"))
		}
	})
	t.Run("multiple nested modules ambiguous", func(t *testing.T) {
		dir := t.TempDir()
		for _, sub := range []string{"src1", "src2"} {
			if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, sub, "go.mod"), []byte("module m\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if got := goModuleRoot(dir); got != "" {
			t.Errorf("ambiguous nested modules: goModuleRoot = %q, want \"\"", got)
		}
	})
	t.Run("no module", func(t *testing.T) {
		dir := t.TempDir()
		if got := goModuleRoot(dir); got != "" {
			t.Errorf("goModuleRoot = %q, want \"\"", got)
		}
	})
}

// TestExecuteBuildNestedModule pins F1 in execution: a root without go.mod
// whose module lives in a subdirectory builds inside the discovered module,
// and the result's Root reflects the module directory.
func TestExecuteBuildNestedModule(t *testing.T) {
	t.Setenv("KERN_ALLOW_UNISOLATED", "1") // fail-closed gate: opt into unisolated runs on hosts without netns (darwin)
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "go.mod"), []byte("module example.com/nested\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "main.go"), []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ex := NewExecutor(dir)
	res := ex.ExecuteBuild(30 * time.Second)
	if !res.OK {
		t.Fatalf("nested-module build failed: OK=%v Err=%v Output=%s", res.OK, res.Err, res.Output)
	}
	if res.Root != filepath.Join(dir, "src") {
		t.Errorf("Root = %q, want the nested module dir %q", res.Root, filepath.Join(dir, "src"))
	}
}

// TestExecuteBuildAmbiguousModulesNoCommand: multiple nested go.mod files are
// ambiguous — the executor keeps its existing no-module error rather than
// guessing.
func TestExecuteBuildAmbiguousModulesNoCommand(t *testing.T) {
	dir := t.TempDir()
	for _, sub := range []string{"src1", "src2"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, sub, "go.mod"), []byte("module m\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ex := NewExecutor(dir)
	res := ex.ExecuteBuild(10 * time.Second)
	if res.OK {
		t.Fatal("ambiguous modules must not auto-build")
	}
	if res.Err == nil || !strings.Contains(res.Err.Error(), "no build command") {
		t.Errorf("Err = %v, want a clean detection error", res.Err)
	}
}
