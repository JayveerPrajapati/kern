package setupundo

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/setup"
)

// ui_test.go — the --remove flow tests, moved from internal/setup with the
// removal family. They drive the package's RunRemove entry point directly
// and read its returned exit code.

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
	setup.WireWith(root, nil, false, true, setup.WireOptions{GlobalOnly: true})
	setup.WireGlobalRules()
	setup.WireGlobal(nil)
}

func captureOut(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	done := make(chan string)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	fn()
	_ = w.Close()
	os.Stdout = old
	return <-done
}

func captureErr(t *testing.T, fn func()) string {
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

	var code int
	out := captureOut(t, func() { code = RunRemove([]string{"--remove", "--plain", "--root", root}) })
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (refuse without --yes when stdin is not a terminal)\n%s", code, out)
	}
	if _, err := os.Stat(hooks); err != nil {
		t.Error("a refused --remove must not change anything")
	}
}

func TestSetupRemoveYesRemovesAndReports(t *testing.T) {
	home := removeTestHome(t)
	root := t.TempDir()
	wireForRemoveTest(t, root)

	var code int
	out := captureOut(t, func() { code = RunRemove([]string{"--remove", "--yes", "--plain", "--root", root}) })
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
	out = captureOut(t, func() { code = RunRemove([]string{"--remove", "--yes", "--plain", "--root", root}) })
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
	out := captureOut(t, func() { code = RunRemove([]string{"--remove", "--dry-run", "--plain", "--root", root}) })
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

func TestSetupRemoveHooksOnlyPreservesOtherWiring(t *testing.T) {
	home := removeTestHome(t)
	root := t.TempDir()
	wireForRemoveTest(t, root)
	mcp := filepath.Join(home, ".cursor", "mcp.json")
	mcpBefore, err := os.ReadFile(mcp)
	if err != nil {
		t.Fatalf("sanity: wiring did not create %s: %v", mcp, err)
	}

	var code int
	out := captureOut(t, func() {
		code = RunRemove([]string{"--remove", "--hooks-only", "--yes", "--plain", "--root", root})
	})
	if code != 0 {
		t.Fatalf("exit = %d\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(home, ".cursor", "hooks.json")); err == nil {
		t.Error("hooks should be removed")
	}
	if got, err := os.ReadFile(mcp); err != nil || string(got) != string(mcpBefore) {
		t.Errorf("MCP wiring must remain byte-for-byte (read err: %v)", err)
	}
	for _, want := range []string{"Removed kern hooks", "MCP wiring, rules, skills, editor config"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestSetupRemoveRejectsWiringFlags(t *testing.T) {
	removeTestHome(t)
	for _, bad := range []string{"--global", "--global-only", "--global-rules", "--all-agents", "--detect"} {
		var code int
		captureOut(t, func() { code = RunRemove([]string{"--remove", bad, "--yes"}) })
		if code != 2 {
			t.Errorf("--remove %s: exit = %d, want 2", bad, code)
		}
	}
}

func TestSetupRemoveNothingToRemove(t *testing.T) {
	removeTestHome(t)
	var code int
	out := captureOut(t, func() { code = RunRemove([]string{"--remove", "--plain", "--root", t.TempDir()}) })
	if code != 0 || !strings.Contains(out, "Nothing to remove") {
		t.Errorf("exit %d, output:\n%s", code, out)
	}
}

// TestTryRemoveDispatch pins the cmd/kern dispatch contract: TryRemove
// claims only --remove invocations (deferring when a read-only mode takes
// precedence) and never claims a plain wiring run.
func TestTryRemoveDispatch(t *testing.T) {
	removeTestHome(t)
	if _, ok := TryRemove([]string{"--plain", "--root", t.TempDir()}); ok {
		t.Error("a wiring invocation must not be claimed")
	}
	if _, ok := TryRemove([]string{"--remove=false"}); ok {
		t.Error("--remove=false must not be claimed")
	}
	if _, ok := TryRemove([]string{"--check"}); ok {
		t.Error("--check must not be claimed")
	}
	if _, ok := TryRemove([]string{"--verify"}); ok {
		t.Error("--verify must not be claimed")
	}
	// Claimed, but nothing to remove: exit 0.
	if code, ok := TryRemove([]string{"--remove", "--plain", "--yes", "--root", t.TempDir()}); !ok || code != 0 {
		t.Errorf("a removal invocation must be claimed and succeed, got ok=%v code=%d", ok, code)
	}
}
