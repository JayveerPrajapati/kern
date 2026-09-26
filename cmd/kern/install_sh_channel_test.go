package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	kversion "github.com/JayveerPrajapati/kern/internal/version"
)

// TestInstallShChannelRE2OnlyMatchesGo is the two-way contract test for the
// channel guard: install.sh's channel_re2_only (POSIX sh case patterns) and
// Go's ResolveChannel (RE2 ereUnsupportedRe) must make the SAME accept/reject
// decision for every channel pattern. Neither side can drift: if one stops
// recognizing \d-style escapes (or starts rejecting ERE-safe patterns), the
// verdicts diverge and this test fails. The sh side runs the REAL install.sh
// function by sourcing the script with the documented KERN_SKIP_DISPATCH=1
// testing override (functions only, no dispatch side effects).
func TestInstallShChannelRE2OnlyMatchesGo(t *testing.T) {
	script := filepath.Join(repoRoot(t), "install.sh")
	if _, err := os.Stat(script); err != nil {
		t.Fatalf("install.sh not found: %v", err)
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skipf("install.sh channel test: sh not available: %v", err)
	}

	// The shared pattern table (finding 9's contract): RE2-only constructs
	// are rejected by BOTH sides; ERE-safe channels are accepted by BOTH.
	tags := []string{"v0.9.9.1", "v0.9.9", "v0.9.5.2"}
	cases := []struct {
		pattern string
		reject  bool // RE2-only -> rejected by both sides
	}{
		{pattern: `^v0\.9\.`, reject: false},
		{pattern: "latest", reject: false},
		{pattern: "stable", reject: false},
		{pattern: `\d+`, reject: true},
		{pattern: `(?i)v`, reject: true},
		{pattern: `\w`, reject: true},
		{pattern: `\s`, reject: true},
		{pattern: `\b`, reject: true},
	}
	for _, tc := range cases {
		t.Run(tc.pattern, func(t *testing.T) {
			// Go side: ResolveChannel's RE2-only rejection.
			_, goErr := kversion.ResolveChannel(tags, tc.pattern)
			goReject := goErr != nil && strings.Contains(goErr.Error(), "RE2-only")
			if tc.reject && !goReject {
				t.Fatalf("Go ResolveChannel(%q) = %v, want RE2-only rejection", tc.pattern, goErr)
			}
			if !tc.reject && goErr != nil {
				t.Fatalf("Go ResolveChannel(%q) must accept, got: %v", tc.pattern, goErr)
			}

			// sh side: the real install.sh channel_re2_only (exit 0 =
			// RE2-only). The script is sourced with the documented
			// KERN_SKIP_DISPATCH=1 override and NO positional arguments —
			// install.sh's load-time loop exits on any unknown positional,
			// so the pattern travels via an env var instead.
			cmd := exec.Command("sh", "-c",
				`KERN_SKIP_DISPATCH=1 . "$KERN_CHANNEL_SCRIPT"; channel_re2_only "$KERN_CHANNEL_PATTERN"`)
			cmd.Env = append(os.Environ(),
				"KERN_CHANNEL_SCRIPT="+script,
				"KERN_CHANNEL_PATTERN="+tc.pattern)
			out, err := cmd.CombinedOutput()
			exitCode := 0
			if err != nil {
				var ee *exec.ExitError
				if errors.As(err, &ee) {
					exitCode = ee.ExitCode()
				} else {
					t.Fatalf("channel_re2_only %q could not run: %v\n%s", tc.pattern, err, out)
				}
			}
			// The function's contract is a verdict, not an error: exit 0 =
			// RE2-only, exit 1 = safe. Anything else means the function did
			// not run (e.g. the source failed).
			if exitCode != 0 && exitCode != 1 {
				t.Fatalf("channel_re2_only %q exited %d (want 0 or 1):\n%s", tc.pattern, exitCode, out)
			}
			shReject := exitCode == 0

			// Two-way verdict equality: neither side may drift from the
			// shared table or from each other.
			if shReject != goReject {
				t.Fatalf("verdict drift for channel %q: Go rejects=%v, install.sh rejects=%v", tc.pattern, goReject, shReject)
			}
			if shReject != tc.reject {
				t.Fatalf("install.sh channel_re2_only(%q) = %v, want %v (shared table)", tc.pattern, shReject, tc.reject)
			}
		})
	}
}
