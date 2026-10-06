package verification

import (
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/verdict"
)

// reuseFuncBody is a non-trivial helper (>= MinCandidateStatements statements,
// with calls so the called-symbol signal is strong) used to build reuse
// fixtures. Duplicating it with only a rename must score ~1.00 similarity.
const reuseFuncBody = `func normalizePath(p string) string {
	trimmed := strings.TrimSpace(p)
	if trimmed == "" {
		return "."
	}
	clean := strings.ReplaceAll(trimmed, "\\", "/")
	if strings.HasPrefix(clean, "/") {
		return clean
	}
	return "/" + clean
}`

// reuseFixture commits a tiny Go module carrying one non-trivial function so
// the tree is clean at HEAD.
func reuseFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitInitFixture(t, dir, map[string]string{
		"go.mod": "module reuse\n\ngo 1.20\n",
		"a.go":   "package reuse\n\nimport \"strings\"\n\n" + reuseFuncBody + "\n",
	})
	return dir
}

// TestVerifyReuseNotGitRepoSkips: no git repo -> Skipped set, OK true, no
// findings.
func TestVerifyReuseNotGitRepoSkips(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"a.go": "package x\n"})
	res := NewEngine(dir).VerifyReuse()
	if !res.OK {
		t.Errorf("a skipped reuse check must stay OK=true, got OK=%v", res.OK)
	}
	if res.Skipped != "reuse skipped: not a git repository" {
		t.Errorf("skip note = %q", res.Skipped)
	}
	if len(res.Findings) != 0 {
		t.Errorf("skipped reuse must have no findings, got %v", res.Findings)
	}
}

// TestVerifyReuseCleanTreePasses: a committed, clean tree is a normal PASS —
// no findings AND no skip.
func TestVerifyReuseCleanTreePasses(t *testing.T) {
	dir := reuseFixture(t)
	res := NewEngine(dir).VerifyReuse()
	if !res.OK {
		t.Errorf("clean tree must stay OK=true, got %v", res.OK)
	}
	if res.Skipped != "" {
		t.Errorf("a clean tree is NOT a skip, got %q", res.Skipped)
	}
	if len(res.Findings) != 0 {
		t.Errorf("clean tree must have no findings, got %v", res.Findings)
	}
}

// TestVerifyReuseFindsDuplicate: a working-tree edit adding a function whose
// body is structurally identical (renamed only) to one in an UNCHANGED file
// must emit a finding with file:line and score, and the overall verdict must
// move from PASS (before the edit) to WARN (after) — never FAIL.
func TestVerifyReuseFindsDuplicate(t *testing.T) {
	dir := reuseFixture(t)

	pre := NewEngine(dir).Verify([]string{"reuse"})
	if pre.Verdict != verdict.VerdictPass {
		t.Fatalf("clean reuse run must be PASS, got %s (summary %s)", pre.Verdict, pre.Summary)
	}

	// New untracked file duplicating normalizePath verbatim (rename only).
	writeTree(t, dir, map[string]string{
		"b.go": "package reuse\n\nimport \"strings\"\n\nfunc normalizePathDup(p string) string {\n" +
			strings.TrimPrefix(reuseFuncBody, "func normalizePath(p string) string {") + "\n",
	})

	res := NewEngine(dir).VerifyReuse()
	if !res.OK {
		t.Errorf("reuse findings must never flip OK, got OK=%v", res.OK)
	}
	if res.Skipped != "" {
		t.Errorf("a dirty tree is not a skip, got %q", res.Skipped)
	}
	if len(res.Findings) != 1 {
		t.Fatalf("expected exactly one finding, got %v", res.Findings)
	}
	f := res.Findings[0]
	for _, want := range []string{
		"possible duplicate:", "b.go:", "normalizePathDup",
		"a.go:", "normalizePath", "(similarity 1.00)",
	} {
		if !strings.Contains(f, want) {
			t.Errorf("finding %q missing %q", f, want)
		}
	}

	post := NewEngine(dir).Verify([]string{"reuse"})
	if post.Verdict != verdict.VerdictWarn {
		t.Errorf("a reuse finding must yield WARN, got %s (summary %s)", post.Verdict, post.Summary)
	}
	if !strings.Contains(post.Summary, "reuse: WARN") {
		t.Errorf("summary must surface the reuse warning, got %q", post.Summary)
	}
}

// TestVerifyReuseTinyFunctionNoFinding: a tiny added function (below the
// duplication scanner's MinCandidateStatements size floor) is never a
// candidate — Similarity returns 0 for it.
func TestVerifyReuseTinyFunctionNoFinding(t *testing.T) {
	dir := reuseFixture(t)
	writeTree(t, dir, map[string]string{
		"c.go": "package reuse\n\nimport \"strings\"\n\nfunc tiny(s string) string {\n\treturn strings.TrimSpace(s)\n}\n",
	})
	res := NewEngine(dir).VerifyReuse()
	if !res.OK {
		t.Errorf("tiny-function run must stay OK=true, got %v", res.OK)
	}
	if len(res.Findings) != 0 {
		t.Errorf("a 1-statement helper must be below the size floor, got %v", res.Findings)
	}
}

// TestVerifyReuseIgnoresTestFiles: _test.go changes are never scanned — they
// contribute no changed files, so the run is a clean pass.
func TestVerifyReuseIgnoresTestFiles(t *testing.T) {
	dir := reuseFixture(t)
	writeTree(t, dir, map[string]string{
		"a_test.go": `package reuse

import "testing"

func TestNormalizePath(t *testing.T) {
	got := normalizePath(" x ")
	if got != "/x" {
		t.Fatalf("got %q", got)
	}
}
`,
	})
	res := NewEngine(dir).VerifyReuse()
	if !res.OK {
		t.Errorf("test-file-only change must stay OK=true, got %v", res.OK)
	}
	if res.Skipped != "" {
		t.Errorf("a test-file-only change is a clean pass, not a skip, got %q", res.Skipped)
	}
	if len(res.Findings) != 0 {
		t.Errorf("_test.go files must never be scanned, got %v", res.Findings)
	}
}
