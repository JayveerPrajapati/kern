package root

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveRoot(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	cwd = filepath.Clean(cwd)

	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty uses cwd", "", cwd},
		{"relative made absolute", "some/rel/path", filepath.Join(cwd, "some/rel/path")},
		{"absolute cleaned", filepath.Join(cwd, "sub", ".."), cwd},
		{"trailing slash cleaned", cwd + "/", cwd},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ResolveRoot(tt.in); got != tt.want {
				t.Errorf("ResolveRoot(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestPlausibleProjectGitDiscovery pins LOW-7: git discovery requires a REAL
// repository (.git directory containing HEAD — git's own work-tree check).
// A planted .git FILE, an empty .git directory, or a .git directory without
// HEAD must not enable the cwd fallback; a real repo (including as an
// ancestor of the queried dir) must.
func TestPlausibleProjectGitDiscovery(t *testing.T) {
	base := t.TempDir()

	realRepo := filepath.Join(base, "realrepo")
	if err := os.MkdirAll(filepath.Join(realRepo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(realRepo, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	gitFile := filepath.Join(base, "gitfile")
	if err := os.MkdirAll(gitFile, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitFile, ".git"), []byte("gitdir: /nowhere\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	emptyGitDir := filepath.Join(base, "emptygit")
	if err := os.MkdirAll(filepath.Join(emptyGitDir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	headlessGitDir := filepath.Join(base, "headless")
	if err := os.MkdirAll(filepath.Join(headlessGitDir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(headlessGitDir, ".git", "config"), []byte("[core]\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		dir  string
		want bool
	}{
		{"real repo (.git dir + HEAD)", realRepo, true},
		{"repo subdir via ancestor discovery", filepath.Join(realRepo, "a", "b"), true},
		{".git FILE (worktree/submodule pointer)", gitFile, false},
		{"empty .git directory", emptyGitDir, false},
		{".git directory without HEAD", headlessGitDir, false},
		{"plain scratch dir", base, false},
		{"empty dir", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := PlausibleProject(tc.dir); got != tc.want {
				t.Errorf("PlausibleProject(%q) = %v, want %v", tc.dir, got, tc.want)
			}
		})
	}
}
