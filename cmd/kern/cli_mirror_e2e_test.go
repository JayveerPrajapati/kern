package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/testfixture"
)

// runKernCLIBounded runs the real CLI with a hard timeout and output-size
// limit. It is the safe mirror runner for e2e commands that can become slow
// or chatty under a broken build.
func runKernCLIBounded(t *testing.T, timeout time.Duration, maxBytes int, args ...string) (string, string, error) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, kernBinary(t), args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if stdout.Len() > maxBytes {
		t.Fatalf("stdout exceeded %d bytes: got %d", maxBytes, stdout.Len())
	}
	if stderr.Len() > maxBytes {
		t.Fatalf("stderr exceeded %d bytes: got %d", maxBytes, stderr.Len())
	}
	return stdout.String(), stderr.String(), err
}

func TestCLI_kern_search_json(t *testing.T) {
	withTempHOME(t)
	root := testfixture.Repo(t)
	t.Setenv("KERN_ROOTS", root)
	t.Setenv("KERN_MCP_ROOTS", root)

	stdout, stderr, err := runKernCLIBounded(t, 30*time.Second, 64<<10,
		"search", "NewServer", "--root", root, "--json", "--limit", "5")
	if err != nil {
		t.Fatalf("kern search --json: %v\n%s", err, stderr)
	}
	var payload any
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("kern search --json returned invalid JSON: %v\n%s", err, stdout)
	}
	if payload == nil {
		t.Fatal("kern search --json returned null")
	}
	if !strings.Contains(stdout, "NewServer") {
		t.Fatalf("kern search --json missing NewServer:\n%s", stdout)
	}
}

func TestCLI_kern_doctor_json(t *testing.T) {
	withTempHOME(t)
	root := testfixture.Repo(t)
	t.Setenv("KERN_ROOTS", root)
	t.Setenv("KERN_MCP_ROOTS", root)

	stdout, stderr, err := runKernCLIBounded(t, 60*time.Second, 128<<10,
		"doctor", "--root", root, "--json")
	if err != nil {
		t.Fatalf("kern doctor --json: %v\n%s", err, stderr)
	}
	var payload any
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("kern doctor --json returned invalid JSON: %v\n%s", err, stdout)
	}
	if payload == nil {
		t.Fatal("kern doctor --json returned null")
	}
}

func TestCLI_kern_mcp_tools_json(t *testing.T) {
	withTempHOME(t)

	stdout, stderr, err := runKernCLIBounded(t, 30*time.Second, 256<<10,
		"mcp", "tools", "--json")
	if err != nil {
		t.Fatalf("kern mcp tools --json: %v\n%s", err, stderr)
	}
	var payload any
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("kern mcp tools --json returned invalid JSON: %v\n%s", err, stdout)
	}
	if !strings.Contains(stdout, "kern_search") {
		t.Fatalf("kern mcp tools --json missing kern_search:\n%s", stdout)
	}
}

func TestCLI_kern_impact_json(t *testing.T) {
	withTempHOME(t)
	root := testfixture.Repo(t)
	t.Setenv("KERN_ROOTS", root)
	t.Setenv("KERN_MCP_ROOTS", root)

	stdout, stderr, err := runKernCLIBounded(t, 60*time.Second, 128<<10,
		"impact", "NewServer", "--root", root, "--json")
	if err != nil {
		t.Fatalf("kern impact --json: %v\n%s", err, stderr)
	}
	var report map[string]any
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("kern impact --json returned invalid JSON: %v\n%s", err, stdout)
	}
	if !strings.Contains(stdout, "NewServer") {
		t.Fatalf("kern impact --json missing NewServer:\n%s", stdout)
	}
}

func TestCLI_kern_verify_architecture_json(t *testing.T) {
	withTempHOME(t)
	root := testfixture.Repo(t)
	t.Setenv("KERN_ROOTS", root)
	t.Setenv("KERN_MCP_ROOTS", root)

	stdout, stderr, err := runKernCLIBounded(t, 120*time.Second, 256<<10,
		"verify", "architecture", "--root", root, "--json")
	if err != nil {
		t.Fatalf("kern verify architecture --json: %v\n%s", err, stderr)
	}
	var report map[string]any
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("kern verify architecture --json returned invalid JSON: %v\n%s", err, stdout)
	}
	if len(report) == 0 {
		t.Fatal("kern verify architecture --json returned an empty object")
	}
}

func TestCLI_kern_meta_explore(t *testing.T) {
	withTempHOME(t)
	root := testfixture.Repo(t)
	t.Setenv("KERN_ROOTS", root)
	t.Setenv("KERN_MCP_ROOTS", root)

	stdout, stderr, err := runKernCLIBounded(t, 60*time.Second, 128<<10,
		"meta", "how does NewServer work?", "--root", root)
	if err != nil {
		t.Fatalf("kern meta: %v\n%s", err, stderr)
	}
	if !strings.Contains(stdout, "kern_explore") {
		t.Fatalf("kern meta did not route to kern_explore:\n%s", stdout)
	}
	if !strings.Contains(stdout, "NewServer") {
		t.Fatalf("kern meta missing NewServer result:\n%s", stdout)
	}
}

func TestCLI_kern_explore_json(t *testing.T) {
	withTempHOME(t)
	root := testfixture.Repo(t)
	t.Setenv("KERN_ROOTS", root)
	t.Setenv("KERN_MCP_ROOTS", root)

	stdout, stderr, err := runKernCLIBounded(t, 60*time.Second, 128<<10,
		"explore", "NewServer", "--root", root, "--json")
	if err != nil {
		t.Fatalf("kern explore --json: %v\n%s", err, stderr)
	}
	var payload any
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("kern explore --json returned invalid JSON: %v\n%s", err, stdout)
	}
	if !strings.Contains(stdout, "NewServer") {
		t.Fatalf("kern explore --json missing NewServer:\n%s", stdout)
	}
}

func TestCLI_kern_graph_json(t *testing.T) {
	withTempHOME(t)
	root := testfixture.Repo(t)
	t.Setenv("KERN_ROOTS", root)
	t.Setenv("KERN_MCP_ROOTS", root)

	stdout, stderr, err := runKernCLIBounded(t, 60*time.Second, 128<<10,
		"graph", "NewServer", "--root", root, "--json")
	if err != nil {
		t.Fatalf("kern graph --json: %v\n%s", err, stderr)
	}
	var payload any
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("kern graph --json returned invalid JSON: %v\n%s", err, stdout)
	}
	if !strings.Contains(stdout, "NewServer") {
		t.Fatalf("kern graph --json missing NewServer:\n%s", stdout)
	}
}

func TestCLI_kern_optimize_prompt(t *testing.T) {
	withTempHOME(t)
	root := testfixture.Repo(t)
	t.Setenv("KERN_ROOTS", root)
	t.Setenv("KERN_MCP_ROOTS", root)
	t.Setenv("KERN_LLM_PROVIDER", "none")

	stdout, stderr, err := runKernCLIBounded(t, 60*time.Second, 128<<10,
		"optimize", "--kind", "prompt", "please compress this prompt for me")
	if err != nil {
		t.Fatalf("kern optimize: %v\n%s", err, stderr)
	}
	if strings.TrimSpace(stdout) == "" {
		t.Fatal("kern optimize returned empty output")
	}
}

func TestCLI_kern_review_json(t *testing.T) {
	withTempHOME(t)
	root := testfixture.Generate(t, testfixture.RepoSpec{
		Files:          2,
		SymbolsPerFile: 2,
		DeepCallGraph:  true,
		DirtyGit:       true,
	})
	t.Setenv("KERN_ROOTS", root)
	t.Setenv("KERN_MCP_ROOTS", root)

	stdout, stderr, err := runKernCLIBounded(t, 60*time.Second, 128<<10,
		"review", "--root", root, "--file", "main.go", "--json")
	if err != nil && !strings.Contains(stderr, "exit 1 (findings)") {
		t.Fatalf("kern review --json: %v\n%s", err, stderr)
	}
	var payload any
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("kern review --json returned invalid JSON: %v\n%s", err, stdout)
	}
	if !strings.Contains(stdout, `"files"`) || !strings.Contains(stdout, `"changes"`) {
		t.Fatalf("kern review --json missing files/changes:\n%s", stdout)
	}
}

func TestCLI_kern_buddy(t *testing.T) {
	withTempHOME(t)
	root := testfixture.Repo(t)
	t.Setenv("KERN_ROOTS", root)
	t.Setenv("KERN_MCP_ROOTS", root)

	stdout, stderr, err := runKernCLIBounded(t, 60*time.Second, 128<<10,
		"buddy", root)
	if err != nil {
		t.Fatalf("kern buddy: %v\n%s", err, stderr)
	}
	if !strings.Contains(stdout, "# kern buddy briefing") {
		t.Fatalf("kern buddy missing briefing header:\n%s", stdout)
	}
}

func TestCLI_kern_context(t *testing.T) {
	withTempHOME(t)
	root := testfixture.Repo(t)
	t.Setenv("KERN_ROOTS", root)
	t.Setenv("KERN_MCP_ROOTS", root)

	stdout, stderr, err := runKernCLIBounded(t, 60*time.Second, 128<<10,
		"context", "NewServer", root)
	if err != nil {
		t.Fatalf("kern context: %v\n%s", err, stderr)
	}
	if !strings.Contains(stdout, "NewServer") {
		t.Fatalf("kern context missing NewServer:\n%s", stdout)
	}
}

func TestCLI_kern_probe_json(t *testing.T) {
	withTempHOME(t)
	root := testfixture.Repo(t)
	t.Setenv("KERN_ROOTS", root)
	t.Setenv("KERN_MCP_ROOTS", root)

	stdout, stderr, err := runKernCLIBounded(t, 60*time.Second, 128<<10,
		"probe", "Explain how NewServer is wired to its callers", root, "--json")
	if err != nil {
		t.Fatalf("kern probe --json: %v\n%s", err, stderr)
	}
	var payload any
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("kern probe --json returned invalid JSON: %v\n%s", err, stdout)
	}
	if !strings.Contains(stdout, "NewServer") {
		t.Fatalf("kern probe --json missing NewServer:\n%s", stdout)
	}
}

func TestCLI_kern_research_json(t *testing.T) {
	withTempHOME(t)
	root := testfixture.Repo(t)
	t.Setenv("KERN_ROOTS", root)
	t.Setenv("KERN_MCP_ROOTS", root)

	stdout, stderr, err := runKernCLIBounded(t, 60*time.Second, 128<<10,
		"research", "Compare NewServer and NewUserService responsibilities", root, "--json")
	if err != nil {
		t.Fatalf("kern research --json: %v\n%s", err, stderr)
	}
	var payload any
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("kern research --json returned invalid JSON: %v\n%s", err, stdout)
	}
	if !strings.Contains(stdout, "NewServer") {
		t.Fatalf("kern research --json missing NewServer:\n%s", stdout)
	}
}

func TestCLI_catalog_help_contract(t *testing.T) {
	withTempHOME(t)

	catalog, stderr, err := runKernCLIBounded(t, 30*time.Second, 256<<10, "--all")
	if err != nil {
		t.Fatalf("kern --all: %v\n%s", err, stderr)
	}
	commandRE := regexp.MustCompile(`(?m)^\s{2}kern ([a-z0-9-]+)`)
	matches := commandRE.FindAllStringSubmatch(catalog, -1)
	if len(matches) < 40 {
		t.Fatalf("kern --all catalog unexpectedly small (%d commands):\n%s", len(matches), catalog)
	}

	seen := make(map[string]bool)
	for _, match := range matches {
		command := match[1]
		if seen[command] {
			continue
		}
		seen[command] = true
		stdout, stderr, err := runKernCLIBounded(t, 10*time.Second, 64<<10, command, "--help")
		if err != nil {
			t.Errorf("kern %s --help: %v\n%s", command, err, stderr)
			continue
		}
		combined := stdout + stderr
		if !strings.Contains(combined, "usage:") && !strings.Contains(combined, "Usage:") {
			t.Errorf("kern %s --help has no usage contract:\n%s", command, combined)
		}
	}
	if len(seen) < 40 {
		t.Fatalf("fewer than 40 unique CLI commands exercised: %d", len(seen))
	}
}
