package commitmsg

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

// gitIn runs `git -C dir <args...>` via the real git binary, failing the test
// on any error (same helper shape as internal/service's git tests).
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// newChangelogRepo initializes a git repo in a temp dir with a user identity
// and one initial commit, returning the repo root.
func newChangelogRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q")
	gitIn(t, dir, "config", "user.name", "changelog-t")
	gitIn(t, dir, "config", "user.email", "t@t")
	gitIn(t, dir, "commit", "--allow-empty", "-qm", "chore(repo): initial commit")
	return dir
}

// TestChangelogGroupsBySubsystemAndType drives a real fixture repo through
// git and asserts the deterministic grouping/ordering contract: subsystems
// sorted by name, fixed type order within a subsystem, empty scope → "other",
// non-conventional commits → other/other, and short-hash bullets.
func TestChangelogGroupsBySubsystemAndType(t *testing.T) {
	root := newChangelogRepo(t)
	// 7 commits after the initial one, newest last in this list.
	messages := []string{
		"feat(api): add login endpoint",
		"feat(api): add signup endpoint",
		"fix(api): retry on 429",
		"feat(db): add migration runner",
		"docs(api): document login",
		"chore: bump deps",      // empty scope → other subsystem
		"merge pull request #1", // non-conventional → other/other
	}
	for _, m := range messages {
		gitIn(t, root, "commit", "--allow-empty", "-qm", m)
	}
	rng := "HEAD~7..HEAD"
	draft, err := Changelog(root, rng)
	if err != nil {
		t.Fatalf("Changelog: %v", err)
	}

	// Header: range + count.
	if !strings.Contains(draft, fmt.Sprintf("Changelog: %s — 7 commits", rng)) {
		t.Errorf("header missing range+count:\n%s", draft)
	}

	// Subsystem sections sorted by name: api, db, other.
	api := strings.Index(draft, "## api")
	db := strings.Index(draft, "## db")
	other := strings.Index(draft, "## other")
	if api < 0 || db < 0 || other < 0 {
		t.Fatalf("expected ## api, ## db, ## other sections:\n%s", draft)
	}
	if !(api < db && db < other) {
		t.Errorf("subsystem sections not sorted (api=%d db=%d other=%d)", api, db, other)
	}

	// Fixed type order within api: feat before fix before docs.
	feat := strings.Index(draft, "### feat")
	fix := strings.Index(draft, "### fix")
	docs := strings.Index(draft, "### docs")
	if !(feat < fix && fix < docs) {
		t.Errorf("types within api not in fixed order (feat=%d fix=%d docs=%d)", feat, fix, docs)
	}

	// Bullets carry the subject and the short hash.
	shortHash := gitIn(t, root, "rev-parse", "--short", "HEAD~3") // feat(db) commit
	if !strings.Contains(draft, "- add migration runner ("+shortHash+")") {
		t.Errorf("missing db bullet with short hash %s:\n%s", shortHash, draft)
	}
	if !strings.Contains(draft, "- add login endpoint (") {
		t.Errorf("missing login bullet:\n%s", draft)
	}

	// Empty scope → "other" subsystem; non-conventional → other/other.
	if !strings.Contains(draft, "- bump deps (") {
		t.Errorf("empty-scope commit missing from ## other:\n%s", draft)
	}
	if !strings.Contains(draft, "- merge pull request #1 (") {
		t.Errorf("non-conventional commit missing from ## other:\n%s", draft)
	}
}

// TestChangelogCapAtMaxCommits creates more than maxChangelogCommits commits
// in ONE git fast-import stream (far faster than thousands of `git commit`
// processes) and asserts the draft stops at the cap.
func TestChangelogCapAtMaxCommits(t *testing.T) {
	root := newChangelogRepo(t)
	branch := gitIn(t, root, "symbolic-ref", "--short", "HEAD")
	from := gitIn(t, root, "rev-parse", "HEAD")

	var sb strings.Builder
	writeCommit := func(mark int, msg, fromRef string) {
		fmt.Fprintf(&sb, "commit refs/heads/%s\nmark :%d\nauthor t <t@t> 0 +0000\ncommitter t <t@t> 0 +0000\ndata %d\n%s\nfrom %s\n",
			branch, mark, len(msg)+1, msg, fromRef)
	}
	writeCommit(1, "init", from)
	for i := 1; i <= maxChangelogCommits+5; i++ {
		writeCommit(i+1, fmt.Sprintf("chore(plumb): commit %d", i), fmt.Sprintf(":%d", i))
	}
	cmd := exec.Command("git", "-C", root, "fast-import", "--quiet")
	cmd.Stdin = strings.NewReader(sb.String())
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fast-import: %v\n%s", err, out)
	}

	draft, err := Changelog(root, "HEAD")
	if err != nil {
		t.Fatalf("Changelog: %v", err)
	}
	if !strings.Contains(draft, fmt.Sprintf("Changelog: HEAD — %d commits", maxChangelogCommits)) {
		t.Errorf("draft not capped at %d commits:\n%.400s", maxChangelogCommits, draft)
	}
	if got := strings.Count(draft, "- commit "); got != maxChangelogCommits {
		t.Errorf("commit bullets = %d, want %d", got, maxChangelogCommits)
	}
}

// TestChangelogErrors pins the clear-error contract: empty range, zero-commit
// range, and non-git root must all return errors (the CLI surfaces them as
// exit 1).
func TestChangelogErrors(t *testing.T) {
	root := newChangelogRepo(t)

	if _, err := Changelog(root, ""); err == nil {
		t.Error("empty range: expected error")
	} else if !strings.Contains(err.Error(), "empty revision range") {
		t.Errorf("empty range error unclear: %v", err)
	}
	if _, err := Changelog(root, "   "); err == nil {
		t.Error("blank range: expected error")
	}

	// A range that matches no commits is an empty result → error.
	if _, err := Changelog(root, "HEAD..HEAD"); err == nil {
		t.Error("zero-commit range: expected error")
	} else if !strings.Contains(err.Error(), "no commits") {
		t.Errorf("zero-commit error unclear: %v", err)
	}

	// Not a git repository → git's own error surfaced clearly.
	plain := t.TempDir()
	if _, err := Changelog(plain, "HEAD~1..HEAD"); err == nil {
		t.Error("non-git root: expected error")
	} else if !strings.Contains(err.Error(), "changelog:") {
		t.Errorf("non-git error unclear: %v", err)
	}
}

// TestParseConventionalSubject pins the parser edge cases.
func TestParseConventionalSubject(t *testing.T) {
	tests := []struct {
		in      string
		typ     string
		scope   string
		subject string
		ok      bool
	}{
		{"feat(api): add login", "feat", "api", "add login", true},
		{"fix: just fix", "fix", "", "just fix", true},
		{"revert(merge): undo change", "revert", "merge", "undo change", true},
		{"feat(api):", "", "", "", false},       // empty subject
		{"feat: ", "", "", "", false},           // empty subject (spaces)
		{"random text", "", "", "", false},      // not conventional
		{"weird(api): nope", "", "", "", false}, // unknown type
		{"feat(api", "broken", "", "", false},   // unterminated scope
	}
	for _, tt := range tests {
		typ, scope, subject, ok := parseConventionalSubject(tt.in)
		if ok != tt.ok {
			t.Errorf("parseConventionalSubject(%q) ok = %v, want %v", tt.in, ok, tt.ok)
			continue
		}
		if !ok {
			continue
		}
		if typ != tt.typ || scope != tt.scope || subject != tt.subject {
			t.Errorf("parseConventionalSubject(%q) = (%q,%q,%q), want (%q,%q,%q)",
				tt.in, typ, scope, subject, tt.typ, tt.scope, tt.subject)
		}
	}
}

// TestParseChangelogLine pins the oneline parse: short hash + subject, and the
// other/other bucket for non-conventional lines.
func TestParseChangelogLine(t *testing.T) {
	c, ok := parseChangelogLine("abcd123 feat(api): add login")
	if !ok {
		t.Fatal("expected parse success")
	}
	if c.Hash != "abcd123" || c.Type != "feat" || c.Scope != "api" || c.Subject != "add login" {
		t.Errorf("parse = %+v", c)
	}

	c, ok = parseChangelogLine("beef456 merge pull request #9")
	if !ok {
		t.Fatal("expected parse success")
	}
	if c.Hash != "beef456" || c.Type != "other" || c.Scope != "other" {
		t.Errorf("non-conventional parse = %+v", c)
	}

	if _, ok := parseChangelogLine(""); ok {
		t.Error("empty line should not parse")
	}
	if _, ok := parseChangelogLine("just-a-hash"); ok {
		t.Error("hash-only line should not parse")
	}
}
