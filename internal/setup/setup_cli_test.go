package setup

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setup_cli_test.go — the RunSetup CLI-shape tests that lived alongside the
// (now extracted) --remove flow in setup_remove_test.go. The removal family
// itself lives in internal/setupundo; these pin setup's own behavior.

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

// isolatedHome points every home-relative resolver at a temp dir so a test
// can never touch the real machine. Formerly in remove_test.go; the removal
// family moved to internal/setupundo but other setup tests build on it.
func isolatedHome(t *testing.T) string {
	return removeTestHome(t)
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
