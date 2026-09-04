// Package strutil provides small text/number helpers shared by the CLI and MCP
// layers so identical implementations are not duplicated across packages.
package strutil

import (
	"net/url"
	"strings"
)

// Pct returns the percentage improvement from before to after. It reports 0
// when before is 0 or negative to avoid a nonsensical/infinite value.
func Pct(before, after int) float64 {
	if before <= 0 {
		return 0
	}
	return float64(before-after) / float64(before) * 100
}

// Lines splits s into lines, normalizing Windows CRLF to LF first so every
// line is stripped of its trailing \r.
func Lines(s string) []string {
	return strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
}

// Slug converts a name into a filesystem-safe lower-case slug: only letters,
// digits and single dashes survive; the result is trimmed of dashes. A name
// with no usable characters yields "doc".
func Slug(s string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.TrimSpace(s) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			lastDash = false
		} else if r >= 'A' && r <= 'Z' {
			b.WriteRune(r + ('a' - 'A'))
			lastDash = false
		} else if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "doc"
	}
	return out
}

// DocSlug derives a filesystem-safe doc name from a URL, e.g.
// https://react.dev/reference/usestate -> react-dev-reference-usestate.
// Invalid URLs yield "doc".
func DocSlug(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "doc"
	}
	return Slug(strings.TrimSuffix(u.Hostname()+u.Path, "/"))
}
