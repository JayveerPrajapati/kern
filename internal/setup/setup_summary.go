package setup

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// CheckSummary derives a one-line summary of the setup --check table from
// the adapter registry and the check results — never from hardcoded
// literals, so the line tracks the registry automatically and cannot drift
// from what setup actually supports. The four counts map to the documented
// numbers: N distinct agents (the README's "11 MCP clients": distinct names
// in the adapter registry, custom adapters included), M config surfaces
// wired (registry entries whose config file currently carries a kern entry),
// K rows checked (the full table), and P/T agents detected on this system
// (the honest companion to M: pre-wiring writes surfaces for agents that may
// not be installed yet, so M alone would imply every agent is in use).
func CheckSummary(root string, results []Status) string {
	agentNames, _ := RegistryCounts(root)
	wired := 0
	for _, s := range results {
		if s.Installed && agentNames[s.Agent] {
			wired++
		}
	}
	detected := DetectAgents(root)
	return fmt.Sprintf("%d MCP clients · %d config surfaces wired · %d checked · %d/%d agents detected",
		len(agentNames), wired, len(results), len(detected), len(agentDetectors))
}

// WiredState reports the project's ACTUAL wiring state from setup's own
// registry (Check), for displays that must not claim "none" when a project
// was already wired by a previous run (e.g. `kern onboard` in a fixture that
// `kern setup` wired). Only project-scoped surfaces are listed (their config
// path lives under root); user-global wiring (home MCP adapters, global
// rules, global hooks) is not part of a project's wired state. Returns
// "none" only when nothing is wired.
func WiredState(root string) string {
	var labels []string
	seen := map[string]bool{}
	prefix := filepath.Clean(root) + string(filepath.Separator)
	for _, s := range Check(root) {
		if !s.Installed || s.Agent == "" || s.Path == "" || seen[s.Agent] {
			continue
		}
		p := filepath.Clean(s.Path)
		if p != filepath.Clean(root) && !strings.HasPrefix(p, prefix) {
			continue // user-global surface — not part of this project's wiring
		}
		seen[s.Agent] = true
		labels = append(labels, s.Agent)
	}
	sort.Strings(labels)
	if len(labels) == 0 {
		return "none"
	}
	return "present: " + strings.Join(labels, ", ")
}
