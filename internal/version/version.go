// Package version holds the build-stamped version string shared by all
// kern binaries. Release builds override it via
// -ldflags "-X github.com/JayveerPrajapati/kern/internal/version.Version=..."
// (or the legacy "-X main.version=..." which still works because each
// main package's version var is initialized from this one).
package version

// Version is the kern release version. Defaults to "dev" for source
// checkouts; stamped at build time for releases.
var Version = "dev"
