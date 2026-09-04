// Package version holds the build-injected version string shared by the
// blueprint CLI and the blueprint-mcp server. It also provides kern
// version parsing and minimum-version checking.
package version

import (
	"fmt"
	"strconv"
	"strings"
)

// Version is injected at build time via -ldflags "-X ...=...".
// Defaults to "dev" for plain `go build` / `go test` runs.
var Version = "dev"

// MinKernVersion is the minimum kern version Blueprint requires. Kern must
// speak contract v2 (schema_version in guard/sec JSON, authz_verdict).
// Blueprint auto-installs kern when it is not found, and rejects versions
// below this threshold with a clear upgrade message.
const MinKernVersion = "v0.9.0"

// KernModulePath is the go install target for the kern binary.
const KernModulePath = "github.com/JayveerPrajapati/kern/cmd/kern@latest"

// ParseVersion parses a version string like "v0.9.0", "0.9.0", or "dev"
// into major, minor, patch integers. Returns an error for unparseable
// versions (e.g. "dev").
func ParseVersion(v string) (major, minor, patch int, err error) {
	v = strings.TrimPrefix(v, "v")
	parts := strings.Split(v, ".")
	if len(parts) < 3 {
		return 0, 0, 0, fmt.Errorf("version %q: expected major.minor.patch", v)
	}
	major, err = strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, 0, fmt.Errorf("version %q: major: %w", v, err)
	}
	minor, err = strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, 0, fmt.Errorf("version %q: minor: %w", v, err)
	}
	patch, err = strconv.Atoi(parts[2])
	if err != nil {
		return 0, 0, 0, fmt.Errorf("version %q: patch: %w", v, err)
	}
	return major, minor, patch, nil
}

// VersionAtLeast reports whether installed is >= required. Both are compared
// as major.minor.patch. The sentinel "dev" (a build without ldflags version
// injection, e.g. from `go install pkg@latest`) is treated as satisfying any
// minimum: it is built from the latest source and is therefore by definition
// at or above the required version. Any other unparseable version is treated
// as 低于 any parsed version (conservative: triggers upgrade).
func VersionAtLeast(installed, required string) bool {
	if installed == "dev" {
		return true
	}
	imajor, iminor, ipatch, iErr := ParseVersion(installed)
	rmajor, rminor, rpatch, rErr := ParseVersion(required)
	if iErr != nil || rErr != nil {
		return false
	}
	if imajor != rmajor {
		return imajor > rmajor
	}
	if iminor != rminor {
		return iminor > rminor
	}
	return ipatch >= rpatch
}
