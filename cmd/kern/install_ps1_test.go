package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// psFunc returns the source of `function name {...}` up to the next top-level
// function (or EOF). install.ps1 cannot be executed in CI on non-Windows
// hosts, so these static checks guard the structure of the new wiring code.
func psFunc(t *testing.T, src, name string) string {
	t.Helper()
	start := strings.Index(src, "function "+name+" ")
	if start < 0 {
		t.Fatalf("install.ps1: function %s not found", name)
	}
	rest := src[start+1:]
	if next := strings.Index(rest, "\nfunction "); next >= 0 {
		rest = rest[:next]
	}
	return "f" + rest
}

func TestInstallPS1WiringStructure(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(repoRoot(t), "install.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)

	for _, fn := range []string{"Write-Colored", "Write-Step", "Write-Warn", "Write-Header", "Write-Box", "Show-NextSteps", "Show-WiredSummary", "Wire-Kern"} {
		body := psFunc(t, src, fn)
		for _, pair := range [][2]string{{"{", "}"}, {"(", ")"}} {
			if o, c := strings.Count(body, pair[0]), strings.Count(body, pair[1]); o != c {
				t.Errorf("%s: unbalanced %s%s (%d vs %d)", fn, pair[0], pair[1], o, c)
			}
		}
	}

	wire := psFunc(t, src, "Wire-Kern")
	for _, want := range []string{"--global-only", "--global-rules", "--yes", "KERN_AUTO_WIRE", "Show-WiredSummary", "Show-NextSteps"} {
		if !strings.Contains(wire, want) {
			t.Errorf("Wire-Kern must contain %q", want)
		}
	}
	for _, banned := range []string{"--detect", "--root", "setup --detect", "& $kern index", "git rev-parse"} {
		if strings.Contains(wire, banned) {
			t.Errorf("Wire-Kern must not touch the current repo, found %q", banned)
		}
	}

	// Box rows: every literal in the two summary functions must fit the
	// 58-column box, be ASCII, and the PATH hint must be conditional.
	row := regexp.MustCompile(`^\s+"([^"]*)",?\)?\s*(\+.*)?$`)
	for _, fn := range []string{"Show-NextSteps", "Show-WiredSummary"} {
		body := psFunc(t, src, fn)
		rows := 0
		for _, line := range strings.Split(body, "\n") {
			m := row.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			rows++
			if len(m[1]) > 58 {
				t.Errorf("%s: box row longer than 58 columns (%d): %q", fn, len(m[1]), m[1])
			}
			for _, r := range m[1] {
				if r > 126 {
					t.Errorf("%s: non-ASCII character in box row %q", fn, m[1])
					break
				}
			}
		}
		if rows < 5 {
			t.Errorf("%s: parsed only %d box rows; the row regexp or the function changed", fn, rows)
		}
		if !strings.Contains(body, "$PathWasSet") {
			t.Errorf("%s must show the PATH hint only when the dir was not already on PATH", fn)
		}
	}

	// Every wiring call site goes through the same function, and the old
	// "kern buddy" trailer (which implied a per-repo step) is gone.
	if n := strings.Count(src, "& Wire-Kern"); n < 3 {
		t.Errorf("expected Wire-Kern at all install exits, found %d call sites", n)
	}
	if strings.Contains(src, "Run 'kern buddy' for a project onboarding digest") {
		t.Error("stale per-repo trailer still present")
	}
}

// TestInstallPS1UninstallRemovesMachineSecrets pins the uninstall contract:
// %APPDATA%\kern (os.UserConfigDir on Windows: audit-chain key, approval
// signing key/HMAC, consumed ledger) and %LOCALAPPDATA%\kern must be removed
// with -Machine — and, since F4 (release audit 2026-10-28), ONLY with it: the
// default uninstall is prefix-scoped so a scratch-prefix smoke cannot
// destroy another install's machine state.
func TestInstallPS1UninstallRemovesMachineSecrets(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(repoRoot(t), "install.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	body := psFunc(t, string(b), "Uninstall-Kern")
	for _, want := range []string{"APPDATA", "LOCALAPPDATA", `Join-Path $env:APPDATA "kern"`, "machine secrets"} {
		if !strings.Contains(body, want) {
			t.Errorf("Uninstall-Kern must remove machine secrets, missing %q", want)
		}
	}
	for _, want := range []string{"$Machine", "KERN_UNINSTALL_MACHINE", "prefix-scoped"} {
		if !strings.Contains(body, want) {
			t.Errorf("Uninstall-Kern must gate machine cleanup behind -Machine (F4), missing %q", want)
		}
	}
}

// TestInstallPS1NoRemovedKernServer pins P0-5 (2026-10-28 adversarial audit):
// cmd/kern-server does not exist, so the go-install fallback must not install
// it and must not gate the surviving copies on its presence.
func TestInstallPS1NoRemovedKernServer(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(repoRoot(t), "install.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	for _, banned := range []string{"cmd/kern-server", "kern-server.exe"} {
		if strings.Contains(src, banned) {
			t.Errorf("install.ps1 still references removed binary %q", banned)
		}
	}
	goInstall := psFunc(t, src, "Install-Go")
	if strings.Contains(goInstall, `-and (Test-Path (Join-Path $goBin "kern-mcp.exe"))`) {
		t.Error("the old all-three existence gate must be gone")
	}
	for _, want := range []string{`@("kern.exe", "kern-mcp.exe")`, `Test-Path (Join-Path $Prefix "kern.exe")`} {
		if !strings.Contains(goInstall, want) {
			t.Errorf("Install-Go must copy each binary independently, missing %q", want)
		}
	}
}
