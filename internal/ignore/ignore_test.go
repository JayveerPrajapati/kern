package ignore

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBareNameMatchesAnyDepth(t *testing.T) {
	m := &Matcher{rules: mustRules(t, "*.log")}
	for _, rel := range []string{"app.log", "a/b/c/out.log", "logs/x.log.gz"} {
		if m.Ignored(rel) != (rel != "logs/x.log.gz") {
			t.Errorf("pattern *.log rel=%s -> %v", rel, m.Ignored(rel))
		}
	}
}

func TestDirOnlyExcludesSubtree(t *testing.T) {
	m := &Matcher{rules: mustRules(t, "build/")}
	for _, rel := range []string{"build", "build/x/y.go", "pkg/build/a.go"} {
		if !m.Ignored(rel) {
			t.Errorf("build/ should ignore %s", rel)
		}
	}
	if m.Ignored("src/build.go") {
		t.Error("build/ must not ignore src/build.go")
	}
}

func TestAnchoredPattern(t *testing.T) {
	m := &Matcher{rules: mustRules(t, "/docs/")}
	if !m.Ignored("docs/readme.md") {
		t.Error("/docs/ should ignore docs/readme.md")
	}
	if m.Ignored("sub/docs/readme.md") {
		t.Error("/docs/ must be anchored to root")
	}
}

func TestMultiSegmentAnchoredAtRoot(t *testing.T) {
	m := &Matcher{rules: mustRules(t, "vendor/golang.org/x/")}
	if !m.Ignored("vendor/golang.org/x/net/http.go") {
		t.Error("vendor/golang.org/x/ should ignore that subtree")
	}
	if m.Ignored("src/vendor/golang.org/x/net/http.go") {
		t.Error("mid-slash pattern must anchor at root")
	}
}

func TestNegationReincludes(t *testing.T) {
	m := &Matcher{rules: mustRules(t, "*.min.js", "!keep.min.js")}
	if !m.Ignored("a.min.js") {
		t.Error("*.min.js should ignore a.min.js")
	}
	if m.Ignored("keep.min.js") {
		t.Error("!keep.min.js must re-include keep.min.js")
	}
}

func TestDoubleStarCrossesDirectories(t *testing.T) {
	m := &Matcher{rules: mustRules(t, "a/**/z.go")}
	if !m.Ignored("a/z.go") {
		t.Error("a/**/z.go should match a/z.go (zero segments)")
	}
	if !m.Ignored("a/b/c/z.go") {
		t.Error("a/**/z.go should match a/b/c/z.go")
	}
	if m.Ignored("x/a/z.go") {
		t.Error("a/**/z.go must not match x/a/z.go")
	}
}

func TestCommentsBlankAndEscapes(t *testing.T) {
	if _, ok := parseLine("# comment"); ok {
		t.Error("comment line must not compile")
	}
	if _, ok := parseLine(""); ok {
		t.Error("blank line must not compile")
	}
	m := &Matcher{rules: mustRules(t, "\\#notcomment", "no\\ directory")}
	if !m.Ignored("#notcomment") {
		t.Error("escaped hash should be literal")
	}
	if m.Ignored("comment") {
		t.Error("comments must be skipped")
	}
}

func TestLoadFromFiles(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(".gitignore", "*.tmp\nnode_modules/\n")
	write(".kernignore", "!keep.tmp\n")

	m := Load(dir)
	if !m.Ignored("out.tmp") {
		t.Error(".gitignore *.tmp should ignore out.tmp")
	}
	if m.Ignored("keep.tmp") {
		t.Error(".kernignore !keep.tmp must override .gitignore")
	}
	if !m.Ignored("node_modules/pkg/index.js") {
		t.Error("node_modules/ should ignore subtree")
	}
	if m.Ignored("src/main.go") {
		t.Error("src/main.go must not be ignored")
	}
}

func TestNestedIgnoreScopedToItsDirectory(t *testing.T) {
	dir := t.TempDir()
	mk := func(name, content string) {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mk(".gitignore", "*.log\n")
	mk("sub/.gitignore", "!keep.log\nbuild/\n")

	m := Load(dir)
	// Root rule applies everywhere.
	if !m.Ignored("a/x.log") {
		t.Error("root *.log must ignore a/x.log")
	}
	// Nested negation re-includes within its own subtree only.
	if m.Ignored("sub/keep.log") {
		t.Error("sub/.gitignore !keep.log must re-include sub/keep.log")
	}
	if !m.Ignored("keep.log") {
		t.Error("sub/.gitignore negation must NOT apply outside sub/ (keep.log stays ignored at root)")
	}
	// Nested dir-only rule is scoped to sub/.
	if !m.Ignored("sub/build/out.go") {
		t.Error("sub/.gitignore build/ must ignore sub/build/out.go")
	}
	if m.Ignored("build/out.go") {
		t.Error("sub/.gitignore build/ must NOT apply at root")
	}
}

func TestNestedAnchoredPattern(t *testing.T) {
	dir := t.TempDir()
	mk := func(name, content string) {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mk("pkg/.gitignore", "/vendor/\n")
	m := Load(dir)
	if !m.Ignored("pkg/vendor/x.go") {
		t.Error("/vendor/ in pkg/ must ignore pkg/vendor/x.go")
	}
	if m.Ignored("vendor/x.go") {
		t.Error("/vendor/ in pkg/ must be anchored to pkg/, not root")
	}
}

func TestNegatedCharClass(t *testing.T) {
	m := &Matcher{rules: mustRules(t, "file[!0-9].log")}
	if !m.Ignored("fileA.log") {
		t.Error("file[!0-9].log should ignore fileA.log")
	}
	if m.Ignored("file5.log") {
		t.Error("file[!0-9].log must not ignore file5.log")
	}
	// [^...] is git's synonym for [!...] and must behave identically.
	m2 := &Matcher{rules: mustRules(t, "file[^0-9].log")}
	if !m2.Ignored("fileB.log") {
		t.Error("file[^0-9].log should ignore fileB.log")
	}
	if m2.Ignored("file7.log") {
		t.Error("file[^0-9].log must not ignore file7.log")
	}
}

func mustRules(t *testing.T, lines ...string) []scopedRule {
	t.Helper()
	var out []scopedRule
	for _, l := range lines {
		r, ok := parseLine(l)
		if !ok {
			t.Fatalf("line %q did not compile", l)
		}
		out = append(out, scopedRule{rule: r})
	}
	return out
}
