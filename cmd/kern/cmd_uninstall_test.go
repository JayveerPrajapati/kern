package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/projectstate"
	"github.com/JayveerPrajapati/kern/internal/setup"
)

// cmd_uninstall_test.go — behavioral pins for the `kern uninstall` command:
// the repo-scope path end to end (wiring reversal, no binary phase), the
// dry-run path (no installer/network ever reached), and the usage guard.
// The phase-2 installer delegation runs the REAL embedded script offline —
// no curl, no network (pinned by TestUninstallPrefixOnlyRunsEmbeddedScriptOffline).

func uninstallTestHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, ".gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("KIRO_HOME", "")
	t.Setenv("CONTINUE_GLOBAL_DIR", "")
	return home
}

func TestUninstallProjectOnlyReversesRepoWiring(t *testing.T) {
	uninstallTestHome(t)
	root := t.TempDir()
	for _, d := range []string{filepath.Join(".git", "info"), ".claude", ".cursor", ".agents", ".opencode"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	agents := []string{"opencode", "claude", "cursor"}
	for _, s := range setup.Wire(root, agents, false, false) {
		if s.Path == "" && s.Note != "" && !s.Installed {
			t.Fatalf("wire step %s failed: %s", s.Agent, s.Note)
		}
	}
	mcp := filepath.Join(root, ".mcp.json")
	if _, err := os.Stat(mcp); err != nil {
		t.Fatal("sanity: wiring did not write .mcp.json")
	}

	var code int
	out := captureStdout(t, func() { code = runUninstall([]string{"--project-only", "--yes", "--plain", "--root", root}) })
	if code != 0 {
		t.Fatalf("exit = %d\n%s", code, out)
	}
	if _, err := os.Stat(mcp); err == nil {
		t.Error(".mcp.json should be removed by the repo-scope uninstall")
	}
	if !strings.Contains(out, "repo wiring only") {
		t.Errorf("output should state the binaries were untouched:\n%s", out)
	}
	// The user-level config was never wired in this test, so phase 1 found
	// nothing there — and that must not fail the command.
	if !strings.Contains(out, "Nothing to remove") && !strings.Contains(out, "Removed") {
		t.Errorf("phase 1 should report its removal summary:\n%s", out)
	}
}

func TestUninstallDryRunNeverReachesInstaller(t *testing.T) {
	uninstallTestHome(t)
	root := t.TempDir()

	var code int
	out := captureStdout(t, func() { code = runUninstall([]string{"--dry-run", "--yes", "--plain", "--root", root}) })
	if code != 0 {
		t.Fatalf("exit = %d\n%s", code, out)
	}
	if !strings.Contains(out, "[dry-run] would run the embedded install.sh uninstall") {
		t.Errorf("dry-run should name what phase 2 would do:\n%s", out)
	}
	if strings.Contains(out, "curl is required") || strings.Contains(out, "could not fetch") {
		t.Errorf("dry-run must not attempt the installer fetch:\n%s", out)
	}
}

func TestUninstallRejectsPositional(t *testing.T) {
	uninstallTestHome(t)
	errOut := captureStderr(t, func() {
		assertExitCode(t, 2, func() {
			_ = runUninstall([]string{"everything", "--yes"})
		})
	})
	if !strings.Contains(errOut, "unexpected argument") {
		t.Errorf("usage error should name the argument:\n%s", errOut)
	}
}

// Phase 2 runs the REAL embedded installer script offline — the tests set
// a temporary HOME (and KERN_INSTALL_DIR), so the script's rm -f and PATH
// surgery only ever touch the test sandbox.

// TestUninstallPrefixOnlyRunsEmbeddedScriptOffline pins the offline
// contract end to end: phase 2 runs the EMBEDDED installer script, so it
// must succeed with the script URL pointed at an unroutable address — the
// old fetch-and-run design failed exactly here (a 502 from the release
// host bricked uninstall). The run must also do real work: remove the
// prefix's binaries and its PATH rc lines while a foreign PATH line and
// another prefix's kern line survive.
func TestUninstallPrefixOnlyRunsEmbeddedScriptOffline(t *testing.T) {
	home := uninstallTestHome(t)
	prefix := t.TempDir()
	t.Setenv("KERN_INSTALL_DIR", prefix)
	t.Setenv("KERN_INSTALL_SCRIPT_URL", "http://127.0.0.1:1/unroutable-install.sh")

	// Fake binaries at the prefix — the script must remove both.
	for _, b := range []string{"kern", "kern-mcp"} {
		p := filepath.Join(prefix, b)
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// An rc file with three PATH lines: a foreign one, this prefix's kern
	// pair (marker + export), and another prefix's kern pair — only the
	// middle pair may be removed.
	rc := filepath.Join(home, ".zshrc")
	rcBody := "export PATH=\"/usr/foreign/bin:$PATH\"\n" +
		"# added by kern installer\n" +
		"export PATH=\"" + prefix + ":$PATH\"\n" +
		"# added by kern installer\n" +
		"export PATH=\"/opt/other-kern/bin:$PATH\"\n"
	if err := os.WriteFile(rc, []byte(rcBody), 0o644); err != nil {
		t.Fatal(err)
	}

	var code int
	out := captureStdout(t, func() { code = runUninstall([]string{"--prefix-only", "--yes", "--plain", "--root", t.TempDir()}) })
	if code != 0 {
		t.Fatalf("offline --prefix-only must succeed (exit %d):\n%s", code, out)
	}
	if strings.Contains(out, "curl") {
		t.Errorf("phase 2 must not involve curl:\n%s", out)
	}
	for _, b := range []string{"kern", "kern-mcp"} {
		if _, err := os.Stat(filepath.Join(prefix, b)); !os.IsNotExist(err) {
			t.Errorf("the embedded script must remove %s from the prefix (stat err=%v)", b, err)
		}
	}
	after, err := os.ReadFile(rc)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(after), prefix) {
		t.Errorf("this prefix's PATH line must be removed, got:\n%s", after)
	}
	if !strings.Contains(string(after), "/usr/foreign/bin") || !strings.Contains(string(after), "/opt/other-kern/bin") {
		t.Errorf("foreign and other-prefix PATH lines must survive, got:\n%s", after)
	}
}

// TestUninstallPrefixOnlyPromptAnsweredViaStdin pins the stdin wiring of
// phase 2: the embedded script's own consent prompt reads the child's
// stdin, which must be the invoking terminal (or pipe). Without
// install.Stdin = os.Stdin the child saw EOF and aborted with "nothing
// removed" even when the user answered y.
func TestUninstallPrefixOnlyPromptAnsweredViaStdin(t *testing.T) {
	home := uninstallTestHome(t)
	prefix := t.TempDir()
	t.Setenv("KERN_INSTALL_DIR", prefix)

	for _, b := range []string{"kern", "kern-mcp"} {
		if err := os.WriteFile(filepath.Join(prefix, b), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	rc := filepath.Join(home, ".zshrc")
	rcBody := "# added by kern installer\nexport PATH=\"" + prefix + ":$PATH\"\n"
	if err := os.WriteFile(rc, []byte(rcBody), 0o644); err != nil {
		t.Fatal(err)
	}

	// No --yes: the script must prompt, and the answer arrives on stdin.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = saved }()
	if _, err := w.WriteString("y\n"); err != nil {
		t.Fatal(err)
	}
	w.Close()

	var code int
	out := captureStdout(t, func() { code = runUninstall([]string{"--prefix-only", "--plain", "--root", t.TempDir()}) })
	if code != 0 {
		t.Fatalf("prompted --prefix-only must succeed when answered y (exit %d):\n%s", code, out)
	}
	if strings.Contains(out, "aborted — nothing removed") {
		t.Errorf("the script prompt must be reachable via stdin, got the EOF abort:\n%s", out)
	}
	for _, b := range []string{"kern", "kern-mcp"} {
		if _, err := os.Stat(filepath.Join(prefix, b)); !os.IsNotExist(err) {
			t.Errorf("answering the prompt must remove %s (stat err=%v)", b, err)
		}
	}
}

// TestUninstallDefaultRemovesMachineData pins the T-08 full default: phase 1
// removes ~/.kern, machine secrets, and the cache before phase 2 touches
// binaries.
func TestUninstallDefaultRemovesMachineData(t *testing.T) {
	home := uninstallTestHome(t)
	kernHome := filepath.Join(home, ".kern")
	if err := os.MkdirAll(filepath.Join(kernHome, "audit"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(kernHome, "index.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	var code int
	out := captureStdout(t, func() { code = runUninstall([]string{"--yes", "--plain", "--root", t.TempDir()}) })
	if code != 0 {
		t.Fatalf("exit = %d\n%s", code, out)
	}
	if _, err := os.Stat(kernHome); !os.IsNotExist(err) {
		t.Errorf("~/.kern must be removed by the default uninstall (stat err=%v)", err)
	}
	if !strings.Contains(out, "user data") {
		t.Errorf("output must name the user-data removal:\n%s", out)
	}
}

// TestUninstallPrefixOnlySkipsPhase1 pins the CI/scratch mode: no wiring or
// data removal — only the binary/PATH phase.
func TestUninstallPrefixOnlySkipsPhase1(t *testing.T) {
	home := uninstallTestHome(t)
	kernHome := filepath.Join(home, ".kern")
	if err := os.MkdirAll(kernHome, 0o755); err != nil {
		t.Fatal(err)
	}

	var code int
	out := captureStdout(t, func() { code = runUninstall([]string{"--prefix-only", "--dry-run", "--yes", "--plain"}) })
	if code != 0 {
		t.Fatalf("exit = %d\n%s", code, out)
	}
	if strings.Contains(out, "Nothing to remove") || strings.Contains(out, "Removed kern") {
		t.Errorf("--prefix-only must not run the wiring/data phase:\n%s", out)
	}
	if !strings.Contains(out, "[dry-run] would run the embedded install.sh uninstall --prefix-only") {
		t.Errorf("--prefix-only dry-run must name its (binary-only) scope:\n%s", out)
	}
	if _, err := os.Stat(kernHome); err != nil {
		t.Fatalf("~/.kern must survive --prefix-only (err=%v)", err)
	}
}

// TestUninstallManagedProjectPrintsCommand pins the project handling: a
// kern-managed root gets the exact cleanup command (non-interactive), and
// its own .kern is NOT swept by the user-level data removal.
func TestUninstallManagedProjectPrintsCommand(t *testing.T) {
	uninstallTestHome(t)
	root := t.TempDir()
	if _, _, err := projectstate.EnsureMarker(root); err != nil {
		t.Fatal(err)
	}

	var code int
	out := captureStdout(t, func() { code = runUninstall([]string{"--yes", "--plain", "--root", root}) })
	if code != 0 {
		t.Fatalf("exit = %d\n%s", code, out)
	}
	want := "kern setup --remove --project --root " + root
	if !strings.Contains(out, want) {
		t.Errorf("a managed project must be given the exact cleanup command %q:\n%s", want, out)
	}
	if _, err := os.Stat(filepath.Join(root, ".kern", "config.json")); err != nil {
		t.Errorf("the project's own .kern must survive the user-level uninstall (err=%v)", err)
	}
}

// TestUninstallNeverTouchesRepoFiles pins the product rule: an uninstaller
// removes the tool, never project content. A kern-managed root with wiring
// present (marker, AGENTS.md, skills) goes through the FULL uninstall
// with --yes and every repo file must survive byte-identical — the managed
// root earns a printed cleanup command, nothing more. (Replaces the old
// consented-offer design, which stripped the repo an uninstall happened
// to run from.)
func TestUninstallNeverTouchesRepoFiles(t *testing.T) {
	uninstallTestHome(t)
	prefix := t.TempDir()
	t.Setenv("KERN_INSTALL_DIR", prefix)
	for _, b := range []string{"kern", "kern-mcp"} {
		if err := os.WriteFile(filepath.Join(prefix, b), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	root := t.TempDir()
	if _, _, err := projectstate.EnsureMarker(root); err != nil {
		t.Fatal(err)
	}
	agentsMD := []byte("# kern usage rules (test fixture)\nkeep me\n")
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), agentsMD, 0o644); err != nil {
		t.Fatal(err)
	}
	skillDir := filepath.Join(root, ".agents", "skills", "kern-investigate")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	skill := []byte("# kern-investigate (test fixture)\n")
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), skill, 0o644); err != nil {
		t.Fatal(err)
	}

	var code int
	out := captureStdout(t, func() { code = runUninstall([]string{"--yes", "--plain", "--root", root}) })
	if code != 0 {
		t.Fatalf("exit = %d\n%s", code, out)
	}
	if got, err := os.ReadFile(filepath.Join(root, "AGENTS.md")); err != nil || string(got) != string(agentsMD) {
		t.Errorf("repo AGENTS.md must survive uninstall byte-identical (err=%v)", err)
	}
	if got, err := os.ReadFile(filepath.Join(skillDir, "SKILL.md")); err != nil || string(got) != string(skill) {
		t.Errorf("repo skill files must survive uninstall (err=%v)", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".kern", "config.json")); err != nil {
		t.Errorf("the project's .kern marker must survive uninstall (err=%v)", err)
	}
	want := "kern setup --remove --project --root " + root
	if !strings.Contains(out, want) {
		t.Errorf("the managed root must still get its printed cleanup command:\n%s", out)
	}
	if strings.Contains(out, "Also remove this repository") {
		t.Errorf("the interactive offer must be gone:\n%s", out)
	}
}

// TestUninstallModeFlagConflicts pins the usage guard for contradictory modes.
func TestUninstallModeFlagConflicts(t *testing.T) {
	uninstallTestHome(t)
	for _, args := range [][]string{
		{"--prefix-only", "--project-only", "--yes"},
		{"--prefix-only", "--machine", "--yes"},
	} {
		errOut := captureStderr(t, func() {
			assertExitCode(t, 2, func() {
				_ = runUninstall(args)
			})
		})
		if !strings.Contains(errOut, "cannot be combined") && !strings.Contains(errOut, "pick one") {
			t.Errorf("RunUninstall(%v) must reject the combination:\n%s", args, errOut)
		}
	}
}
