package index

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestUnsafeAutoIndexRoot(t *testing.T) {
	t.Setenv(envAllowBroadIndex, "")
	base := t.TempDir()
	home := filepath.Join(base, "home", "me")
	mk := func(rel string, markers ...string) string {
		dir := filepath.Join(base, rel)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, m := range markers {
			if err := os.WriteFile(filepath.Join(dir, m), nil, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return dir
	}
	mk("home/me")
	docs := mk("home/me/Documents")
	docsProject := mk("home/me/Documents/app", "go.mod")
	gitDocs := mk("home/me/Downloads", ".git")
	code := mk("home/me/code/app")
	parent := filepath.Join(base, "home")

	cases := []struct {
		name   string
		root   string
		unsafe bool
	}{
		{"filesystem root", string(filepath.Separator), true},
		{"home directory", home, true},
		{"directory containing home", parent, true},
		{"documents folder without markers", docs, true},
		{"project inside documents", docsProject, false},
		{"downloads that is a git repo", gitDocs, false},
		{"ordinary project dir without markers", code, false},
	}
	for _, c := range cases {
		got := unsafeAutoIndexRoot(c.root, home)
		if (got != "") != c.unsafe {
			t.Errorf("%s: unsafeAutoIndexRoot(%q) = %q, want unsafe=%v", c.name, c.root, got, c.unsafe)
		}
	}

	t.Setenv(envAllowBroadIndex, "1")
	if got := unsafeAutoIndexRoot(home, home); got != "" {
		t.Errorf("%s=1 must allow, got %q", envAllowBroadIndex, got)
	}
}

// An automatic first build in $HOME must be refused instead of walking the
// user's whole machine, and must leave no index behind.
func TestLoadOrBuildRefusesUnsafeRoot(t *testing.T) {
	t.Setenv(envAllowBroadIndex, "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if err := os.WriteFile(filepath.Join(home, "notes.go"), []byte("package n\nfunc A() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadOrBuild(home)
	var ue *UnsafeRootError
	if !errors.As(err, &ue) {
		t.Fatalf("expected UnsafeRootError for $HOME, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(home, ".kern")); statErr == nil {
		t.Error("a refused build must not create .kern")
	}
}
