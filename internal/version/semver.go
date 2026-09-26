// This file holds the release-channel version policy for `kern update`:
// strict parsing, first-class tuple comparison and the allow/deny decision.
// It is deliberately self-contained (no golang.org/x/semver): this project
// ships 4-component tags (v0.9.9.1) which x/semver rejects outright, so the
// comparison is a first-class numeric tuple instead of a vendored parser.
package version

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// maxVersionLen bounds a version string so a hostile (or merely absurd)
// input cannot force unbounded parsing work. 64 characters covers the
// longest plausible 4-component tag with prerelease and build metadata.
const maxVersionLen = 64

// maxNumericField is the exclusive upper bound for every numeric component:
// 1,000,000. Real releases use tiny numbers (v0.9.9.1); the bound exists so
// overflow-class inputs ("999999999999") fail Parse instead of silently
// comparing as garbage.
const maxNumericField = 1_000_000

// versionRe is the anchored shape of a release version: optional v prefix,
// three required numeric components, an optional 4th, and optional
// -prerelease / +build suffixes. The regex alone admits inputs the numeric
// validation must still reject (leading zeros like "007", or a sign inside a
// field would fail the digit charset) — Parse re-validates every field.
var versionRe = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+(\.[0-9]+)?(-[A-Za-z0-9.-]+)?(\+[A-Za-z0-9.-]+)?$`)

// localHashRe matches git short/full hashes — the stamp the Makefile puts
// into every `make build` (VERSION ?= git rev-parse --short HEAD).
var localHashRe = regexp.MustCompile(`^[0-9a-f]{7,40}$`)

// SemVer is a parsed release version: the 4-component numeric tuple plus
// optional prerelease/build suffixes. Compare orders tuples numerically and
// then applies semver prerelease precedence; build metadata never affects
// ordering. (Named SemVer, not Version — the package-level Version var is
// the build-stamped release string and its semantics are pinned elsewhere.)
type SemVer struct {
	Major, Minor, Patch, Fourth int
	Prerelease                  string // without the leading '-'; "" when absent
	Build                       string // without the leading '+'; "" when absent
	Raw                         string // the exact input that parsed
}

// String returns the exact input the version was parsed from.
func (v SemVer) String() string { return v.Raw }

// Parse parses s as a strict release version. It is fail-closed: any
// deviation from the anchored shape — empty string, overlong input, more
// than 4 components, signs, leading zeros, an out-of-range field, or an
// illegal prerelease/build character — returns an error instead of a
// best-effort version. A missing 4th component parses as 0.
func Parse(s string) (SemVer, error) {
	var v SemVer
	if len(s) == 0 {
		return v, fmt.Errorf("empty version string")
	}
	if len(s) > maxVersionLen {
		return v, fmt.Errorf("version %q exceeds the %d-character limit", s, maxVersionLen)
	}
	if !versionRe.MatchString(s) {
		return v, fmt.Errorf("version %q does not match the release shape", s)
	}
	body := strings.TrimPrefix(s, "v")
	if i := strings.IndexByte(body, '+'); i >= 0 {
		v.Build = body[i+1:]
		body = body[:i]
	}
	if i := strings.IndexByte(body, '-'); i >= 0 {
		v.Prerelease = body[i+1:]
		body = body[:i]
	}
	fields := strings.Split(body, ".")
	nums := []*int{&v.Major, &v.Minor, &v.Patch, &v.Fourth}
	for i, f := range fields {
		if i >= len(nums) {
			return v, fmt.Errorf("version %q has more than 4 numeric components", s)
		}
		n, err := parseField(f)
		if err != nil {
			return v, fmt.Errorf("version %q: component %d: %v", s, i+1, err)
		}
		*nums[i] = n
	}
	v.Raw = s
	return v, nil
}

// parseField parses one numeric component: digits only, no sign, no leading
// zeros, strictly below maxNumericField. strconv.Atoi alone would accept
// "+1" (sign) and "007" (leading zero); both are rejected here.
func parseField(f string) (int, error) {
	if f == "" {
		return 0, fmt.Errorf("empty numeric component")
	}
	if f[0] == '0' && len(f) > 1 {
		return 0, fmt.Errorf("component %q: leading zeros are not allowed", f)
	}
	if f[0] == '+' || f[0] == '-' {
		return 0, fmt.Errorf("component %q: a sign is not allowed", f)
	}
	for i := 0; i < len(f); i++ {
		if f[i] < '0' || f[i] > '9' {
			return 0, fmt.Errorf("component %q: non-digit character", f)
		}
	}
	n, err := strconv.Atoi(f)
	if err != nil {
		return 0, fmt.Errorf("component %q: %v", f, err)
	}
	if n >= maxNumericField {
		return 0, fmt.Errorf("component %q exceeds the %d bound", f, maxNumericField)
	}
	return n, nil
}

// Compare orders two versions: negative if a < b, zero if equal, positive if
// a > b. The 4-component numeric tuple is compared first (a missing 4th
// component is 0), then semver prerelease precedence (1.0.0-rc1 < 1.0.0).
// Build metadata never affects ordering.
func Compare(a, b SemVer) int {
	if c := cmpInt(a.Major, b.Major); c != 0 {
		return c
	}
	if c := cmpInt(a.Minor, b.Minor); c != 0 {
		return c
	}
	if c := cmpInt(a.Patch, b.Patch); c != 0 {
		return c
	}
	if c := cmpInt(a.Fourth, b.Fourth); c != 0 {
		return c
	}
	return comparePrerelease(a.Prerelease, b.Prerelease)
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// comparePrerelease implements semver prerelease precedence: a version with
// a prerelease sorts BEFORE the same version without one; dot-split
// identifiers compare with numeric < alphanumeric; when all shared
// identifiers are equal, the version with more identifiers is greater.
func comparePrerelease(a, b string) int {
	switch {
	case a == "" && b == "":
		return 0
	case a == "":
		return 1 // release > prerelease
	case b == "":
		return -1
	}
	ai, bi := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(ai) && i < len(bi); i++ {
		if c := comparePrereleaseID(ai[i], bi[i]); c != 0 {
			return c
		}
	}
	return cmpInt(len(ai), len(bi))
}

func isNumericID(id string) bool {
	if id == "" {
		return false
	}
	for i := 0; i < len(id); i++ {
		if id[i] < '0' || id[i] > '9' {
			return false
		}
	}
	return true
}

// numericIDValue interprets a numeric prerelease identifier, ignoring
// leading zeros per semver ("01" == "1"). The input is digits-only by
// isNumericID, so Atoi cannot fail.
func numericIDValue(id string) int {
	id = strings.TrimLeft(id, "0")
	if id == "" {
		return 0
	}
	n, _ := strconv.Atoi(id)
	return n
}

func comparePrereleaseID(a, b string) int {
	an, bn := isNumericID(a), isNumericID(b)
	switch {
	case an && bn:
		return cmpInt(numericIDValue(a), numericIDValue(b))
	case an:
		return -1 // numeric identifiers sort before alphanumeric ones
	case bn:
		return 1
	default:
		return strings.Compare(a, b) // ASCII order, per semver
	}
}

// ProvenanceKind classifies what a version string looks like.
type ProvenanceKind string

const (
	// ProvenanceRelease is a release-shaped version (matches the full
	// anchored release regex, v prefix optional).
	ProvenanceRelease ProvenanceKind = "Release"
	// ProvenanceLocal is a git-hash-shaped stamp or the literal "dev" —
	// the two values a `make build` / plain `go build` produce.
	ProvenanceLocal ProvenanceKind = "Local"
	// ProvenanceUnknown is anything else — including BuildID's
	// "dev+<size>@<mtime>" fingerprint and unstamped "hash (dev)" output.
	// Unknown strings must never be parsed as versions.
	ProvenanceUnknown ProvenanceKind = "Unknown"
)

// Provenance classifies a version string without parsing it: Release
// (release-shaped), Local (git hash / "dev" — the local-build stamps), or
// Unknown (everything else). Classification is permissive on purpose — it
// feeds the fail-closed UpdateDecision, which still requires a successful
// Parse before any upgrade is allowed.
func Provenance(s string) ProvenanceKind {
	switch {
	case versionRe.MatchString(s):
		return ProvenanceRelease
	case s == "dev" || localHashRe.MatchString(s):
		return ProvenanceLocal
	default:
		return ProvenanceUnknown
	}
}

// DenyKind distinguishes why UpdateDecision refused, so callers can apply a
// deliberate-override policy (e.g. --pin consenting to the downgrade it
// names) without weakening any other gate.
type DenyKind int

const (
	DenyLocalBuild DenyKind = iota
	DenyUnparseableCurrent
	DenyUnparseableTarget
	DenyDowngrade
)

func (k DenyKind) String() string {
	switch k {
	case DenyLocalBuild:
		return "local-build"
	case DenyUnparseableCurrent:
		return "unparseable-current"
	case DenyUnparseableTarget:
		return "unparseable-target"
	case DenyDowngrade:
		return "downgrade"
	}
	return "unknown"
}

// Decision is the outcome of UpdateDecision. Exactly one of Allow, Noop or
// Deny is set; when Deny, Kind is the machine-readable cause and Reason the
// human-readable guidance.
type Decision struct {
	Allow  bool
	Noop   bool
	Deny   bool
	Kind   DenyKind
	Reason string
}

// UpdateDecision applies the release-channel policy to (cur, tgt) — the
// installed version cur and the candidate release tgt. It is fail-closed:
// any version it cannot positively reason about is refused, and --force is
// the only override. A local-build installed version is refused outright
// (rebuild + --force), an unparseable string on either side is refused, a
// strictly newer target is allowed, an older target is refused as a
// downgrade (--pin <tag> is the deliberate-downgrade path), and equal
// versions are a no-op.
func UpdateDecision(cur, tgt string) Decision {
	if Provenance(cur) == ProvenanceLocal {
		return Decision{Deny: true, Kind: DenyLocalBuild,
			Reason: fmt.Sprintf("installed version %q is a local build — rebuild via 'make build && make install', or re-run with --force to overwrite", cur)}
	}
	cv, err := Parse(cur)
	if err != nil {
		return Decision{Deny: true, Kind: DenyUnparseableCurrent,
			Reason: fmt.Sprintf("cannot verify installed version %q (%v) — rebuild via 'make build && make install', or re-run with --force to overwrite", cur, err)}
	}
	tv, err := Parse(tgt)
	if err != nil {
		return Decision{Deny: true, Kind: DenyUnparseableTarget,
			Reason: fmt.Sprintf("cannot verify target version %q (%v) — refusing to update", tgt, err)}
	}
	switch c := Compare(cv, tv); {
	case c > 0:
		return Decision{Deny: true, Kind: DenyDowngrade,
			Reason: fmt.Sprintf("%s is newer than %s — pass --pin %s to downgrade deliberately", cur, tgt, tgt)}
	case c == 0:
		return Decision{Noop: true}
	default:
		return Decision{Allow: true}
	}
}
