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
