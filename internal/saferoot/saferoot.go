// Package saferoot decides whether an automatic first index build may run in a
// directory: with kern wired globally, any agent can start anywhere, and
// indexing $HOME or a filesystem root would walk and persist the whole machine.
// Only AUTOMATIC first builds are gated — `kern index <dir>` is a deliberate
// operator choice.
package saferoot

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// EnvAllowBroadIndex lets an operator deliberately allow auto-indexing a broad
// directory.
const EnvAllowBroadIndex = "KERN_ALLOW_BROAD_INDEX"

// UnsafeRootError is returned when an automatic first build is refused.
type UnsafeRootError struct {
	Root   string
	Reason string
}

func (e *UnsafeRootError) Error() string {
	return fmt.Sprintf("kern: refusing to auto-index %s: %s. Open a project directory instead, or index it deliberately with `kern index %s` (override: %s=1)",
		e.Root, e.Reason, e.Root, EnvAllowBroadIndex)
}

// projectMarkers identify a directory the user treats as a project.
var projectMarkers = []string{
	".git", ".kern", "go.mod", "package.json", "pyproject.toml", "setup.py",
	"Cargo.toml", "pom.xml", "build.gradle", "build.gradle.kts", "Gemfile",
	"composer.json", "mix.exs", "CMakeLists.txt", "Makefile",
}

// homeNonProjectDirs are well-known folders directly under $HOME that hold
// documents and media rather than code (lower-case).
var homeNonProjectDirs = map[string]bool{
	"desktop": true, "documents": true, "downloads": true, "library": true,
	"pictures": true, "movies": true, "music": true, "applications": true,
	"public": true, "appdata": true, "onedrive": true, "dropbox": true,
}

// UnsafeAutoIndexRoot returns a non-empty human reason when root must not be
// indexed automatically.
func UnsafeAutoIndexRoot(root string) string {
	home, _ := os.UserHomeDir()
	return unsafeAutoIndexRoot(root, home)
}

func unsafeAutoIndexRoot(root, home string) string {
	if os.Getenv(EnvAllowBroadIndex) == "1" {
		return ""
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return ""
	}
	abs = filepath.Clean(abs)
	if filepath.Dir(abs) == abs {
		return "it is a filesystem root"
	}
	if home != "" {
		home = filepath.Clean(home)
		if abs == home {
			return "it is your home directory"
		}
		if strings.HasPrefix(home, abs+string(filepath.Separator)) {
			return "it contains your home directory"
		}
	}
	for _, m := range projectMarkers {
		if _, err := os.Stat(filepath.Join(abs, m)); err == nil {
			return ""
		}
	}
	if home != "" && filepath.Dir(abs) == home && homeNonProjectDirs[strings.ToLower(filepath.Base(abs))] {
		return "it looks like a documents/media folder, not a project (no .git, go.mod, package.json, ... found)"
	}
	return ""
}
