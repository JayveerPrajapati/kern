// This file holds the release-channel resolver for `kern update`: given the
// available release tags, pick the target a channel names. It is a pure
// function over the tags — no network, no state — so the Go side, install.sh
// and the dry-run preview all share the same closed semantics. Comparison
// always goes through Compare (never golang.org/x/semver, which rejects this
// project's 4-component tags).
package version

import (
	"fmt"
	"regexp"
)

// ereUnsupportedRe flags RE2-only regex constructs that the installer's
// POSIX-ERE grep (install.sh, grep -E) resolves differently — \d, \w, \s,
// \b and every (?...) form. A channel shared between `kern update` (Go RE2)
// and install.sh must stay in the common POSIX-ERE subset, or the two
// resolvers silently pick different releases (finding 9).
var ereUnsupportedRe = regexp.MustCompile(`\\([dDwWsSbB])|\(\?`)

// ResolveChannel resolves the target release for a channel from the
// available release tags, returning the highest matching tag's raw string.
// Fail-closed, same posture as Parse:
//
//   - "" or "latest": the highest overall tag — 4-component hotfixes like
//     v0.9.9.1 included. This is the pre-channel behavior of install.sh's
//     /releases/latest lookup.
//   - "stable": the highest 3-component tag — 4-component hotfixes are
//     excluded, so a stable channel never auto-picks a hotfix release.
//     Prerelease tags are not filtered out explicitly: Compare ranks a
//     release above its own prerelease, so the maximum naturally prefers
//     the bare release when both exist.
//   - any other non-empty value: a regular expression matched against every
//     tag; the highest match wins. An invalid regex or a regex with no
//     matching tag is an error — never a silent fallback to latest.
//
// Tags that do not Parse as release versions are skipped (they are not
// release tags); if nothing remains after filtering, ResolveChannel errors
// rather than guessing.
func ResolveChannel(tags []string, channel string) (string, error) {
	if len(tags) == 0 {
		return "", fmt.Errorf("no release tags available to resolve channel %q", channel)
	}
	parsed := make([]SemVer, 0, len(tags))
	for _, t := range tags {
		if v, err := Parse(t); err == nil {
			parsed = append(parsed, v)
		}
	}
	if len(parsed) == 0 {
		return "", fmt.Errorf("no tags parse as release versions")
	}
	switch channel {
	case "", "latest":
		// highest overall, hotfixes included
	case "stable":
		parsed = filterChannel(parsed, func(v SemVer) bool { return v.Fourth == 0 })
		if len(parsed) == 0 {
			return "", fmt.Errorf("channel %q: no 3-component release tags (4-component hotfixes like v0.9.9.1 are excluded from stable)", channel)
		}
	default:
		// A channel regex must stay in the POSIX-ERE subset the installer
		// supports; RE2-only constructs (\d, \w, (?i), ...) would resolve
		// differently under install.sh's grep -E, so they are rejected
		// EARLY here with the ERE spelling recommended (finding 9).
		if ereUnsupportedRe.MatchString(channel) {
			return "", fmt.Errorf("channel %q uses RE2-only regex syntax (\\d, \\w, \\s, \\b, (?i), ...) that the installer's POSIX-ERE grep does not support; use the ERE spelling instead — [0-9] for \\d, [A-Za-z0-9_] for \\w, case-insensitive classes like [A-Za-z]", channel)
		}
		re, err := regexp.Compile(channel)
		if err != nil {
			return "", fmt.Errorf("channel %q: invalid channel regex: %v", channel, err)
		}
		parsed = filterChannel(parsed, func(v SemVer) bool { return re.MatchString(v.Raw) })
		if len(parsed) == 0 {
			return "", fmt.Errorf("channel %q: no release tags match the channel regex", channel)
		}
	}
	best := parsed[0]
	for _, v := range parsed[1:] {
		if Compare(v, best) > 0 {
			best = v
		}
	}
	return best.Raw, nil
}

// filterChannel returns the parsed versions for which keep reports true,
// reusing the underlying slice (callers never use it afterwards).
func filterChannel(parsed []SemVer, keep func(SemVer) bool) []SemVer {
	out := parsed[:0]
	for _, v := range parsed {
		if keep(v) {
			out = append(out, v)
		}
	}
	return out
}
