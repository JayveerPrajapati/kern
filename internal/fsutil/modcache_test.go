package fsutil

import (
	"path/filepath"
	"testing"
)

func TestModuleCacheDirPrefersEnv(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GOMODCACHE", dir)
	if got := ModuleCacheDir(); got != dir {
		t.Fatalf("ModuleCacheDir = %q, want GOMODCACHE %q", got, dir)
	}

	t.Setenv("GOMODCACHE", "")
	gopath := t.TempDir()
	t.Setenv("GOPATH", gopath)
	if got, want := ModuleCacheDir(), filepath.Join(gopath, "pkg", "mod"); got != want {
		t.Fatalf("ModuleCacheDir = %q, want %q", got, want)
	}
}
