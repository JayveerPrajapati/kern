package saferoot

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUnsafeAutoIndexRoot(t *testing.T) {
	t.Setenv(EnvAllowBroadIndex, "")
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

	t.Setenv(EnvAllowBroadIndex, "1")
	if got := unsafeAutoIndexRoot(home, home); got != "" {
		t.Errorf("%s=1 must allow, got %q", EnvAllowBroadIndex, got)
	}
}
