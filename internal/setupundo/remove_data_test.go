package setupundo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/cache"
)

// remove_data_test.go — pins the T-08 uninstall data-removal contract: exact
// resolved paths only, symlink refusal, home containment for ~/.kern, honest
// dry-run, and the --data flag orchestration in RunRemove.

func dataDirs(t *testing.T) (home string, dirs []string) {
	t.Helper()
	home = isolatedHome(t)
	dirs = []string{filepath.Join(home, ".kern")}
	// Derive the secrets dir from the same resolver the helper uses:
	// os.UserConfigDir ignores XDG_CONFIG_HOME on macOS (Library/Application
	// Support), so hardcoding .config would test the wrong path.
	if cfg, err := os.UserConfigDir(); err == nil {
		dirs = append(dirs, filepath.Join(cfg, "kern"))
	}
	// The cache must be seeded where the RUNTIME writes it — internal/
	// cache.Dir (KERN_CACHE_DIR, then XDG_CACHE_HOME, then ~/.cache/kern).
	// Seeding os.UserCacheDir()/kern instead tested the wrong path on macOS
	// (~/Library/Caches vs the runtime's ~/.cache) and hid F-A8: the
	// uninstaller removed the directory the test created, not the one kern
	// actually uses.
	dirs = append(dirs, cache.Dir())
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(d, "nested"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "nested", "index.json"), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return home, dirs
}

func TestRemoveMachineDataRemovesEveryTarget(t *testing.T) {
	_, dirs := dataDirs(t)
	statuses := RemoveMachineData(false)
	if len(statuses) != len(dirs) {
		t.Fatalf("expected %d statuses, got %d: %+v", len(dirs), len(statuses), statuses)
	}
	for _, s := range statuses {
		if !s.Installed || s.Skipped {
			t.Errorf("target %s was not removed: %+v", s.Path, s)
		}
	}
	for _, d := range dirs {
		if _, err := os.Stat(d); !os.IsNotExist(err) {
			t.Errorf("%s must be gone (stat err=%v)", d, err)
		}
	}
}

func TestRemoveMachineDataSkipsMissingTargets(t *testing.T) {
	isolatedHome(t)
	if got := RemoveMachineData(false); len(got) != 0 {
		t.Fatalf("a machine with no kern data must report nothing, got %+v", got)
	}
}

func TestRemoveMachineDataDryRunWritesNothing(t *testing.T) {
	_, dirs := dataDirs(t)
	statuses := RemoveMachineData(true)
	if len(statuses) != len(dirs) {
		t.Fatalf("expected %d dry-run statuses, got %d", len(dirs), len(statuses))
	}
	for _, s := range statuses {
		if !strings.Contains(s.Note, "[dry-run]") {
			t.Errorf("dry-run status must be marked: %+v", s)
		}
	}
	for _, d := range dirs {
		if _, err := os.Stat(filepath.Join(d, "nested", "index.json")); err != nil {
			t.Fatalf("%s must survive a dry-run (err=%v)", d, err)
		}
	}
}

func TestRemoveMachineDataRefusesSymlinkTarget(t *testing.T) {
	home := isolatedHome(t)
	real := t.TempDir()
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(home, ".kern")); err != nil {
		t.Skip("platform without symlink support")
	}
	statuses := RemoveMachineData(false)
	refused := false
	for _, s := range statuses {
		if filepath.Base(s.Path) == ".kern" && !s.Skipped {
			t.Errorf("symlinked ~/.kern must be refused, got %+v", s)
		}
		if filepath.Base(s.Path) == ".kern" && s.Skipped {
			refused = true
		}
	}
	if !refused {
		t.Fatalf("expected a refusal status for ~/.kern, got %+v", statuses)
	}
	if _, err := os.Stat(real); err != nil {
		t.Fatalf("the symlink destination must be untouched (err=%v)", err)
	}
	if _, err := os.Lstat(filepath.Join(home, ".kern")); err != nil {
		t.Fatalf("the symlink itself must be left in place (err=%v)", err)
	}
}

func TestResolvesUnderHome(t *testing.T) {
	for _, tc := range []struct {
		name, home, path string
		want             bool
	}{
		{"direct child", "/a/home", "/a/home/.kern", true},
		{"the home itself", "/a/home", "/a/home", true},
		{"outside", "/a/home", "/elsewhere/.kern", false},
		{"prefix trick", "/a/home", "/a/homebrew/.kern", false},
	} {
		if got := resolvesUnderHome(tc.home, tc.path); got != tc.want {
			t.Errorf("%s: resolvesUnderHome(%q, %q) = %v, want %v", tc.name, tc.home, tc.path, got, tc.want)
		}
	}
	// A symlinked home must not produce a false refusal when the target
	// lives under the resolved home (the macOS /var -> /private/tmp case).
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "home-link")
	if err := os.Symlink(real, link); err != nil {
		t.Skip("platform without symlink support")
	}
	if !resolvesUnderHome(link, filepath.Join(real, ".kern")) {
		t.Errorf("path under the resolved home must pass (home link %s -> %s)", link, real)
	}
	if !resolvesUnderHome(real, filepath.Join(link, ".kern")) {
		t.Errorf("path resolving under home must pass (path link %s -> %s)", link, real)
	}
}

func TestRemoveDataFlagOrchestration(t *testing.T) {
	home, dirs := dataDirs(t)
	if code := RunRemove([]string{"--remove", "--data", "--yes", "--plain"}); code != 0 {
		t.Fatalf("RunRemove --data exited %d", code)
	}
	for _, d := range dirs {
		if _, err := os.Stat(d); !os.IsNotExist(err) {
			t.Errorf("%s must be removed by --remove --data (stat err=%v)", d, err)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".kern")); !os.IsNotExist(err) {
		t.Fatalf("~/.kern must be gone")
	}
}

func TestRemoveDataFlagRejectsNarrowScopes(t *testing.T) {
	for _, args := range [][]string{
		{"--remove", "--data", "--project"},
		{"--remove", "--data", "--hooks-only"},
	} {
		if code := RunRemove(args); code != 2 {
			t.Errorf("RunRemove(%v) must exit 2 (usage rejection), got %d", args, code)
		}
	}
}

func TestMachineDataTargetsAreExactHomePaths(t *testing.T) {
	home := isolatedHome(t)
	targets := MachineDataTargets()
	if len(targets) != 3 {
		t.Fatalf("expected 3 targets (state, secrets, cache), got %+v", targets)
	}
	seenState := false
	for _, target := range targets {
		if filepath.Base(target.Path) != "kern" && filepath.Base(target.Path) != ".kern" {
			t.Errorf("unexpected target %q", target.Path)
		}
		if target.Path == filepath.Join(home, ".kern") {
			seenState = true
		}
	}
	if !seenState {
		t.Errorf("targets must include ~/.kern, got %+v", targets)
	}
	// F-A8: the cache target must be the RUNTIME cache root (the resolver
	// internal/cache writes through), not os.UserCacheDir() — on macOS those
	// two disagree (~/Library/Caches vs ~/.cache/kern) and the uninstaller
	// left the real cache behind.
	wantCache := cache.Dir()
	foundCache := false
	for _, target := range targets {
		if target.Path == wantCache {
			foundCache = true
		}
	}
	if !foundCache {
		t.Errorf("targets must include the runtime cache root %s, got %+v", wantCache, targets)
	}
}
