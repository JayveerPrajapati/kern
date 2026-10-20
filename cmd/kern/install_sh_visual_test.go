package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unicode/utf8"
)

// runInstallSh sources the REAL install.sh (functions only, via the
// documented KERN_SKIP_DISPATCH=1 override) and runs body.
func runInstallSh(t *testing.T, env []string, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("install.sh is a POSIX sh script")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skipf("sh not available: %v", err)
	}
	script := filepath.Join(repoRoot(t), "install.sh")
	cmd := exec.Command("sh", "-c", `KERN_SKIP_DISPATCH=1 . "$KERN_TEST_SCRIPT"; `+body)
	cmd.Env = append(os.Environ(), append([]string{
		"KERN_TEST_SCRIPT=" + script,
		"KERN_INSTALL_DIR=" + t.TempDir(),
		"NO_COLOR=", "KERN_PLAIN=", "KERN_ASCII=", "KERN_AUTO_WIRE=",
	}, env...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("install.sh snippet failed: %v\n%s", err, out)
	}
	return string(out)
}

func utf8Env() []string { return []string{"LC_ALL=en_US.UTF-8", "LANG=en_US.UTF-8"} }

func TestInstallShStepGlyphsFollowLocale(t *testing.T) {
	uni := runInstallSh(t, utf8Env(), `step "hello"`)
	if !strings.Contains(uni, "✔ hello") {
		t.Errorf("UTF-8 locale must keep the historical glyph, got %q", uni)
	}
	for name, env := range map[string][]string{
		"C locale":   {"LC_ALL=C", "LANG=C"},
		"KERN_ASCII": append(utf8Env(), "KERN_ASCII=1"),
	} {
		out := runInstallSh(t, env, `step "hello"; warn "careful"`)
		if strings.ContainsAny(out, "✔⚠✖…") {
			t.Errorf("%s: ascii mode leaked unicode glyphs: %q", name, out)
		}
		if !strings.Contains(out, "ok hello") {
			t.Errorf("%s: want 'ok hello', got %q", name, out)
		}
	}
}

func TestInstallShNoColorWhenNotATerminal(t *testing.T) {
	out := runInstallSh(t, utf8Env(), `step "x"; next_steps`)
	if strings.Contains(out, "\x1b[") {
		t.Errorf("ANSI escapes leaked into non-terminal output: %q", out)
	}
}

func TestInstallShNextStepsBox(t *testing.T) {
	for name, env := range map[string][]string{"unicode": utf8Env(), "ascii": {"LC_ALL=C", "LANG=C"}} {
		out := runInstallSh(t, env, `PATH_WAS_SET=0; next_steps`)
		for _, want := range []string{
			"kern is installed", "Nothing was wired", "kern doctor", "kern setup --dry-run",
			"kern setup ", "kern setup --global-only", "Open a NEW terminal",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("%s: next steps missing %q:\n%s", name, want, out)
			}
		}
		width := -1
		for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
			if !strings.HasPrefix(line, "  ") || strings.TrimSpace(line) == "" {
				continue
			}
			w := utf8.RuneCountInString(line)
			if width == -1 {
				width = w
			}
			if w != width {
				t.Errorf("%s: box row width %d != %d: %q", name, w, width, line)
			}
		}
		if name == "ascii" && strings.ContainsAny(out, "╭╮╰╯─│") {
			t.Errorf("ascii box leaked box-drawing runes:\n%s", out)
		}
	}
	// The PATH hint only appears when the install dir was not already on PATH.
	if out := runInstallSh(t, utf8Env(), `PATH_WAS_SET=1; next_steps`); strings.Contains(out, "Open a NEW terminal") {
		t.Errorf("PATH hint must be hidden when the dir was already on PATH:\n%s", out)
	}
}

// fakeKern is an executable that records its arguments, standing in for the
// freshly installed binary.
func fakeKern(t *testing.T) (bin, log string) {
	t.Helper()
	dir := t.TempDir()
	bin = filepath.Join(dir, "kern")
	log = filepath.Join(dir, "calls.log")
	body := "#!/bin/sh\necho \"$@\" >> \"" + log + "\"\n"
	if err := os.WriteFile(bin, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, log
}

func readLog(t *testing.T, log string) string {
	t.Helper()
	b, err := os.ReadFile(log)
	if err != nil {
		return ""
	}
	return string(b)
}

func TestInstallShOptOutWiresNothing(t *testing.T) {
	bin, log := fakeKern(t)
	out := runInstallSh(t, append(utf8Env(), "KERN_AUTO_WIRE=0"), `wire "`+bin+`" install`)
	if got := readLog(t, log); got != "" {
		t.Errorf("KERN_AUTO_WIRE=0 must not run kern setup, but ran: %q", got)
	}
	if !strings.Contains(out, "kern is installed") || strings.Contains(out, "wired globally") {
		t.Errorf("expected the next-steps box and no wiring, got:\n%s", out)
	}
}

// The default install registers kern ONCE, machine-wide, and writes nothing
// into any repo: no --root, no project flags.
func TestInstallShDefaultWiresGloballyWithoutTouchingRepos(t *testing.T) {
	bin, log := fakeKern(t)
	out := runInstallSh(t, utf8Env(), `wire "`+bin+`" install`)
	got := strings.TrimSpace(readLog(t, log))
	for _, want := range []string{"setup", "--global-only", "--global-rules", "--yes"} {
		if !strings.Contains(got, want) {
			t.Errorf("default install must run setup with %s, ran: %q", want, got)
		}
	}
	for _, banned := range []string{"--root", "--detect"} {
		if strings.Contains(got, banned) {
			t.Errorf("global install must not pass %s (project scope), ran: %q", banned, got)
		}
	}
	if !strings.Contains(out, "kern is wired globally") || !strings.Contains(out, "No repo was touched") {
		t.Errorf("expected the global-wiring summary:\n%s", out)
	}
}

func TestInstallShWiredSummaryBoxIsRectangular(t *testing.T) {
	out := runInstallSh(t, utf8Env(), `PATH_WAS_SET=0; wired_summary`)
	width := -1
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if !strings.HasPrefix(line, "  ") || strings.TrimSpace(line) == "" {
			continue
		}
		w := utf8.RuneCountInString(line)
		if width == -1 {
			width = w
		}
		if w != width {
			t.Errorf("summary box row width %d != %d: %q", w, width, line)
		}
	}
}

func TestInstallShUpgradeRefreshesExistingWiring(t *testing.T) {
	bin, log := fakeKern(t)
	runInstallSh(t, utf8Env(), `wire "`+bin+`" upgrade`)
	if got := readLog(t, log); !strings.Contains(got, "setup") {
		t.Errorf("an upgrade must refresh wiring (plugin/hook assets ship in the binary), ran: %q", got)
	}
}
