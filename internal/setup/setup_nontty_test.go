package setup

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRunSetupNonTTYDefaultsToDryRun pins the non-interactive apply guard: a
// non-TTY `kern setup` without --yes must preview and write nothing, while an
// explicit --yes still applies. This is the class the audit reproduced (a
// global MCP client running setup in a scratch cwd rewrote managed files).
func TestRunSetupNonTTYDefaultsToDryRun(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	root := t.TempDir()
	if code := RunSetup([]string{"--root", root, "--all-agents", "--plain"}); code != 0 {
		t.Fatalf("setup preview exit = %d, want 0", code)
	}
	if entries, err := os.ReadDir(root); err != nil {
		t.Fatal(err)
	} else if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("a non-TTY setup without --yes must write nothing, found %v", names)
	}

	if code := RunSetup([]string{"--root", root, "--all-agents", "--plain", "--yes"}); code != 0 {
		t.Fatalf("setup --yes exit = %d, want 0", code)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("an explicit --yes must apply the wiring")
	}
}
