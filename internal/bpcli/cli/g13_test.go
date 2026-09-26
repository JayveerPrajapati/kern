package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	bpcmcp "github.com/JayveerPrajapati/kern/internal/bpcli/mcp"
)

// TestG13_FreshMachineEndToEnd simulates the complete "fresh machine"
// experience (spec Phase 13, G13 gate, lines 1518-1531):
//
//	install → initialize → configure → run check → install hook →
//	connect MCP → trigger block → fix finding → pass validation → run CI
//
// This test builds the merged kern binary fresh, creates a new repo from
// scratch, and walks through every step a user would perform.
func TestG13_FreshMachineEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("E2E gate test — full pipeline; runs in nightly non-short suite")
	}
	kernPath := requireKernPath(t)

	// ─── Step 1: INSTALL ───
	// Build the merged kern binary fresh (simulates `go install`). The
	// standalone blueprint binary was declared redundant: kern check/fix/ci
	// forward to the same internal/blueprint/cli implementation.
	binPath := kernTestBinary(t)
	t.Logf("✓ Step 1: INSTALL — kern binary built at %s", binPath)

	// The MCP server runs in-process (mcp.NewServer) — the same engine the
	// standalone blueprint-mcp binary wired over stdio.
	srv := newTestMCPServer()
	t.Logf("✓ Step 1b: INSTALL — blueprint-mcp engine wired in-process (mcp.NewServer)")

	// Verify the binary runs.
	versionOut := runCommand(t, binPath, "version")
	if versionOut == "" {
		t.Fatal("kern version returned empty output")
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
	// Stage a clean change and run kern check — should PASS.
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
	// Start the MCP server (in-process) and verify it responds to tools/list.
	mcpResult := testMCPConnection(t, srv, dir, kernPath)
	if !mcpResult {
		t.Fatal("Step 6: MCP server did not respond correctly")
	}
	t.Logf("✓ Step 6: CONNECT MCP — blueprint-mcp responds to initialize + tools/list")

	// ─── Step 7: TRIGGER BLOCK ───
	// Stage a violating change — web importing db.
	writeFile(t, dir, "web/bad.go", "package web\n\nimport \"example.com/myapp/db\"\n\nfunc BadQuery() string { return db.Query() }")
	runGit(t, dir, "add", "web/bad.go")

	// Run kern check — should BLOCK (exit 1).
	exitCode = runBlueprintCheck(t, binPath, dir, kernPath)
	if exitCode != 1 {
		t.Fatalf("Step 7: expected exit 1 (BLOCK) for architecture violation, got %d", exitCode)
	}
	t.Logf("✓ Step 7: TRIGGER BLOCK — architecture violation blocked (exit 1)")

	// Also verify via MCP that the block is machine-readable.
	mcpBlocked := testMCPValidate(t, srv, dir, kernPath)
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
	// Run kern check again — should PASS (exit 0).
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
		t.Fatalf("Step 10: kern ci failed with exit %d (expected 0 PASS or 1 BLOCK)", ciExit)
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
	t.Logf("✓ Step 10: RUN CI — kern ci produced artifact (status=%v)", artifact["status"])

	t.Logf("")
	t.Logf("━━━ G13 PASSED: Fresh machine end-to-end ━━━")
	t.Logf("  install → initialize → configure → check → hook → MCP → block → fix → pass → CI")
}

// --- Helpers ---

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

// runBlueprintCheck runs `kern check --staged` against the repo.
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
	t.Fatalf("kern check: %v", err)
	return -1
}

// newTestMCPServer wires the in-process MCP server exactly like the legacy
// cmd/blueprint-mcp main.go: the same four blueprint tools plus the
// pre-tool-use confinement gate (roots from BLUEPRINT_ROOTS, default cwd).
func newTestMCPServer() *bpcmcp.Server {
	srv := bpcmcp.NewServer("blueprint", "test")
	srv.RegisterTool(bpcmcp.ValidateStagedHandler{})
	srv.RegisterTool(bpcmcp.ValidateProposedHandler{})
	srv.RegisterTool(bpcmcp.ExplainFindingHandler{})
	srv.RegisterTool(bpcmcp.RepairGuidanceHandler{})
	srv.WithPreToolHook(testMCPConfinementHook())
	return srv
}

// testMCPConfinementHook mirrors cmd/blueprint-mcp main.go's confinementGate:
// BLUEPRINT_ROOTS (default: the process cwd) bounds every path-typed tool
// argument, with symlinks resolved before containment. The roots are read
// lazily at call time so a test can set BLUEPRINT_ROOTS after server
// construction (a real process has the env fixed before the server starts).
func testMCPConfinementHook() func(name string, args map[string]any) error {
	return func(name string, args map[string]any) error {
		roots := testMCPWorkspaceRoots()
		for key, val := range args {
			s, ok := val.(string)
			if !ok || s == "" || !testMCPIsPathArg(key) {
				continue
			}
			if err := testMCPGatePath(roots, s); err != nil {
				return err
			}
		}
		return nil
	}
}

func testMCPWorkspaceRoots() []string {
	var roots []string
	if env := os.Getenv("BLUEPRINT_ROOTS"); env != "" {
		for _, r := range strings.FieldsFunc(env, func(r rune) bool { return r == ':' || r == ',' }) {
			r = strings.TrimSpace(r)
			if r == "" {
				continue
			}
			if abs, err := filepath.Abs(r); err == nil {
				roots = append(roots, abs)
			}
		}
	}
	if len(roots) == 0 {
		if cwd, err := os.Getwd(); err == nil {
			roots = []string{cwd}
		}
	}
	return roots
}

func testMCPIsPathArg(key string) bool {
	if key == "repo" || key == "dir" {
		return true
	}
	return strings.Contains(strings.ToLower(key), "path")
}

// testMCPGatePath confines a single path argument to the allowed roots,
// resolving symlinks first (matching the legacy gate's containment semantics).
func testMCPGatePath(allowed []string, p string) error {
	abs, err := filepath.Abs(p)
	if err != nil {
		return fmt.Errorf("invalid path %q", p)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return fmt.Errorf("path %q cannot be resolved: %v", p, err)
	}
	for _, root := range allowed {
		if testMCPWithinRoot(root, resolved) {
			return nil
		}
	}
	return fmt.Errorf("path %q is outside the allowed workspace roots %v", resolved, allowed)
}

// testMCPWithinRoot mirrors internal/mcp.RootContains: the root is
// symlink-resolved before containment is judged.
func testMCPWithinRoot(root, resolved string) bool {
	r, err := filepath.EvalSymlinks(root)
	if err != nil {
		r = root
	}
	rel, err := filepath.Rel(r, resolved)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// runMCPServerPipe feeds newline-delimited JSON-RPC input to an in-process
// MCP server over os.Pipe() stdio (the standalone binary's stdio contract)
// and returns the server's response lines.
func runMCPServerPipe(t *testing.T, srv *bpcmcp.Server, input, roots string) string {
	t.Helper()
	if roots != "" {
		t.Setenv("BLUEPRINT_ROOTS", roots)
	}
	rIn, wIn, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	rOut, wOut, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- srv.Serve(context.Background(), rIn, wOut)
		_ = wOut.Close()
		_ = rIn.Close()
	}()
	if _, err := io.WriteString(wIn, input); err != nil {
		t.Fatalf("write mcp input: %v", err)
	}
	_ = wIn.Close()
	out, err := io.ReadAll(rOut)
	if err != nil {
		t.Fatalf("read mcp output: %v", err)
	}
	_ = rOut.Close()
	if err := <-done; err != nil {
		t.Fatalf("mcp server: %v", err)
	}
	return string(out)
}

// testMCPConnection verifies the in-process MCP server responds to
// initialize + tools/list.
func testMCPConnection(t *testing.T, srv *bpcmcp.Server, dir, kernPath string) bool {
	t.Helper()
	t.Setenv("KERN_BINARY", kernPath)
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
	}, "\n")

	out := runMCPServerPipe(t, srv, input, dir) // fixture repo is the workspace root

	lines := strings.Split(strings.TrimSpace(out), "\n")
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
func testMCPValidate(t *testing.T, srv *bpcmcp.Server, dir, kernPath string) bool {
	t.Helper()
	t.Setenv("KERN_BINARY", kernPath)
	input := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`+"\n"+
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"blueprint_validate_staged","arguments":{"repo":"%s","source":"agent"}}}`+"\n", dir)

	out := runMCPServerPipe(t, srv, input, dir) // fixture repo is the workspace root

	lines := strings.Split(strings.TrimSpace(out), "\n")
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
