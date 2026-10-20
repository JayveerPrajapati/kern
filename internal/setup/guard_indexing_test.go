package setup

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// While kern builds the first index of a project it can only answer "still
// building"; the guard must let built-in reads through for that window, and
// must never stay open forever if a build crashed.
func TestKernGuardAllowsWhileFirstIndexBuilds(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX sh guard")
	}
	script := writeGuardScriptFile(t)
	home := t.TempDir()
	bin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "kern"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	read := `{"tool_name":"Read","tool_input":{"file_path":"/tmp/x/main.go"}}`
	envFor := func(project string) []string {
		return []string{"KERN_GUARD_ASSUME_KERN=0", "PATH=/usr/bin:/bin", "HOME=" + home, "CLAUDE_PROJECT_DIR=" + project}
	}

	project := t.TempDir()
	marker := filepath.Join(project, ".kern", "indexing")
	if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
		t.Fatal(err)
	}

	if code, _ := runGuard(t, script, read, envFor(project)); code != 2 {
		t.Fatalf("no marker: guard must block, got exit %d", code)
	}

	if err := os.WriteFile(marker, []byte("1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _ := runGuard(t, script, read, envFor(project)); code != 0 {
		t.Errorf("fresh indexing marker: guard must allow, got exit %d", code)
	}

	old := time.Now().Add(-30 * time.Minute)
	if err := os.Chtimes(marker, old, old); err != nil {
		t.Fatal(err)
	}
	if code, _ := runGuard(t, script, read, envFor(project)); code != 2 {
		t.Errorf("stale marker (crashed build): guard must block again, got exit %d", code)
	}

	// The marker only opens the project it lives in.
	if err := os.Chtimes(marker, time.Now(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if code, _ := runGuard(t, script, read, envFor(t.TempDir())); code != 2 {
		t.Errorf("another project must stay guarded, got exit %d", code)
	}
}
