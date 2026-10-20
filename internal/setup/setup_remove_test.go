package setup

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setup_remove_test.go — the setup family's --remove flow tests, moved from
// cmd/kern (workstream 3.6-A — setup family extraction). They now drive the
// internal RunSetup entry point directly and read its returned exit code
// instead of recovering cmd/kern's exitError panic.

func removeTestHome(t *testing.T) string {
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

func wireForRemoveTest(t *testing.T, root string) {
	t.Helper()
	WireWith(root, nil, false, true, WireOptions{GlobalOnly: true})
	WireGlobalRules()
	WireGlobal(nil)
}

func captureSetupErr(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	fn()
	_ = w.Close()
	os.Stderr = old
	b, _ := io.ReadAll(r)
	return string(b)
}

func TestSetupRemoveRefusesNonInteractiveWithoutYes(t *testing.T) {
	home := removeTestHome(t)
	root := t.TempDir()
	wireForRemoveTest(t, root)
	hooks := filepath.Join(home, ".cursor", "hooks.json")
	if _, err := os.Stat(hooks); err != nil {
		t.Fatalf("sanity: wiring did not create %s: %v", hooks, err)
	}

	out := captureSetupOut(t, func() {
		if code := RunSetup([]string{"--remove", "--plain", "--root", root}); code != 2 {
			t.Fatalf("exit = %d, want 2 (refuse without --yes when stdin is not a terminal)", code)
		}
	})
	_ = out
	if _, err := os.Stat(hooks); err != nil {
		t.Error("a refused --remove must not change anything")
	}
}

func TestSetupRemoveYesRemovesAndReports(t *testing.T) {
	home := removeTestHome(t)
	root := t.TempDir()
	wireForRemoveTest(t, root)

	var code int
	out := captureSetupOut(t, func() { code = RunSetup([]string{"--remove", "--yes", "--plain", "--root", root}) })
	if code != 0 {
		t.Fatalf("exit = %d\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(home, ".cursor", "hooks.json")); err == nil {
		t.Error("cursor hooks should be removed")
	}
	for _, want := range []string{"Removed kern from", "~/.kern data", "kern setup --global-only --global-rules"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	// A second run has nothing to do and still succeeds.
	out = captureSetupOut(t, func() { code = RunSetup([]string{"--remove", "--yes", "--plain", "--root", root}) })
	if code != 0 || !strings.Contains(out, "Nothing to remove") {
		t.Errorf("second run: exit %d, output:\n%s", code, out)
	}
}

func TestSetupRemoveDryRunChangesNothing(t *testing.T) {
	home := removeTestHome(t)
	root := t.TempDir()
	wireForRemoveTest(t, root)
	hooks := filepath.Join(home, ".cursor", "hooks.json")

	var code int
	out := captureSetupOut(t, func() { code = RunSetup([]string{"--remove", "--dry-run", "--plain", "--root", root}) })
	if code != 0 {
		t.Fatalf("exit = %d\n%s", code, out)
	}
	if _, err := os.Stat(hooks); err != nil {
		t.Error("--dry-run must not remove anything")
	}
	if !strings.Contains(out, "Dry-run preview only") || !strings.Contains(out, "[dry-run] would") {
		t.Errorf("dry-run output should preview the removal:\n%s", out)
	}
}

func TestSetupRemoveRejectsWiringFlags(t *testing.T) {
	removeTestHome(t)
	for _, bad := range []string{"--global", "--global-only", "--global-rules", "--all-agents", "--detect"} {
		var code int
		captureSetupOut(t, func() { code = RunSetup([]string{"--remove", bad, "--yes"}) })
		if code != 2 {
			t.Errorf("--remove %s: exit = %d, want 2", bad, code)
		}
	}
}

// TestSetupRejectsPositionals pins the query-shaped-intent guard: a
// positional such as "status" reads like a read-only subcommand but setup
// has none — it must exit 2 pointing at the read-only surface instead of
// silently running the real wiring (the old behavior mutated hooks,
// plugin copies and managed files under a query intent).
func TestSetupRejectsPositionals(t *testing.T) {
	removeTestHome(t)
	root := t.TempDir()
	for _, pos := range []string{"status", "check", "init"} {
		var code int
		err := captureSetupErr(t, func() { code = RunSetup([]string{pos, "--plain", "--root", root}) })
		if code != 2 {
			t.Errorf("setup %q: exit = %d, want 2\n%s", pos, code, err)
		}
		if !strings.Contains(err, "kern setup --check") {
			t.Errorf("setup %q: usage message should name the read-only surface:\n%s", pos, err)
		}
	}
	if entries, _ := os.ReadDir(root); len(entries) > 0 {
		t.Errorf("a rejected positional must not run any wiring; root now has %d entries", len(entries))
	}
}

func TestSetupRemoveNothingToRemove(t *testing.T) {
	removeTestHome(t)
	var code int
	out := captureSetupOut(t, func() { code = RunSetup([]string{"--remove", "--plain", "--root", t.TempDir()}) })
	if code != 0 || !strings.Contains(out, "Nothing to remove") {
		t.Errorf("exit %d, output:\n%s", code, out)
	}
}

// TestSetupDryRunPrintsPreviewDespiteQuietDefault pins the quiet-default
// interaction: a non-interactive "kern setup --dry-run" on an already-wired
// repo must still print its would-lines — every dry-run note ends with
// "(already present — would refresh if stale)", which the quiet filter
// (statusQuietHidden) would otherwise swallow whole, leaving the preview
// completely empty (the 4dcc3da quiet-by-default regression).
func TestSetupDryRunPrintsPreviewDespiteQuietDefault(t *testing.T) {
	removeTestHome(t)
	root := t.TempDir()
	WireWith(root, nil, false, false, WireOptions{})

	var code int
	out := captureSetupOut(t, func() { code = RunSetup([]string{"--dry-run", "--plain", "--root", root}) })
	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s", code, out)
	}
	if !strings.Contains(out, "[dry-run] would") {
		t.Fatalf("dry-run preview swallowed by the quiet default:\n%s", out)
	}
}
