package intel

import (
	"context"
	"strings"
	"testing"
)

// TestValidateRangeSides (audit F1, 2026-11-21): rev-range sides reaching git
// argv must be bare revisions. `--output=<path>` wrote the repo diff to
// arbitrary paths and `--ext-diff=<cmd>` attempted command execution before
// this validation existed; both must now be rejected before git runs.
func TestValidateRangeSides(t *testing.T) {
	for _, bad := range []string{
		"--output=/tmp/pwn",
		"--ext-diff=touch /tmp/pwn",
		"-O/tmp/orderfile",
		"-",
		"--",
		"HEAD; rm -rf /",
		"HEAD $(touch /tmp/pwn)",
		"HEAD `id`",
		"main feature", // embedded whitespace
		"\tHEAD",
		"HEAD\n",
		"--output=/dev/null; rm -rf /tmp/x",
	} {
		if err := validateRangeSides(bad, ""); err == nil {
			t.Errorf("from-side %q must be rejected", bad)
		}
		if err := validateRangeSides("", bad); err == nil {
			t.Errorf("to-side %q must be rejected", bad)
		}
	}
	for _, good := range []string{"", "HEAD", "HEAD~5", "main", "origin/main", "feature/x", "abc123def456", "v1.0.0", "a", "9", "HEAD^", "user@remote", "a%b=c+d-e"} {
		if err := validateRangeSides(good, ""); err != nil {
			t.Errorf("from-side %q must be accepted: %v", good, err)
		}
		if err := validateRangeSides("", good); err != nil {
			t.Errorf("to-side %q must be accepted: %v", good, err)
		}
	}
}

// TestFilesForRangeRejectsFlagInjection: the flag-injection payloads from the
// audit must fail with the validation error BEFORE any git invocation (a
// missing git repo would produce a different error, so a temp dir proves the
// ordering).
func TestFilesForRangeRejectsFlagInjection(t *testing.T) {
	dir := t.TempDir()
	for _, payload := range []string{
		"--output=" + dir + "/pwned ",
		"--ext-diff=touch " + dir + "/marker ",
	} {
		if _, err := FilesForRange(dir, payload, ""); err == nil || !strings.Contains(err.Error(), "invalid git range") {
			t.Errorf("FilesForRange payload %q must fail with the validation error, got: %v", payload, err)
		}
		if _, err := FilesForRangeL(dir, payload, ""); err == nil || !strings.Contains(err.Error(), "invalid git range") {
			t.Errorf("FilesForRangeL payload %q must fail with the validation error, got: %v", payload, err)
		}
		if _, err := ChurnContext(context.Background(), dir, payload, ""); err == nil || !strings.Contains(err.Error(), "invalid git range") {
			t.Errorf("ChurnContext payload %q must fail with the validation error, got: %v", payload, err)
		}
		if _, err := CoChangeContext(context.Background(), dir, payload, ""); err == nil || !strings.Contains(err.Error(), "invalid git range") {
			t.Errorf("CoChangeContext payload %q must fail with the validation error, got: %v", payload, err)
		}
	}
}
