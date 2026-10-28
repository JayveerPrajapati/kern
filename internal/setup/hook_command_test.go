package setup

import (
	"strings"
	"testing"
)

// TestHookCommandWindowsUsesPortableRoot pins P2-8: $CLAUDE_PROJECT_DIR is a
// POSIX expansion and does not expand under cmd.exe/PowerShell, so the Windows
// command passes "." instead. The hook resolves the root from the payload cwd.
func TestHookCommandWindowsUsesPortableRoot(t *testing.T) {
	cmd := hookCommandFor("windows", `C:\Program Files\kern\kern.exe`, "claude-post")
	if strings.Contains(cmd, "$CLAUDE_PROJECT_DIR") {
		t.Fatalf("Windows hook command kept a POSIX env expansion: %q", cmd)
	}
	if !strings.Contains(cmd, `"C:\Program Files\kern\kern.exe"`) {
		t.Fatalf("Windows binary path must be quoted (spaces): %q", cmd)
	}
	if !strings.HasSuffix(cmd, `"."`) || !strings.Contains(cmd, "claude-post") {
		t.Fatalf("Windows hook command must pass a portable root: %q", cmd)
	}
}

func TestHookCommandPOSIXKeepsProjectDir(t *testing.T) {
	cmd := hookCommandFor("linux", "/usr/local/bin/kern", "gemini-after")
	if !strings.Contains(cmd, `"$CLAUDE_PROJECT_DIR"`) {
		t.Fatalf("POSIX hook command must keep the env-var root: %q", cmd)
	}
}
