package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestG0_BaselineBuildsAndVets enforces the G0 gate (docs/phase-0-baseline.md):
// the module must build and vet cleanly before any gate test runs.
func TestG0_BaselineBuildsAndVets(t *testing.T) {
	root := findRepoRoot(t)

	build := exec.Command("go", "build", "./...")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build ./... failed: %v\n%s", err, out)
	}

	vet := exec.Command("go", "vet", "./...")
	vet.Dir = root
	if out, err := vet.CombinedOutput(); err != nil {
		t.Fatalf("go vet ./... failed: %v\n%s", err, out)
	}
}

// TestG0_OwnershipDocsExist enforces that the G0 ownership documents exist
// and are non-empty.
func TestG0_OwnershipDocsExist(t *testing.T) {
	root := findRepoRoot(t)
	for _, rel := range []string{"README.md"} {
		info, err := os.Stat(filepath.Join(root, rel))
		if err != nil {
			t.Errorf("%s: %v", rel, err)
			continue
		}
		if info.Size() == 0 {
			t.Errorf("%s: empty file", rel)
		}
	}
}

// TestG0_ExitCodeContract enforces the documented exit-code contract
// (spec Section 6): 2 = tool/runtime/configuration error (usage), 3 = invalid
// Blueprint configuration, 4 = unsupported operation or environment.
func TestG0_ExitCodeContract(t *testing.T) {
	binPath := buildBlueprintBinary(t)

	// No args → usage (exit 2).
	if code := runCommandExit(t, binPath); code != 2 {
		t.Errorf("no args: exit = %d, want 2 (usage)", code)
	}

	// Unknown command → exit 4 (unsupported operation).
	if code := runCommandExit(t, binPath, "frobnicate"); code != 4 {
		t.Errorf("unknown command: exit = %d, want 4 (unsupported operation)", code)
	}

	// version → exit 0, output contains "blueprint".
	versionOut := runCommand(t, binPath, "version")
	if !strings.Contains(versionOut, "blueprint") {
		t.Errorf("version output %q does not contain \"blueprint\"", versionOut)
	}

	// Invalid config: temp repo with garbage .blueprint/config.yaml plus a
	// committed file → `blueprint check -repo <repo>` → exit 3.
	dir := t.TempDir()
	runGit(t, dir, "init", "-q", "-b", "main")
	runGit(t, dir, "config", "user.email", "dev@example.com")
	runGit(t, dir, "config", "user.name", "dev")
	writeFile(t, dir, ".blueprint/config.yaml", "::: not yaml")
	writeFile(t, dir, "main.go", "package main\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-qm", "base")
	if code := runCommandExit(t, binPath, "check", "-repo", dir); code != 3 {
		t.Errorf("invalid config: exit = %d, want 3 (invalid Blueprint configuration)", code)
	}
}
