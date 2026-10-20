package setup

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func treeOf(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	_ = filepath.Walk(dir, func(p string, _ os.FileInfo, err error) error {
		if err == nil {
			rel, _ := filepath.Rel(dir, p)
			out = append(out, rel)
		}
		return nil
	})
	sort.Strings(out)
	return out
}

// GlobalOnly is the "install once, every repo initializes itself" mode: it
// must register kern at user level and leave the repo it was run from
// completely untouched.
func TestWireGlobalOnlyTouchesNoRepoFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := treeOf(t, root)

	sts := WireWith(root, nil, false, true, WireOptions{GlobalOnly: true})
	if len(sts) == 0 {
		t.Fatal("global-only wiring returned no statuses")
	}

	if after := treeOf(t, root); strings.Join(before, "\n") != strings.Join(after, "\n") {
		t.Fatalf("GlobalOnly wrote into the repo.\nbefore: %v\nafter:  %v", before, after)
	}
	for _, s := range sts {
		if s.Path != "" && strings.HasPrefix(s.Path, root) {
			t.Errorf("status %q reports a repo path: %s", s.Agent, s.Path)
		}
	}
	for _, rel := range []string{".claude/settings.json", ".cursor/hooks.json"} {
		if _, err := os.Stat(filepath.Join(home, rel)); err != nil {
			t.Errorf("GlobalOnly must still create ~/%s: %v", rel, err)
		}
	}
}

func TestWireGlobalOnlyDryRunWritesNothingAnywhere(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	root := t.TempDir()
	homeBefore, rootBefore := treeOf(t, home), treeOf(t, root)

	sts := WireWith(root, nil, false, true, WireOptions{GlobalOnly: true, DryRun: true})
	if len(sts) == 0 || !strings.Contains(sts[0].Note, "dry-run") || !strings.Contains(sts[0].Note, "no repo files") {
		t.Fatalf("dry-run must describe a repo-free plan, got %+v", sts)
	}
	if strings.Join(homeBefore, "\n") != strings.Join(treeOf(t, home), "\n") ||
		strings.Join(rootBefore, "\n") != strings.Join(treeOf(t, root), "\n") {
		t.Error("dry-run changed the filesystem")
	}
}

// Without GlobalOnly the legacy --global behaviour (project files too) is kept.
func TestWireGlobalWithoutGlobalOnlyStillWritesProjectFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	root := t.TempDir()
	WireWith(root, nil, false, true, WireOptions{})
	if _, err := os.Stat(filepath.Join(root, ".mcp.json")); err != nil {
		t.Errorf("legacy --global must still write the project .mcp.json: %v", err)
	}
}
