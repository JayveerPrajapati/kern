package fsutil

import (
	"os"
	"path/filepath"
	"strings"
)

// ModuleCacheDir returns the local Go module cache root without spawning
// `go env`: GOMODCACHE when set, else GOPATH/pkg/mod, else ~/go/pkg/mod.
func ModuleCacheDir() string {
	if dir := strings.TrimSpace(os.Getenv("GOMODCACHE")); dir != "" {
		return dir
	}
	if gopath := strings.TrimSpace(os.Getenv("GOPATH")); gopath != "" {
		if list := filepath.SplitList(gopath); len(list) > 0 && list[0] != "" {
			return filepath.Join(list[0], "pkg", "mod")
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "go", "pkg", "mod")
}
