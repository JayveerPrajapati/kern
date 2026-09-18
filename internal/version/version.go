// Package version holds the build-stamped version string shared by all
// kern binaries. Release builds override it via
// -ldflags "-X github.com/JayveerPrajapati/kern/internal/version.Version=..."
// (or the legacy "-X main.version=..." which still works because each
// main package's version var is initialized from this one).
package version

import (
	"fmt"
	"os"
)

// Version is the kern release version. Defaults to "dev" for source
// checkouts; stamped at build time for releases.
var Version = "dev"

// Adopt resolves the effective version a main package should report. Each
// binary keeps a `var version = "dev"` in package main so the legacy
// `-ldflags "-X main.version=..."` (which only rewrites a compile-time
// constant initializer) keeps working; Adopt then fingerprints whether that
// var is still the unwrapped "dev" and, if so, falls back to the shared
// internal/version.Version (either "dev" or the newer
// `-X .../internal/version.Version=...` stamp). This centralizes the
// boilerplate that used to be repeated (verbatim) in every cmd/*/main.go
// init(), while preserving exact behavior for both ldflags forms — DRY.
func Adopt(compiledIn string) string {
	if compiledIn != "dev" {
		return compiledIn
	}
	return Version
}

// BuildID identifies the running build for tool-response cache keying.
// Release builds (Version stamped) return Version unchanged. Dev builds
// ("dev") fold in the executable's size and mtime so that a rebuild after a
// code change mints fresh cache keys instead of serving responses cached by
// the pre-change binary — a fixed handler otherwise appears still broken
// until the 24h TTL lapses or the cache is cleared by hand.
func BuildID() string {
	if Version != "dev" {
		return Version
	}
	self, err := os.Executable()
	if err != nil {
		return Version
	}
	st, err := os.Stat(self)
	if err != nil {
		return Version
	}
	return fmt.Sprintf("dev+%d@%d", st.Size(), st.ModTime().Unix())
}
