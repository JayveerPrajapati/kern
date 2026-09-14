package kern

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// blueprintBinPath is the cached path to a freshly-built blueprint binary,
// shared across G2 CLI tests in this package.
var blueprintBinPath string

// blueprintModRoot returns the blueprint repository root (the dir containing
// go.mod), walking up from the test's working directory. Note: this is
// distinct from the existing repoRoot() helper in architecture_test.go,
// which returns a temp dir with a .kern/ folder for unit tests.
func blueprintModRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for i := 0; i < 10; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatal("could not find go.mod walking up from " + dir)
	return ""
}

// buildBlueprintBinary builds the blueprint CLI binary once and caches its
// path. Tests that need the CLI call this to get the binary path.
func buildBlueprintBinary(t *testing.T) string {
	t.Helper()
	if blueprintBinPath != "" {
		if _, err := os.Stat(blueprintBinPath); err == nil {
			return blueprintBinPath
		}
	}
	binDir := t.TempDir()
	binPath := filepath.Join(binDir, "blueprint")
	cmd := exec.Command("go", "build", "-buildvcs=false", "-o", binPath, "./cmd/blueprint")
	cmd.Dir = blueprintModRoot(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build blueprint binary: %v\n%s", err, out)
	}
	blueprintBinPath = binPath
	return binPath
}

// runBlueprintCheck runs `blueprint check` with the given args against repo
// and returns (combined output, exit code).
func runBlueprintCheck(t *testing.T, binPath, repo string, extraArgs ...string) (string, int) {
	t.Helper()
	args := append([]string{"check", "--repo", repo}, extraArgs...)
	cmd := exec.Command(binPath, args...)
	// Ensure the kern binary is discoverable by the blueprint subprocess.
	// KERN_BINARY is inherited from the test env if set; otherwise blueprint
	// resolves via $PATH or ../kern/bin/kern.
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		} else {
			t.Fatalf("run blueprint check: %v\n%s", err, out)
		}
	}
	return string(out), code
}

