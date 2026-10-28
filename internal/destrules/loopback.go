package destrules

import (
	"net"
	"regexp"
	"strings"
)

// LoopbackOnlySegment reports whether EVERY http(s) URL in the command targets
// a loopback host. A command with no URL never qualifies (the fetch target
// cannot be proven from the text alone). Shared by the exec classifier's
// KERN_ALLOW_LOOPBACK opt-out; it lives here so execclassify stays under its
// ARCHITECTURE.md LOC cap.
func LoopbackOnlySegment(seg string) bool {
	re := regexp.MustCompile(`https?://[^/\s"']+`)
	found := false
	for _, m := range re.FindAllString(seg, -1) {
		found = true
		host := m
		host = strings.TrimPrefix(strings.TrimPrefix(host, "https://"), "http://")
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		host = strings.Trim(host, "[]")
		if host == "localhost" {
			continue
		}
		if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
			continue
		}
		return false
	}
	return found
}
