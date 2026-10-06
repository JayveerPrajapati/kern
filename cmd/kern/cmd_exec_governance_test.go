package main

import (
	"strings"
	"testing"
)

// TestRunBuildRejectsCommandSubstitution locks the mode-4 fix: shell
// command-substitution constructs (backticks / $(...)) must be rejected
// BEFORE governance and BEFORE any execution — RunBuild's sh -c would
// evaluate the substitution blind (verified live: `echo \`touch /tmp/x\“
// created the file). The denial must name the construct and the workaround
// (file + -F), and exit as a policy denial (3). A substitution-free command
// must NOT be rejected by this guard.
func TestRunBuildRejectsCommandSubstitution(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("KERN_ALLOW_EXEC", "1") // benign control command may run

	cases := []string{
		"echo `date`",
		"echo $(whoami)",
		"echo pre`touch /tmp/never-created`post",
		"git commit -m \"msg`kern approve appr-x`\"",
	}
	for _, cmd := range cases {
		stderr, code := captureStderrExit(t, func() {
			runBuild([]string{cmd})
		})
		if code != 3 {
			t.Fatalf("runBuild(%q) exit = %d, want 3 (policy denial)", cmd, code)
		}
		for _, want := range []string{"command-substitution", "backtick", "-F"} {
			if !strings.Contains(stderr, want) {
				t.Fatalf("runBuild(%q) denial missing %q, got: %.300s", cmd, want, stderr)
			}
		}
	}

	// Positive control: a substitution-free command is not rejected by this
	// guard — with KERN_ALLOW_EXEC=1 it is allowlisted and runs.
	_, code := captureStderrExit(t, func() {
		runBuild([]string{"echo hello"})
	})
	if code != 0 {
		t.Fatalf("substitution-free command must not be rejected by the substitution guard (exit %d)", code)
	}
}
