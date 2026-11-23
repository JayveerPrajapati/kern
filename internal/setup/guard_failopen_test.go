package setup

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
	readFile := filepath.Join(t.TempDir(), "main.go")
	if err := os.WriteFile(readFile, []byte("package main\n\n"+strings.Repeat("// governed code fixture line\n", 120)), 0o644); err != nil {
		t.Fatal(err)
	}
	read := fmt.Sprintf(`{"tool_name":"Read","tool_input":{"file_path":%q}}`, readFile)
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
	if err := os.MkdirAll(filepath.Join(home, ".kern"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".kern", "index.sqlite"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	env := []string{"KERN_GUARD_ASSUME_KERN=0", "PATH=/usr/bin:/bin", "HOME=" + home, "CLAUDE_PROJECT_DIR=" + home}
	readFile := filepath.Join(t.TempDir(), "main.go")
	if err := os.WriteFile(readFile, []byte("package main\n\n"+strings.Repeat("// governed code fixture line\n", 120)), 0o644); err != nil {
		t.Fatal(err)
	}
	read := fmt.Sprintf(`{"tool_name":"Read","tool_input":{"file_path":%q}}`, readFile)
	if code, _ := runGuard(t, script, read, env); code != 2 {
		t.Errorf("with kern installed the guard must still block code reads, got exit %d", code)
	}
}

func TestKernGuardFailsOpenInRepoWithoutKernIndex(t *testing.T) {
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
	project := t.TempDir()
	readFile := filepath.Join(t.TempDir(), "main.go")
	if err := os.WriteFile(readFile, []byte("package main\n\n"+strings.Repeat("// governed code fixture line\n", 120)), 0o644); err != nil {
		t.Fatal(err)
	}
	read := fmt.Sprintf(`{"tool_name":"Read","tool_input":{"file_path":%q}}`, readFile)
	env := []string{"KERN_GUARD_ASSUME_KERN=0", "PATH=/usr/bin:/bin", "HOME=" + home, "CLAUDE_PROJECT_DIR=" + project}
	if code, stderr := runGuard(t, script, read, env); code != 0 {
		t.Fatalf("unmanaged repo must fail open, got exit %d stderr %q", code, stderr)
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
	readFile := filepath.Join(t.TempDir(), "main.go")
	if err := os.WriteFile(readFile, []byte("package main\n\n"+strings.Repeat("// governed code fixture line\n", 120)), 0o644); err != nil {
		t.Fatal(err)
	}
	read := fmt.Sprintf(`{"tool_name":"Read","tool_input":{"file_path":%q}}`, readFile)
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
	if err := os.MkdirAll(filepath.Join(other, ".kern"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, ".kern", "index.sqlite"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _ := runGuard(t, script, read, append(append([]string{}, base...), "CLAUDE_PROJECT_DIR="+other)); code != 2 {
		t.Errorf("a managed repo without .kern/off must still be guarded, got exit %d", code)
	}
}
