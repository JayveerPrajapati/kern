package cliflags

import "strings"

// SplitRange parses a git range "a..b" into (from, to). A single ref is kept
// as "from" with "to" empty, which git treats as "compare to working tree".
// It is the shared home of the previously triplicated splitRange helper
// (cmd/kern/helpers.go, internal/graphcli/helpers.go,
// internal/contextcli/guard.go — consolidation tranche 8); call sites use it
// directly, no local wrapper.
func SplitRange(r string) (string, string) {
	if r == "" {
		return "", ""
	}
	if p := strings.SplitN(r, "..", 2); len(p) == 2 {
		return p[0], p[1]
	}
	return r, ""
}
