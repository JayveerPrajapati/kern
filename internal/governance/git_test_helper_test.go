package governance

import (
	"os/exec"
	"testing"
)

// gitIn runs a git command in dir, failing the test on error, with the
// machine-global git hooks isolated (the same convention internal/index's
// testGit uses — fixture commits must not trip kern's global pre-commit).
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir, "-c", "core.hooksPath="}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return string(out)
}
