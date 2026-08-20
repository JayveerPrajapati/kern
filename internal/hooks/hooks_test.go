package hooks

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallWritesHook(t *testing.T) {
	root := gitInit(t)
	if err := Install(root); err != nil {
		t.Fatal(err)
	}
	hook := filepath.Join(root, ".git", "hooks", "post-commit")
	b, err := os.ReadFile(hook)
	if err != nil {
		t.Fatalf("hook not written: %v", err)
	}
	if !strings.Contains(string(b), "hook store") {
		t.Fatalf("hook missing store command: %s", b)
	}
	fi, _ := os.Stat(hook)
	if fi.Mode()&0o111 == 0 {
		t.Fatal("hook must be executable")
	}
}

func TestInstallRequiresGitRepo(t *testing.T) {
	root := t.TempDir() // no .git, not a repo
	if err := Install(root); err == nil {
		t.Fatal("expected error for non-repo, got nil")
	}
	if _, err := os.Stat(filepath.Join(root, ".git", "hooks", "post-commit")); !os.IsNotExist(err) {
		t.Fatalf("hook must not be created outside a repo: %v", err)
	}
}

func TestInstallDoesNotOverwriteUserHook(t *testing.T) {
	root := gitInit(t)
	hook := filepath.Join(root, ".git", "hooks", "post-commit")
	user := "#!/bin/sh\necho 'user hook'\n"
	if err := os.WriteFile(hook, []byte(user), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Install(root); err == nil {
		t.Fatal("expected error when a user hook already exists, got nil")
	}
	b, err := os.ReadFile(hook)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != user {
		t.Fatalf("user hook was modified: %s", b)
	}
}

func TestInstallOverwritesKernHook(t *testing.T) {
	root := gitInit(t)
	// Install once (creates a kern-managed hook), then re-install should
	// succeed because the existing hook carries the "# kern:" marker.
	if err := Install(root); err != nil {
		t.Fatal(err)
	}
	if err := Install(root); err != nil {
		t.Fatalf("reinstall over kern hook should succeed: %v", err)
	}
	hook := filepath.Join(root, ".git", "hooks", "post-commit")
	b, err := os.ReadFile(hook)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "# kern:") {
		t.Fatalf("hook should still be kern-managed: %s", b)
	}
}

func gitInit(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	cmd := exec.Command("git", "-C", root, "init", "-q")
	if err := cmd.Run(); err != nil {
		t.Fatalf("git init: %v", err)
	}
	return root
}

func TestCompressDiff(t *testing.T) {
	d := `diff --git a/main.go b/main.go
index 1234567..89abcde 100644
--- a/main.go
+++ b/main.go
@@ -1,3 +1,4 @@
-old line
+new line
 unchanged context
`
	out := compressDiff(d)
	if !strings.Contains(out, "## a/main.go b/main.go") {
		t.Fatalf("file header missing: %s", out)
	}
	if !strings.Contains(out, "+new line") || !strings.Contains(out, "-old line") {
		t.Fatalf("added/removed lines missing: %s", out)
	}
	if strings.Contains(out, "index 1234567") {
		t.Fatalf("index line should be dropped: %s", out)
	}
	if strings.Contains(out, "unchanged context") {
		t.Fatalf("context lines should be dropped: %s", out)
	}
	if strings.Contains(out, "--- a/main.go") || strings.Contains(out, "+++ b/main.go") {
		t.Fatalf("a/b headers should be dropped: %s", out)
	}
}
