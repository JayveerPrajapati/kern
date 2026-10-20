package main

import (
	"regexp"
	"strings"
	"testing"
)

// usageAdvertisedCmd matches the command name in every help line that
// advertises a kern invocation (two-space indent, "kern ", then the name).
// Only the FIRST token after "kern " is captured: subcommand spellings
// ("kern mcp tools", "kern evidence export") advertise their parent
// command, which must itself be registered.
var usageAdvertisedCmd = regexp.MustCompile(`(?m)^  kern ([a-z][a-z0-9-]*)`)

// TestUsageAdvertisesRegisteredCommands pins the main usage banner against
// the dispatch table: every command the banner advertises must be a real
// commandTable entry. The banner is hand-maintained prose that no other
// gate covers (dispatch usage strings are only checked against
// docs/cli-reference.md), so dead commands survived the P4/P5 deletions
// (mutate, rename, refactor, pack, web were advertised but unknown).
func TestUsageAdvertisesRegisteredCommands(t *testing.T) {
	out := captureStderr(t, usage)
	matches := usageAdvertisedCmd.FindAllStringSubmatch(out, -1)
	if len(matches) < 5 {
		t.Fatalf("usage() advertised only %d commands — the banner lost its workflow list", len(matches))
	}
	seen := map[string]bool{}
	for _, m := range matches {
		name := m[1]
		if seen[name] {
			continue
		}
		seen[name] = true
		if _, ok := commandTable[name]; !ok {
			t.Errorf("usage() advertises %q, but it is not a registered command (run 'kern --all' for the live catalog)", name)
		}
	}
	// The five essential verbs must stay the backbone of the banner.
	for _, verb := range []string{"meta", "explore", "search", "plan", "verify"} {
		if !seen[verb] {
			t.Errorf("usage() no longer advertises core verb %q", verb)
		}
	}
	if !strings.Contains(out, "kern --all") {
		t.Error("usage() must point at 'kern --all' for the full catalog")
	}
}
