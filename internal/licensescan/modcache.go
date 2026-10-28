package licensescan

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/fsutil"
)

// EscapeModulePath applies the Go module cache's case encoding: uppercase
// letters become "!" + lowercase (github.com/KyleBanks/depth →
// github.com/!kyle!banks/depth).
func EscapeModulePath(path string) string {
	var b strings.Builder
	for _, r := range path {
		if r >= 'A' && r <= 'Z' {
			b.WriteByte('!')
			b.WriteRune(r + ('a' - 'A'))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// ReadModuleLicense returns the LICENSE/COPYING text of the newest cached
// version of modulePath, if the module cache has one. Local and offline; it
// never runs the go tool.
func ReadModuleLicense(modulePath string) ([]byte, bool) {
	cache := fsutil.ModuleCacheDir()
	if cache == "" || modulePath == "" {
		return nil, false
	}
	dirs, err := filepath.Glob(filepath.Join(cache, EscapeModulePath(modulePath)+"@*"))
	if err != nil || len(dirs) == 0 {
		return nil, false
	}
	sort.Sort(sort.Reverse(sort.StringSlice(dirs)))
	for _, dir := range dirs {
		for _, name := range []string{"LICENSE", "LICENSE.txt", "LICENSE.md", "COPYING", "COPYING.txt", "LICENCE"} {
			data, err := os.ReadFile(filepath.Join(dir, name))
			if err == nil && len(data) > 0 {
				return data, true
			}
		}
	}
	return nil, false
}
