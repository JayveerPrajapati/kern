package main

import "testing"

// TestMachineDataRemovedBy pins the F-A8 residual guard (2026-11-22 dogfood
// campaign): only invocations that actually remove machine data skip the
// exit-time metrics persist — the persist re-created ~/.cache/kern after a
// full uninstall, leaving the exact residue docs/configuration.md:293 promised
// was gone.
func TestMachineDataRemovedBy(t *testing.T) {
	cases := []struct {
		name string
		cmd  string
		rest []string
		want bool
	}{
		{"full uninstall", "uninstall", []string{"--yes"}, true},
		{"bare uninstall", "uninstall", nil, true},
		{"uninstall prefix-only", "uninstall", []string{"--prefix-only", "--yes"}, false},
		{"uninstall project-only", "uninstall", []string{"--project-only", "--yes"}, false},
		{"uninstall dry-run", "uninstall", []string{"--dry-run"}, false},
		{"setup remove data", "setup", []string{"--remove", "--data", "--yes"}, true},
		{"setup remove project", "setup", []string{"--remove", "--project", "--yes"}, false},
		{"setup remove data dry-run", "setup", []string{"--remove", "--data", "--dry-run"}, false},
		{"setup plain", "setup", []string{"--yes"}, false},
		{"version", "version", nil, false},
	}
	for _, tc := range cases {
		if got := machineDataRemovedBy(tc.cmd, tc.rest); got != tc.want {
			t.Errorf("%s: machineDataRemovedBy(%q, %v) = %v, want %v", tc.name, tc.cmd, tc.rest, got, tc.want)
		}
	}
}
