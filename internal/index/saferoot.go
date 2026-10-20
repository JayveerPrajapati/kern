package index

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// With kern wired globally, any agent can start in any directory and the
// first tool call builds an index there. That is exactly what we want inside a
// project and exactly what we must not do in $HOME, a filesystem root, or a
// documents folder: it would walk and persist an index of the user's whole
// machine. UnsafeAutoIndexRoot decides, and only for AUTOMATIC first builds —
// the explicit `kern index <dir>` command is a deliberate choice and is never
// blocked here.

// envAllowBroadIndex lets an operator deliberately allow auto-indexing a broad
// directory.
const envAllowBroadIndex = "KERN_ALLOW_BROAD_INDEX"

// UnsafeRootError is returned when an automatic first build is refused.
type UnsafeRootError struct {
	Root   string
	Reason string
}

func (e *UnsafeRootError) Error() string {
	return fmt.Sprintf("kern: refusing to auto-index %s: %s. Open a project directory instead, or index it deliberately with `kern index %s` (override: %s=1)",
		e.Root, e.Reason, e.Root, envAllowBroadIndex)
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

// HasPersistedIndex reports whether an index store already exists for root,
// i.e. someone deliberately indexed it before. It only stats files: opening
// the SQLite store would itself create <root>/.kern.
func HasPersistedIndex(root string) bool {
	for _, p := range []string{StorePath(root), SQLitePath(root)} {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}

// UnsafeAutoIndexRoot returns a non-empty human reason when root must not be
// indexed automatically.
func UnsafeAutoIndexRoot(root string) string {
	home, _ := os.UserHomeDir()
	return unsafeAutoIndexRoot(root, home)
}

func unsafeAutoIndexRoot(root, home string) string {
	if os.Getenv(envAllowBroadIndex) == "1" {
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
