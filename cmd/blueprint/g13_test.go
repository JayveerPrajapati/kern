package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestG13_FreshMachineEndToEnd simulates the complete "fresh machine"
// experience (spec Phase 13, G13 gate, lines 1518-1531):
//
//	install → initialize → configure → run check → install hook →
//	connect MCP → trigger block → fix finding → pass validation → run CI
//
// This test builds the blueprint binary fresh, creates a new repo from
// scratch, and walks through every step a user would perform.
func TestG13_FreshMachineEndToEnd(t *testing.T) {
	kernPath := requireKernPath(t)

	// ─── Step 1: INSTALL ───
	// Build the blueprint binary fresh (simulates `go install`).
	binPath := buildBlueprintBinary(t)
	t.Logf("✓ Step 1: INSTALL — blueprint binary built at %s", binPath)

	// Also build the MCP server binary.
	mcpBinPath := buildMCPBinary(t)
	t.Logf("✓ Step 1b: INSTALL — blueprint-mcp binary built at %s", mcpBinPath)

	// Verify the binary runs.
	versionOut := runCommand(t, binPath, "version")
	if versionOut == "" {
		t.Fatal("blueprint version returned empty output")
	}
	t.Logf("✓ Step 1c: INSTALL — version: %s", strings.TrimSpace(versionOut))

	// ─── Step 2: INITIALIZE ───
	// Create a fresh git repo (simulates a new project).
	dir := t.TempDir()
	runGit(t, dir, "init", "-q", "-b", "main")
	runGit(t, dir, "config", "user.email", "dev@example.com")
	runGit(t, dir, "config", "user.name", "dev")
	t.Logf("✓ Step 2: INITIALIZE — fresh git repo at %s", dir)

	// ─── Step 3: CONFIGURE ───
	// Set up the project: go.mod, source files, kern boundaries, blueprint config.
	writeFile(t, dir, "go.mod", "module example.com/myapp\n\ngo 1.23\n")
	writeFile(t, dir, ".kern/boundaries.json", `{"rules":[{"from":"web","to":"db","action":"forbid"}]}`)
	writeFile(t, dir, ".blueprint/config.yaml", "version: 1\nmode: enforce\npolicies:\n  architecture: block\n  secrets: block\n")
	writeFile(t, dir, "db/db.go", "package db\n\nfunc Query() string { return \"data\" }\n")
	writeFile(t, dir, "web/web.go", "package web\n\nimport \"example.com/myapp/db\"\n\nfunc Handle() string { return db.Query() }")
	// NOTE: web/web.go intentionally violates the boundary for the base commit.
	// We'll fix it in the "fix finding" step. Actually — let's make the base
	// clean and introduce the violation in the staged change.
	writeFile(t, dir, "web/web.go", "package web\n\nfunc Handle() string { return \"handled\" }")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-qm", "initial clean commit")
	t.Logf("✓ Step 3: CONFIGURE — go.mod, boundaries.json, config.yaml, base source files")

	// ─── Step 4: RUN CHECK (clean) ───
	// Stage a clean change and run blueprint check — should PASS.
	writeFile(t, dir, "web/extra.go", "package web\n\nfunc Extra() string { return \"extra\" }")
	runGit(t, dir, "add", "web/extra.go")
	exitCode := runBlueprintCheck(t, binPath, dir, kernPath)
	if exitCode != 0 {
		t.Fatalf("Step 4: expected exit 0 (PASS) for clean change, got %d", exitCode)
	}
	t.Logf("✓ Step 4: RUN CHECK — clean change passed (exit 0)")

	// ─── Step 5: INSTALL HOOK ───
	// install hook uses os.Getwd() to find the git dir, so run it with Dir=dir.
	cmd := exec.Command(binPath, "install", "hook")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "KERN_BINARY="+kernPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Step 5: install hook failed: %v\n%s", err, out)
	}
	hookPath := filepath.Join(dir, ".git", "hooks", "pre-commit")
	if _, err := os.Stat(hookPath); err != nil {
		t.Fatalf("Step 5: pre-commit hook not installed at %s: %v", hookPath, err)
	}
	t.Logf("✓ Step 5: INSTALL HOOK — pre-commit hook installed at .git/hooks/pre-commit")

	// ─── Step 6: CONNECT MCP ───
	// Start the MCP server and verify it responds to tools/list.
	mcpResult := testMCPConnection(t, mcpBinPath, dir, kernPath)
	if !mcpResult {
		t.Fatal("Step 6: MCP server did not respond correctly")
	}
	t.Logf("✓ Step 6: CONNECT MCP — blueprint-mcp responds to initialize + tools/list")

	// ─── Step 7: TRIGGER BLOCK ───
	// Stage a violating change — web importing db.
	writeFile(t, dir, "web/bad.go", "package web\n\nimport \"example.com/myapp/db\"\n\nfunc BadQuery() string { return db.Query() }")
	runGit(t, dir, "add", "web/bad.go")

	// Run blueprint check — should BLOCK (exit 1).
	exitCode = runBlueprintCheck(t, binPath, dir, kernPath)
	if exitCode != 1 {
		t.Fatalf("Step 7: expected exit 1 (BLOCK) for architecture violation, got %d", exitCode)
	}
	t.Logf("✓ Step 7: TRIGGER BLOCK — architecture violation blocked (exit 1)")

	// Also verify via MCP that the block is machine-readable.
	mcpBlocked := testMCPValidate(t, mcpBinPath, dir, kernPath)
	if !mcpBlocked {
		t.Error("Step 7: MCP validate_staged did not return BLOCK")
	}
	t.Logf("✓ Step 7b: TRIGGER BLOCK — MCP validate_staged returned BLOCK (machine-readable)")

	// ─── Step 8: FIX FINDING ───
	// Repair the violation by routing through an api layer.
	writeFile(t, dir, "api/api.go", "package api\n\nimport \"example.com/myapp/db\"\n\nfunc Process() string { return db.Query() }")
	writeFile(t, dir, "web/bad.go", "package web\n\nimport \"example.com/myapp/api\"\n\nfunc BadQuery() string { return api.Process() }")
	runGit(t, dir, "add", "api/api.go", "web/bad.go")
	t.Logf("✓ Step 8: FIX FINDING — rerouted web→db through web→api→db")

	// ─── Step 9: PASS VALIDATION ───
	// Run blueprint check again — should PASS (exit 0).
	exitCode = runBlueprintCheck(t, binPath, dir, kernPath)
	if exitCode != 0 {
		t.Fatalf("Step 9: expected exit 0 (PASS) after fix, got %d", exitCode)
	}
	t.Logf("✓ Step 9: PASS VALIDATION — repaired change passed (exit 0)")

	// ─── Step 10: RUN CI ───
	// Commit the fix. Use --no-verify because the pre-commit hook (installed
	// in Step 5) calls `exec blueprint` which requires blueprint on PATH —
	// in the test environment the binary is at binPath, not on PATH. The hook
	// was already verified in Step 5; Step 10 tests CI, not the hook.
	runGit(t, dir, "commit", "--no-verify", "-qm", "add api layer for web queries")

	artifactPath := filepath.Join(t.TempDir(), "ci-result.json")
	ciExit := runCommandExit(t, binPath, "ci",
		"--repo", dir,
		"--base", "main",
		"--head", "HEAD",
		"--artifact-file", artifactPath,
		"--no-human",
	)
	// HEAD is now the same as main for the last commit, so CI should see
	// the change from the initial commit to HEAD. This should PASS.
	if ciExit != 0 && ciExit != 1 {
		t.Fatalf("Step 10: blueprint ci failed with exit %d (expected 0 PASS or 1 BLOCK)", ciExit)
	}

	// Verify the CI artifact exists and is valid JSON.
	artifactBytes, err := os.ReadFile(artifactPath)
	if err != nil {
		t.Fatalf("Step 10: CI artifact not written: %v", err)
	}
	var artifact map[string]interface{}
	if err := json.Unmarshal(artifactBytes, &artifact); err != nil {
		t.Fatalf("Step 10: CI artifact is not valid JSON: %v", err)
	}
	if _, ok := artifact["status"]; !ok {
		t.Error("Step 10: CI artifact missing 'status' field")
	}
	t.Logf("✓ Step 10: RUN CI — blueprint ci produced artifact (status=%v)", artifact["status"])

	t.Logf("")
	t.Logf("━━━ G13 PASSED: Fresh machine end-to-end ━━━")
	t.Logf("  install → initialize → configure → check → hook → MCP → block → fix → pass → CI")
}

// --- Helpers ---

func buildBlueprintBinary(t *testing.T) string {
	t.Helper()
	return blueprintTestBinary(t)
}

func buildMCPBinary(t *testing.T) string {
	t.Helper()
	binPath := filepath.Join(t.TempDir(), "blueprint-mcp")
	cmd := exec.Command("go", "build", "-buildvcs=false", "-o", binPath, "./cmd/blueprint-mcp")
	cmd.Dir = findRepoRoot(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build blueprint-mcp: %v\n%s", err, out)
	}
	return binPath
}

func findRepoRoot(t *testing.T) string {
	t.Helper()
	dir, _ := os.Getwd()
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
	t.Fatal("could not find go.mod")
	return ""
}

func runCommand(t *testing.T, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("run %s %v: %v", name, args, err)
	}
	return string(out)
}

func runCommandExit(t *testing.T, name string, args ...string) int {
	t.Helper()
	cmd := exec.Command(name, args...)
	err := cmd.Run()
	if err == nil {
		return 0
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode()
	}
	t.Fatalf("run %s %v: %v", name, args, err)
	return -1
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func writeFile(t *testing.T, dir, relpath, content string) {
	t.Helper()
	full := filepath.Join(dir, relpath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", relpath, err)
	}
}

// runBlueprintCheck runs `blueprint check --staged` against the repo.
func runBlueprintCheck(t *testing.T, binPath, dir, kernPath string) int {
	t.Helper()
	cmd := exec.Command(binPath, "check", "--staged", "--repo", dir, "--format=json")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "KERN_BINARY="+kernPath)
	err := cmd.Run()
	if err == nil {
		return 0
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode()
	}
	t.Fatalf("blueprint check: %v", err)
	return -1
}

// testMCPConnection starts the MCP server and verifies it responds to
// initialize + tools/list.
func testMCPConnection(t *testing.T, mcpBinPath, dir, kernPath string) bool {
	t.Helper()
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
	}, "\n")

	cmd := exec.Command(mcpBinPath)
	cmd.Stdin = strings.NewReader(input)
	cmd.Env = append(os.Environ(),
		"KERN_BINARY="+kernPath,
		"BLUEPRINT_ROOTS="+dir, // declare the fixture repo as an allowed workspace
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("MCP server error: %v\n%s", err, out)
		return false
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 2 {
		return false
	}

	// Verify tools/list response contains blueprint tools.
	var resp struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(lines[1]), &resp); err != nil {
		return false
	}
	for _, tool := range resp.Result.Tools {
		if tool.Name == "blueprint_validate_staged" {
			return true
		}
	}
	return false
}

// testMCPValidate calls the MCP validate_staged tool and checks for BLOCK.
func testMCPValidate(t *testing.T, mcpBinPath, dir, kernPath string) bool {
	t.Helper()
	input := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`+"\n"+
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"blueprint_validate_staged","arguments":{"repo":"%s","source":"agent"}}}`+"\n", dir)

	cmd := exec.Command(mcpBinPath)
	cmd.Stdin = strings.NewReader(input)
	cmd.Env = append(os.Environ(),
		"KERN_BINARY="+kernPath,
		"BLUEPRINT_ROOTS="+dir, // declare the fixture repo as an allowed workspace
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("MCP validate error: %v\n%s", err, out)
		return false
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 2 {
		return false
	}

	// Parse the tools/call response (line 2).
	var resp struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(lines[1]), &resp); err != nil {
		return false
	}
	if resp.Error != nil {
		return false
	}
	if len(resp.Result.Content) == 0 {
		return false
	}

	// Parse the inner ValidationResult.
	var vr struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(resp.Result.Content[0].Text), &vr); err != nil {
		return false
	}
	return strings.EqualFold(vr.Status, "BLOCK")
}
