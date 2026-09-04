// Package version holds the build-stamped version string shared by all
// kern binaries. Release builds override it via
// -ldflags "-X github.com/JayveerPrajapati/kern/internal/version.Version=..."
// (or the legacy "-X main.version=..." which still works because each
// main package's version var is initialized from this one).
package version

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
