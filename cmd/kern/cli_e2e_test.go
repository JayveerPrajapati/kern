package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var (
	kernBinaryPath string
	buildKernErr   error
)

func TestMain(m *testing.M) {
	buildKernCLI()
	code := m.Run()
	if kernBinaryPath != "" {
		_ = os.RemoveAll(filepath.Dir(kernBinaryPath))
	}
	os.Exit(code)
}

func buildKernCLI() {
	dir, err := os.MkdirTemp("", "kern-cli-e2e-*")
	if err != nil {
		buildKernErr = err
		return
	}
	path := filepath.Join(dir, "kern")
	cmd := exec.Command("go", "build", "-o", path, ".")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		buildKernErr = fmt.Errorf("go build: %w\n%s", err, out.String())
		return
	}
	kernBinaryPath = path
}

func kernBinary(t *testing.T) string {
	t.Helper()
	if buildKernErr != nil {
		t.Fatalf("build kern CLI: %v", buildKernErr)
	}
	return kernBinaryPath
}

func runKernCLI(t *testing.T, args ...string) (string, string, error) {
	t.Helper()

	cmd := exec.Command(kernBinary(t), args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

// runKernCLIRooted runs the CLI with KERN_ROOTS set to the supplied root.
func runKernCLIRooted(t *testing.T, root string, args ...string) (string, string, error) {
	t.Helper()
	t.Setenv("KERN_ROOTS", root)
	t.Setenv("KERN_MCP_ROOTS", root)
	return runKernCLI(t, args...)
}

// runKernCLIJSON runs the CLI and parses stdout as JSON.
func runKernCLIJSON(t *testing.T, args ...string) (map[string]any, error) {
	t.Helper()
	stdout, _, err := runKernCLI(t, args...)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		return nil, err
	}
	return result, nil
}

// withTempHOME isolates the CLI from the developer's real home directory and
// disables background index preloading during the test.
func withTempHOME(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("KERN_PRELOAD", "0")
	return home
}

func TestCLI_kern_version(t *testing.T) {
	withTempHOME(t)

	stdout, stderr, err := runKernCLI(t, "version")
	if err != nil {
		t.Fatalf("kern version: %v\n%s", err, stderr)
	}
	if strings.TrimSpace(stdout) == "" {
		t.Fatalf("kern version produced empty stdout")
	}
	if strings.TrimSpace(stderr) != "" {
		t.Fatalf("kern version produced unexpected stderr: %s", stderr)
	}
}

func TestCLI_kern_help(t *testing.T) {
	withTempHOME(t)

	stdout, stderr, err := runKernCLI(t, "--help")
	if err != nil {
		t.Fatalf("kern --help: %v\n%s", err, stderr)
	}
	combined := stdout + stderr
	if !strings.Contains(combined, "kern") {
		t.Fatalf("kern --help did not mention kern:\n%s", combined)
	}
}

func TestCLI_kern_help_all(t *testing.T) {
	withTempHOME(t)

	stdout, stderr, err := runKernCLI(t, "--all")
	if err != nil {
		t.Fatalf("kern --all: %v\n%s", err, stderr)
	}
	if !strings.Contains(stdout, "kern") {
		t.Fatalf("kern --all did not produce the catalog header:\n%s", stdout)
	}
}
