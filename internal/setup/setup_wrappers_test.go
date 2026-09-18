package setup

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	kernversion "github.com/JayveerPrajapati/kern/internal/version"
)

// TestWrapperFreshnessMismatch verifies the A16 guard: when a stale `kern`
// wrapper on PATH reports a version that differs from the running kern's, the
// setup check surfaces a STALE warning pointing at the refresh action. (The
// check targets kern itself — the standalone blueprint / blueprint-mcp shims
// were decommissioned, their functionality now lives as `kern blueprint`
// subcommands.)
func TestWrapperFreshnessMismatch(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell stub requires a POSIX shell")
	}
	kernversion.Version = "v0.9.5.2" // injected so the mismatch is comparable
	defer func() { kernversion.Version = "dev" }()
	dir := t.TempDir()
	stubVersion(dir, "kern", "v0.9.4")
	t.Setenv("PATH", dir)

	sts := checkWrapperFreshness()
	if len(sts) == 0 {
		t.Fatal("expected a STALE warning for a mismatched kern, got none")
	}
	found := false
	for _, s := range sts {
		if s.Agent == "kern" && strings.Contains(s.Note, "STALE") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected STALE note for kern, got %+v", sts)
	}
}

// TestWrapperFreshnessMatching verifies a healthy deployment stays silent: a
// wrapper reporting the same version as kern contributes no status.
func TestWrapperFreshnessMatching(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell stub requires a POSIX shell")
	}
	dir := t.TempDir()
	stubVersion(dir, "kern", "dev")
	t.Setenv("PATH", dir)
	if sts := checkWrapperFreshness(); len(sts) != 0 {
		t.Fatalf("matching wrapper should stay silent, got %+v", sts)
	}
}

// TestWrapperFreshnessUnreadable verifies an unreadable wrapper version is
// flagged with the refresh hint.
func TestWrapperFreshnessUnreadable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell stub requires a POSIX shell")
	}
	dir := t.TempDir()
	stubVersion(dir, "kern", "")
	t.Setenv("PATH", dir)
	sts := checkWrapperFreshness()
	if len(sts) == 0 {
		t.Fatal("expected a WARN for an unreadable wrapper version")
	}
	for _, s := range sts {
		if s.Agent == "kern" && strings.Contains(s.Note, "WARN") && strings.Contains(s.Note, "refresh") {
			return
		}
	}
	t.Fatalf("expected WARN(…refresh…) for kern, got %+v", sts)
}

// stubVersion writes a shell binary into dir that prints the given second
// token after the program name for its `version` subcommand. An empty version
// writes a stub that fails (mimicking a binary that cannot report a version).
func stubVersion(dir, name, version string) {
	body := "#!/bin/sh\necho " + name + " " + version + "\n"
	if version == "" {
		body = "#!/bin/sh\nexit 1\n"
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		panic(err)
	}
}
