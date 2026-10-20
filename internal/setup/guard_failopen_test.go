package setup

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// A global hook must never leave an agent unable to read code: when kern is
// not usable the guard allows the call instead of redirecting to tools that
// do not exist.
func TestKernGuardFailsOpenWhenKernIsNotUsable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX sh guard")
	}
	script := writeGuardScriptFile(t)
	read := `{"tool_name":"Read","tool_input":{"file_path":"/tmp/x/main.go"}}`
	bash := `{"tool_name":"bash","tool_input":{"command":"cat main.go"}}`
	emptyHome := t.TempDir()
	// A PATH with no kern on it and a HOME without ~/.local/bin/kern.
	noKern := []string{"KERN_GUARD_ASSUME_KERN=0", "PATH=/usr/bin:/bin", "HOME=" + emptyHome, "CLAUDE_PROJECT_DIR=" + emptyHome}

	for name, payload := range map[string]string{"read": read, "bash": bash} {
		if code, stderr := runGuard(t, script, payload, noKern); code != 0 {
			t.Errorf("%s: kern missing must fail open, got exit %d stderr %q", name, code, stderr)
		}
	}
}

func TestKernGuardStillBlocksWhenKernIsUsable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX sh guard")
	}
	script := writeGuardScriptFile(t)
	// A fake kern binary at the documented install location.
	home := t.TempDir()
	bin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "kern"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	env := []string{"KERN_GUARD_ASSUME_KERN=0", "PATH=/usr/bin:/bin", "HOME=" + home, "CLAUDE_PROJECT_DIR=" + home}
	read := `{"tool_name":"Read","tool_input":{"file_path":"/tmp/x/main.go"}}`
	if code, _ := runGuard(t, script, read, env); code != 2 {
		t.Errorf("with kern installed the guard must still block code reads, got exit %d", code)
	}
}

func TestKernGuardOffSwitches(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX sh guard")
	}
	script := writeGuardScriptFile(t)
	home := t.TempDir()
	bin := filepath.Join(home, ".local", "bin")
	_ = os.MkdirAll(bin, 0o755)
	_ = os.WriteFile(filepath.Join(bin, "kern"), []byte("#!/bin/sh\n"), 0o755)
	read := `{"tool_name":"Read","tool_input":{"file_path":"/tmp/x/main.go"}}`
	base := []string{"KERN_GUARD_ASSUME_KERN=0", "PATH=/usr/bin:/bin", "HOME=" + home}

	// KERN_DISABLED=1 switches kern off for the process.
	if code, _ := runGuard(t, script, read, append(append([]string{}, base...), "CLAUDE_PROJECT_DIR="+home, "KERN_DISABLED=1")); code != 0 {
		t.Errorf("KERN_DISABLED=1 must allow, got exit %d", code)
	}

	// A .kern/off file switches kern off for ONE repo only.
	off := t.TempDir()
	other := t.TempDir()
	if err := os.MkdirAll(filepath.Join(off, ".kern"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(off, ".kern", "off"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _ := runGuard(t, script, read, append(append([]string{}, base...), "CLAUDE_PROJECT_DIR="+off)); code != 0 {
		t.Errorf(".kern/off must allow in that repo, got exit %d", code)
	}
	if code, _ := runGuard(t, script, read, append(append([]string{}, base...), "CLAUDE_PROJECT_DIR="+other)); code != 2 {
		t.Errorf("a repo without .kern/off must still be guarded, got exit %d", code)
	}
}
