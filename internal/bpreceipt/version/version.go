// Package version holds the build-injected version string shared by the
// blueprint CLI and the blueprint-mcp server. It also provides kern
// version parsing and minimum-version checking.
package version

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	kversion "github.com/JayveerPrajapati/kern/internal/version"
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
// into major, minor, patch integers. Pre-release / build suffixes are
// stripped before parsing ("v0.9.7-local" -> 0.9.7, "1.0.0-rc1+meta" ->
// 1.0.0) because a suffixed build of X.Y.Z still satisfies a >= X.Y.Z
// minimum-version gate. Returns an error for unparseable versions
// (e.g. "dev").
func ParseVersion(v string) (major, minor, patch int, err error) {
	v = strings.TrimPrefix(v, "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
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

// channelSuffixRe matches the trailing parenthesized build-channel suffix
// that `kern version` appends for unstamped source builds ("e2f1762 (dev)"
// in cmd/kern/cmd_meta.go).
var channelSuffixRe = regexp.MustCompile(`\s+\([^)]*\)$`)

// VersionAtLeast reports whether installed is >= required. Both are compared
// as major.minor.patch. The sentinel "dev" and commit-hash stamps (checkout
// builds without a release tag) are treated as satisfying any minimum: they
// are built from current source and are therefore by definition at or above
// the required version. That local-build classification is the canonical
// kversion.Provenance == Local (the same classifier `kern update` uses);
// any other unparseable version is treated as below any parsed version
// (conservative: triggers upgrade).
func VersionAtLeast(installed, required string) bool {
	// Strip the `kern version` channel suffix first: an unstamped source
	// build reports "<hash> (dev)", which is a current-source checkout
	// build and must satisfy the minimum like "dev" and bare hashes.
	installed = channelSuffixRe.ReplaceAllString(installed, "")
	if kversion.Provenance(installed) == kversion.ProvenanceLocal {
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
