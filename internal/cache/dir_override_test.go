package cache

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCacheDirOverrideAndReadOnly(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("KERN_CACHE_DIR", dir)
	t.Setenv("KERN_CACHE_READONLY", "")
	if got := Dir(); got != dir {
		t.Fatalf("Dir() = %q, want KERN_CACHE_DIR %q", got, dir)
	}
	if !Writable() {
		t.Fatal("Writable() = false for a writable KERN_CACHE_DIR")
	}

	t.Setenv("KERN_CACHE_READONLY", "1")
	if !ReadOnly() {
		t.Fatal("ReadOnly() = false with KERN_CACHE_READONLY=1")
	}
	if Writable() {
		t.Fatal("Writable() = true in read-only mode")
	}
	if _, err := os.Stat(filepath.Join(dir, ".write-probe")); err == nil {
		t.Fatal("read-only mode left a probe file")
	}
}

func TestCacheDirFallsBackToXDG(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("KERN_CACHE_DIR", "")
	t.Setenv("XDG_CACHE_HOME", xdg)
	if got := Dir(); got != filepath.Join(xdg, "kern") {
		t.Fatalf("Dir() = %q, want %q", got, filepath.Join(xdg, "kern"))
	}
}
