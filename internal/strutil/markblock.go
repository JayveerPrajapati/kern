package strutil

import "strings"

// RemoveMarkedBlock strips the substring between openMarker and closeMarker
// (inclusive of both markers) from s. If either marker is absent the input is
// returned unchanged. A single line terminator following the close marker
// (LF or CRLF) is dropped as well, so repeatedly removing and re-inserting a
// managed block does not accumulate blank lines.
//
// Callers: kern-managed blocks in .gitignore / AGENTS.md style files
// (setup's kern-generated block, check's blueprint runtime block).
func RemoveMarkedBlock(s, openMarker, closeMarker string) string {
	start := strings.Index(s, openMarker)
	if start < 0 {
		return s
	}
	end := strings.Index(s[start:], closeMarker)
	if end < 0 {
		return s
	}
	end += start + len(closeMarker)
	if strings.HasPrefix(s[end:], "\r\n") {
		end += 2
	} else if end < len(s) && s[end] == '\n' {
		end++
	}
	return s[:start] + s[end:]
}
