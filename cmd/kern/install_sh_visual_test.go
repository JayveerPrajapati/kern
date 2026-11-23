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

func TestInstallShWireFlagOptsIn(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("install.sh is a POSIX sh script")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skipf("sh not available: %v", err)
	}
	script := filepath.Join(repoRoot(t), "install.sh")
	cmd := exec.Command("sh", "-c", `KERN_SKIP_DISPATCH=1 KERN_AUTO_WIRE= . "$KERN_TEST_SCRIPT" --wire; printf '%s\n' "$WIRE"`)
	cmd.Env = append(os.Environ(), "KERN_TEST_SCRIPT="+script, "KERN_INSTALL_DIR="+t.TempDir())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("install.sh --wire failed: %v\n%s", err, out)
	}
	if strings.TrimSpace(string(out)) != "1" {
		t.Fatalf("--wire must set WIRE=1, got %q", out)
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

// The default install stays read-only outside its prefix; wiring is explicit.
func TestInstallShDefaultDoesNotWireAgents(t *testing.T) {
	bin, log := fakeKern(t)
	out := runInstallSh(t, utf8Env(), `wire "`+bin+`" install`)
	if got := readLog(t, log); got != "" {
		t.Errorf("default install must not run kern setup, but ran: %q", got)
	}
	if !strings.Contains(out, "kern is installed") || strings.Contains(out, "wired globally") {
		t.Errorf("expected the no-wiring next-steps box, got:\n%s", out)
	}
}

// --wire is the one-invocation opt-in for the previous auto-wire behavior.
func TestInstallShWireOptInWiresGloballyWithoutTouchingRepos(t *testing.T) {
	bin, log := fakeKern(t)
	out := runInstallSh(t, append(utf8Env(), "KERN_AUTO_WIRE=1"), `wire "`+bin+`" install`)
	got := strings.TrimSpace(readLog(t, log))
	for _, want := range []string{"setup", "--global-only", "--global-rules", "--yes"} {
		if !strings.Contains(got, want) {
			t.Errorf("opt-in install must run setup with %s, ran: %q", want, got)
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
	runInstallSh(t, append(utf8Env(), "KERN_AUTO_WIRE=1"), `wire "`+bin+`" upgrade`)
	if got := readLog(t, log); !strings.Contains(got, "setup") {
		t.Errorf("an upgrade must refresh wiring (plugin/hook assets ship in the binary), ran: %q", got)
	}
}

// TestInstallShUninstallRemovesMachineSecrets pins the full-uninstall
// contract: the machine-secret directory at os.UserConfigDir
// (/Library/Application Support/kern on macOS, XDG_CONFIG_HOME/kern on
// Linux) is removed by the DEFAULT uninstall (--machine is now just a
// compatibility alias of that default). PATH is stripped of kern so the
// run is deterministic (the direct removal path, not the host binary).
func TestInstallShUninstallRemovesMachineSecrets(t *testing.T) {
	home := t.TempDir()
	xdg := filepath.Join(home, "xdg")
	mac := filepath.Join(home, "Library", "Application Support", "kern")
	for _, d := range []string{mac, filepath.Join(xdg, "kern")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "exec-approval.key"), []byte("secret"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	out := runInstallSh(t, []string{"HOME=" + home, "XDG_CONFIG_HOME=" + xdg, "PATH=/usr/bin:/bin"}, `KERN_UNINSTALL_MACHINE=1 ASSUME_YES=1 cmd_uninstall`)
	for _, d := range []string{mac, filepath.Join(xdg, "kern")} {
		if _, err := os.Stat(d); !os.IsNotExist(err) {
			t.Errorf("the default (--machine-aliased) uninstall must remove %s (stat err=%v); output:\n%s", d, err, out)
		}
	}
	if !strings.Contains(out, "machine secrets") {
		t.Errorf("uninstall output must name the machine-secret removal, got:\n%s", out)
	}
}

// TestInstallShUninstallFullDefaultRemovesAllUserState pins the T-08 default:
// a full uninstall leaves the machine as if kern never existed — ~/.kern,
// machine secrets, the cache, and the ~/AGENTS.md kern block (with non-kern
// content preserved and a backup), without any kern binary on PATH.
func TestInstallShUninstallFullDefaultRemovesAllUserState(t *testing.T) {
	home := t.TempDir()
	xdg := filepath.Join(home, "xdg")
	for _, d := range []string{
		filepath.Join(home, ".kern", "audit"),
		filepath.Join(xdg, "kern"),
		filepath.Join(home, ".cache", "kern"),
	} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(home, ".kern", "index.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "AGENTS.md"), []byte("# my own notes\n\nkeep me\n\n<!-- kern:global-rules begin -->\nkern block\n<!-- kern:global-rules end -->\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := runInstallSh(t, []string{"HOME=" + home, "XDG_CONFIG_HOME=" + xdg, "PATH=/usr/bin:/bin"}, `ASSUME_YES=1 cmd_uninstall`)
	for _, d := range []string{
		filepath.Join(home, ".kern"),
		filepath.Join(xdg, "kern"),
		filepath.Join(home, ".cache", "kern"),
	} {
		if _, err := os.Stat(d); !os.IsNotExist(err) {
			t.Errorf("full uninstall must remove %s (stat err=%v); output:\n%s", d, err, out)
		}
	}
	agents, err := os.ReadFile(filepath.Join(home, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(agents), "keep me") || strings.Contains(string(agents), "kern block") {
		t.Errorf("non-kern AGENTS.md content must survive, kern block must go; now:\n%s\noutput:\n%s", agents, out)
	}
	if _, err := os.Stat(filepath.Join(home, "AGENTS.md.kern-uninstall.bak")); err != nil {
		t.Errorf("AGENTS.md backup must exist (err=%v)", err)
	}
	if !strings.Contains(out, "requires separate project cleanup") {
		t.Errorf("the preview must carry the separate project-cleanup section, got:\n%s", out)
	}
}

// TestInstallShUninstallPrefixOnlyIsScoped pins the CI/scratch mode: it must
// stay prefix-scoped — machine secrets, ~/.kern and ~/AGENTS.md are
// untouched, and PATH rc lines belonging to another prefix (the real
// install's) survive with their markers intact.
func TestInstallShUninstallPrefixOnlyIsScoped(t *testing.T) {
	home := t.TempDir()
	xdg := filepath.Join(home, "xdg")
	mac := filepath.Join(home, "Library", "Application Support", "kern")
	for _, d := range []string{mac, filepath.Join(xdg, "kern"), filepath.Join(home, ".kern")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "exec-approval.key"), []byte("secret"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(home, "AGENTS.md"), []byte("# kern usage rules\nkeep me\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	zshrc := filepath.Join(home, ".zshrc")
	realPrefix := filepath.Join(home, ".local", "bin")
	rcBody := "#!/usr/bin/env zsh\n" +
		"# added by kern installer\nexport PATH=\"" + realPrefix + ":$PATH\"\n" +
		"export EDITOR=vim\n"
	if err := os.WriteFile(zshrc, []byte(rcBody), 0o644); err != nil {
		t.Fatal(err)
	}
	out := runInstallSh(t, []string{"HOME=" + home, "XDG_CONFIG_HOME=" + xdg, "PATH=/usr/bin:/bin"}, `KERN_UNINSTALL_PREFIX_ONLY=1 ASSUME_YES=1 cmd_uninstall`)
	for _, d := range []string{mac, filepath.Join(xdg, "kern"), filepath.Join(home, ".kern")} {
		if _, err := os.Stat(d); os.IsNotExist(err) {
			t.Errorf("--prefix-only must NOT remove %s; output:\n%s", d, out)
		}
	}
	if _, err := os.Stat(filepath.Join(home, "AGENTS.md")); err != nil {
		t.Errorf("--prefix-only must leave ~/AGENTS.md untouched (err=%v); output:\n%s", err, out)
	}
	got, err := os.ReadFile(zshrc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "# added by kern installer") ||
		!strings.Contains(string(got), "export PATH=\""+realPrefix+":$PATH\"") {
		t.Errorf("--prefix-only must preserve another prefix's kern PATH lines; .zshrc now:\n%s\nuninstall output:\n%s", got, out)
	}
	if !strings.Contains(out, "CI/scratch mode") {
		t.Errorf("--prefix-only output should state its narrow scope, got:\n%s", out)
	}

	// Adjacency: the rc carries BOTH prefixes' marker/export pairs — the
	// scratch uninstall (KERN_INSTALL_DIR is the harness temp dir) must
	// remove only its own pair and leave the real prefix's pair intact.
	realPrefixLine := "export PATH=\"" + realPrefix + ":$PATH\""
	scopedBody := `printf '#!/bin/sh\n# added by kern installer\nexport PATH="%s:$PATH"\n# added by kern installer\n' "$KERN_INSTALL_DIR" > "$HOME/.bashrc"` +
		"; printf 'export PATH=\"" + realPrefix + ":$PATH\"\n' >> \"$HOME/.bashrc\"" +
		`; KERN_UNINSTALL_PREFIX_ONLY=1 ASSUME_YES=1 cmd_uninstall >/dev/null; cat "$HOME/.bashrc"`
	scoped := runInstallSh(t, []string{"HOME=" + home, "XDG_CONFIG_HOME=" + xdg, "PATH=/usr/bin:/bin"}, scopedBody)
	if strings.Contains(scoped, "KERN_INSTALL_DIR") {
		t.Errorf("scoped uninstall must remove its own prefix export; .bashrc now:\n%s", scoped)
	}
	if got := strings.Count(scoped, "# added by kern installer"); got != 1 {
		t.Errorf("scoped uninstall must keep exactly the other prefix's marker, got %d; .bashrc:\n%s", got, scoped)
	}
	if !strings.Contains(scoped, realPrefixLine) {
		t.Errorf("scoped uninstall must preserve the real prefix's export line; .bashrc now:\n%s", scoped)
	}
}

func TestInstallShUninstallRemovesGlobalHooksBeforeBinary(t *testing.T) {
	home := t.TempDir()
	xdg := filepath.Join(home, "xdg")
	bin := filepath.Join(home, "bin")
	log := filepath.Join(home, "kern-calls.log")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	// The stub records every invocation (append) so the test also proves the
	// wiring/data removal happens BEFORE the binaries disappear.
	stub := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$KERN_CALL_LOG\"\n"
	if err := os.WriteFile(filepath.Join(bin, "kern"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	out := runInstallSh(t, []string{
		"HOME=" + home,
		"XDG_CONFIG_HOME=" + xdg,
		"PATH=" + bin + ":/usr/bin:/bin",
		"KERN_CALL_LOG=" + log,
	}, `ASSUME_YES=1 cmd_uninstall`)
	calls, err := os.ReadFile(log)
	if err != nil {
		t.Fatalf("installer did not invoke the kern wiring removal; output:\n%s", out)
	}
	if got := strings.TrimSpace(string(calls)); got != "setup --remove --data --yes --plain" {
		t.Fatalf("unexpected kern invocation: %q", got)
	}
	if !strings.Contains(out, "removed kern wiring and user data") {
		t.Fatalf("output missing the wiring/data removal message:\n%s", out)
	}
}
