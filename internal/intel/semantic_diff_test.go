package intel

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// git runs a git command in dir and fails the test on error.
func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=qa", "GIT_AUTHOR_EMAIL=qa@kern.dev",
		"GIT_COMMITTER_NAME=qa", "GIT_COMMITTER_EMAIL=qa@kern.dev",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// TestSemanticDiffCommentOnlyNotModified locks F-WARN (comment-only edits must
// not flag symbols as Modified): appending a trailing comment after a method
// is doc churn, not a code modification.
func TestSemanticDiffCommentOnlyNotModified(t *testing.T) {
	root := t.TempDir()
	py := filepath.Join(root, "greeter.py")
	base := "class Greeter:\n    def __init__(self, prefix):\n        self.prefix = prefix\n\n    def greet(self, name):\n        return f\"{self.prefix}, {name}\"\n"
	if err := os.WriteFile(py, []byte(base), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, root, "init", "-q")
	git(t, root, "add", "-A")
	git(t, root, "commit", "-qm", "base")

	ix, err := index.Build(root)
	if err != nil {
		t.Fatalf("index build: %v", err)
	}

	// Comment-only append inside the class span (matches the QA repro).
	withComment := base + "\n# trailing comment after the class\n"
	if err := os.WriteFile(py, []byte(withComment), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err := SemanticDiff(ix, root, "", "")
	if err != nil {
		t.Fatalf("semantic diff (comment): %v", err)
	}
	if len(rep.ModifiedSymbols) != 0 {
		names := make([]string, 0, len(rep.ModifiedSymbols))
		for _, m := range rep.ModifiedSymbols {
			names = append(names, m.Symbol)
		}
		t.Fatalf("comment-only edit flagged %d symbols as Modified: %s", len(names), strings.Join(names, ", "))
	}

	// Real code change must still be flagged.
	withCode := strings.Replace(base, "return f\"{self.prefix}, {name}\"",
		"return f\"{self.prefix}, {name}!\"", 1)
	if err := os.WriteFile(py, []byte(withCode), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err = SemanticDiff(ix, root, "", "")
	if err != nil {
		t.Fatalf("semantic diff (code): %v", err)
	}
	found := false
	for _, m := range rep.ModifiedSymbols {
		if strings.HasSuffix(m.Symbol, "greet") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("real code edit not flagged as Modified: %+v", rep.ModifiedSymbols)
	}
}

// TestCommentOnly covers the language table of commentOnly.
func TestCommentOnly(t *testing.T) {
	cases := []struct {
		content, file string
		want          bool
	}{
		{"# trailing", "greeter.py", true},
		{"  # indent", "x.sh", true},
		{"// comment", "x.go", true},
		{"/* block */", "x.ts", true},
		{"return 1 // note", "x.go", false},
		{"x = 1  # note", "x.py", false},
		{"", "x.go", true},
		{"   ", "x.go", true},
		{"code", "unknown.ext", false},
	}
	for _, c := range cases {
		if got := commentOnly(c.content, c.file); got != c.want {
			t.Errorf("commentOnly(%q, %q) = %v, want %v", c.content, c.file, got, c.want)
		}
	}
}
