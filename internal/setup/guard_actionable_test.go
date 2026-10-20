package setup

import (
	"strings"
	"testing"
)

// The guard's deny messages must hand the agent a concrete next call that
// exists on the DEFAULT MCP surface. The glob redirect used to name only
// kern_project_map (KERN_MCP_FULL=1 only), a dead end on a default install;
// the bash redirect listed tools without saying which command maps to which.
func TestGuardDenyMessagesAreActionable(t *testing.T) {
	reasons := map[string]string{}
	for _, line := range strings.Split(kernGuardScript, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, `reason="`) {
			continue
		}
		switch {
		case strings.Contains(line, "kern_verify"):
			reasons["bash"] = line
		case strings.Contains(line, "kern_project_map"):
			reasons["glob"] = line
		}
	}

	glob := reasons["glob"]
	if glob == "" {
		t.Fatal("glob deny reason not found in kern-guard.sh")
	}
	buddy := strings.Index(glob, "kern_buddy")
	projectMap := strings.Index(glob, "kern_project_map")
	if buddy < 0 || buddy > projectMap {
		t.Errorf("glob deny must lead with kern_buddy (default surface) before kern_project_map (full only): %s", glob)
	}

	bash := reasons["bash"]
	if bash == "" {
		t.Fatal("bash deny reason not found in kern-guard.sh")
	}
	for _, want := range []string{
		"not in the passthrough set",
		"use $repl instead",
		"build/test: kern_verify",
		"unsure: kern_meta",
	} {
		if !strings.Contains(bash, want) {
			t.Errorf("bash deny reason missing %q", want)
		}
	}
	// The per-command replacement map (3.1-A) must exist in the script with
	// default-surface tools only and default to the NL router, so every deny
	// hands the agent a concrete next call that exists on a default install.
	for _, want := range []string{
		`grep|rg|ag) repl="kern_search`,
		`cat|head|tail|sed|awk|less|more) repl="kern_explore`,
		`find) repl="kern_buddy"`,
		`go|make) repl="kern_verify`,
		`*) repl="kern_meta`,
	} {
		if !strings.Contains(kernGuardScript, want) {
			t.Errorf("guard script missing per-command replacement %q", want)
		}
	}
}
