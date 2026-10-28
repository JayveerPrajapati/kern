package index

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/saferoot"
)

// An automatic first build in $HOME must be refused instead of walking the
// user's whole machine, and must leave no index behind.
func TestLoadOrBuildRefusesUnsafeRoot(t *testing.T) {
	t.Setenv(saferoot.EnvAllowBroadIndex, "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if err := os.WriteFile(filepath.Join(home, "notes.go"), []byte("package n\nfunc A() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadOrBuild(home)
	var ue *UnsafeRootError
	if !errors.As(err, &ue) {
		t.Fatalf("expected UnsafeRootError for $HOME, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(home, ".kern")); statErr == nil {
		t.Error("a refused build must not create .kern")
	}
}
