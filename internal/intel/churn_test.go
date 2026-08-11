package intel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// newGitRepo creates a disposable git repo with a couple of files and
// commits, returning the repo root.
func newGitRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	execGit(t, root, "init", "-q", "-b", "main")
	execGit(t, root, "config", "user.email", "test@example.com")
	execGit(t, root, "config", "user.name", "Test")
	write := func(rel, content string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.go", "package x\n")
	execGit(t, root, "add", "a.go")
	execGit(t, root, "commit", "-q", "-m", "commit 1")

	write("a.go", "package x\nfunc A() {}\n")
	write("b.go", "package x\nfunc B() {}\n")
	execGit(t, root, "add", ".")
	execGit(t, root, "commit", "-q", "-m", "commit 2")
	return root
}

func TestChurnRange(t *testing.T) {
	root := newGitRepo(t)
	report, err := Churn(root, "", "")
	if err != nil {
		t.Fatalf("Churn: %v", err)
	}
	if report.Commits < 1 {
		t.Errorf("expected at least 1 commit, got %d", report.Commits)
	}
	found := false
	for _, e := range report.Entries {
		if e.File == "a.go" && e.Commits >= 2 {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a.go counted across commits, entries=%v", report.Entries)
	}
	if len(report.From) != 0 {
		t.Errorf("empty range should leave From empty, got %q", report.From)
	}
}

func TestChurnExplicitRange(t *testing.T) {
	root := newGitRepo(t)
	report, err := Churn(root, "HEAD~1", "HEAD")
	if err != nil {
		t.Fatalf("Churn: %v", err)
	}
	if report.From != "HEAD~1" || report.To != "HEAD" {
		t.Errorf("from/to not propagated: %q..%q", report.From, report.To)
	}
	if report.Commits < 1 {
		t.Errorf("expected commits in HEAD~1..HEAD, got %d", report.Commits)
	}
}

func TestChurnNoCommits(t *testing.T) {
	root := t.TempDir()
	report, err := Churn(root, "", "")
	if err == nil {
		t.Fatal("expected error for non-git dir")
	}
	if report != nil {
		t.Errorf("expected nil report on error, got %+v", report)
	}
}

func TestRenderChurn(t *testing.T) {
	report := &ChurnReport{
		From:    "HEAD~2",
		To:      "HEAD",
		Commits: 3,
		Files:   2,
		Entries: []ChurnEntry{
			{File: "a.go", Commits: 2, InWorkingTree: true},
			{File: "b.go", Commits: 1},
		},
	}
	out := RenderChurn(report)
	for _, want := range []string{"HEAD~2..HEAD", "3 commits", "a.go", "[being edited NOW]", "b.go"} {
		if !strings.Contains(out, want) {
			t.Errorf("RenderChurn missing %q in:\n%s", want, out)
		}
	}

	noRange := RenderChurn(&ChurnReport{Commits: 5, Files: 1, Entries: []ChurnEntry{{File: "a.go", Commits: 5}}})
	if !strings.Contains(noRange, "over 5 commits") {
		t.Errorf("empty-range header not rendered: %q", noRange)
	}
}

func TestParseLog(t *testing.T) {
	out := "a.go\nb.go\n\na.go\nb.go\nb.go\n\na.go\n"
	counts, commits := parseLog(out)
	want := map[string]int{"a.go": 3, "b.go": 2}
	if len(counts) != len(want) {
		t.Fatalf("parseLog = %v; want %v", counts, want)
	}
	for f, n := range want {
		if counts[f] != n {
			t.Errorf("parseLog[%q] = %d; want %d", f, counts[f], n)
		}
	}
	if commits != 3 {
		t.Errorf("parseLog commit count = %d; want 3 (three non-empty blocks)", commits)
	}
	// Same file listed twice in one commit counts once.
	once, one := parseLog("a.go\na.go\n")
	if once["a.go"] != 1 {
		t.Errorf("duplicate in same commit counted wrong: %d", once["a.go"])
	}
	if one != 1 {
		t.Errorf("parseLog commit count for a single block = %d; want 1", one)
	}
	// No trailing blank line still counts as one commit.
	if _, n := parseLog("a.go\nb.go\n"); n != 1 {
		t.Errorf("parseLog commit count without trailing blank = %d; want 1", n)
	}
}

func TestFilesOf(t *testing.T) {
	entries := []ChurnEntry{{File: "a.go"}, {File: "b.go"}}
	got := filesOf(entries)
	if len(got) != 2 || got[0] != "a.go" || got[1] != "b.go" {
		t.Errorf("filesOf = %v; want [a.go b.go]", got)
	}
	if len(filesOf(nil)) != 0 {
		t.Error("filesOf(nil) should be empty")
	}
}

func TestTestGaps(t *testing.T) {
	root := buildTestProject(t)
	ix := buildIndex(t, root)
	gaps := TestGaps(ix, 5)
	if len(gaps) > 5 {
		t.Errorf("limit not honored: got %d gaps", len(gaps))
	}
	for _, g := range gaps {
		if g.Symbol == "" || g.File == "" {
			t.Errorf("gap missing symbol/file: %+v", g)
		}
	}
	// Default limit when <= 0.
	if got := TestGaps(ix, 0); len(got) == 0 {
		t.Error("expected some gaps from the buildTestProject index")
	}
}

var _ = index.Index{}
