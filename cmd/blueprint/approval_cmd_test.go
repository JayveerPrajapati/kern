package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// gApprovalCheckJSON runs `blueprint check --staged --format=json` and returns
// the raw stdout plus the exit code. Stderr (degraded-mode warnings, audit
// notices) is captured separately so JSON parsing is not confused by prefixed
// warning lines.
func gApprovalCheckJSON(t *testing.T, binPath, dir string, extraArgs ...string) (string, int) {
	t.Helper()
	args := append([]string{"check", "--staged", "--format=json", "--repo", dir}, extraArgs...)
	cmd := exec.Command(binPath, args...)
	cmd.Env = append(os.Environ(), "KERN_BINARY="+os.Getenv("KERN_BINARY"))
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		} else {
			t.Fatalf("run blueprint check: %v\nstderr: %s", err, stderr.String())
		}
	}
	return stdout.String(), code
}

// approvalResult is the subset of ValidationResult JSON the test asserts on.
type approvalResult struct {
	Status   string `json:"status"`
	ExitCode int    `json:"exit_code"`
	Checks   []struct {
		Name   string `json:"name"`
		Status string `json:"status"`
	} `json:"checks"`
	Findings []struct {
		RuleID   string `json:"rule_id"`
		Severity string `json:"severity"`
	} `json:"findings"`
}

func parseApprovalResult(t *testing.T, out string) approvalResult {
	t.Helper()
	var res approvalResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("parse check JSON: %v\noutput:\n%s", err, out)
	}
	return res
}

func gateStatus(t *testing.T, res approvalResult) string {
	t.Helper()
	for _, c := range res.Checks {
		if c.Name == "approval:gate" {
			return c.Status
		}
	}
	t.Fatalf("approval:gate check missing from result: %+v", res.Checks)
	return ""
}

// TestP13_ApprovalGateE2E drives the full two-person flow through the built
// binary: stage a sensitive change as an agent -> BLOCK; request-approval ->
// approve -> check with --approval-id passes. It also covers reject and the
// error paths (unknown id, already decided).
func TestP13_ApprovalGateE2E(t *testing.T) {
	_ = requireKernPath(t)
	bin := g4BuildBinary(t)
	dir := t.TempDir()
	g4GitRepo(t, dir)
	g4WriteFile(t, dir, "main.go", "package main\nfunc main() {}\n")
	g4RunGit(t, dir, "add", "-A")
	g4RunGit(t, dir, "commit", "-qm", "init")

	// Stage a change to a sensitive path (.kern/**) with innocuous content so
	// the ONLY blocker is the approval gate.
	g4WriteFile(t, dir, ".kern/gate-test.txt", "hello\n")
	g4RunGit(t, dir, "add", ".kern/gate-test.txt")

	// 1. Agent change on a sensitive path: BLOCK, approval:required.
	out, code := gApprovalCheckJSON(t, bin, dir, "--source", "agent")
	if code != 1 {
		t.Fatalf("exit=%d want 1 (BLOCK) without approval; output:\n%s", code, out)
	}
	res := parseApprovalResult(t, out)
	if gateStatus(t, res) != "BLOCK" {
		t.Fatalf("gate status = %q, want BLOCK", gateStatus(t, res))
	}
	foundRequired := false
	for _, f := range res.Findings {
		if f.RuleID == "approval:required" {
			foundRequired = true
		}
	}
	if !foundRequired {
		t.Fatalf("missing approval:required finding:\n%s", out)
	}

	// 2. request-approval: creates a pending request and prints its id.
	reqOut, reqCode := runCLI(t, bin, dir, "request-approval", "--intent", "update gate test", "--source", "agent")
	if reqCode != 0 {
		t.Fatalf("request-approval exit=%d want 0; output:\n%s", reqCode, reqOut)
	}
	id := extractRequestID(t, reqOut)
	if !strings.Contains(reqOut, "risk=high") {
		t.Fatalf("request output missing risk assessment:\n%s", reqOut)
	}

	// 3. approve: records the human decision. Run from a DIFFERENT working
	// directory with --repo to prove flags after the positional id parse
	// (stdlib flag stops at the first positional — regression guard).
	other := t.TempDir()
	appOut, appCode := runCLIFrom(t, bin, other, "approve", id, "--repo", dir, "--reason", "looks fine")
	if appCode != 0 {
		t.Fatalf("approve exit=%d want 0; output:\n%s", appCode, appOut)
	}
	if !strings.Contains(appOut, "approved") || !strings.Contains(appOut, id) {
		t.Fatalf("approve output missing confirmation:\n%s", appOut)
	}

	// 4. check --approval-id: the gate passes.
	out2, code2 := gApprovalCheckJSON(t, bin, dir, "--source", "agent", "--approval-id", id)
	if code2 != 0 {
		t.Fatalf("exit=%d want 0 with valid approval; output:\n%s", code2, out2)
	}
	res2 := parseApprovalResult(t, out2)
	if gateStatus(t, res2) != "PASS" {
		t.Fatalf("gate status = %q, want PASS with approved id:\n%s", gateStatus(t, res2), out2)
	}

	// 5. A pending request still blocks.
	reqOut2, _ := runCLI(t, bin, dir, "request-approval", "--intent", "pending one", "--source", "agent")
	pendingID := extractRequestID(t, reqOut2)
	out3, code3 := gApprovalCheckJSON(t, bin, dir, "--source", "agent", "--approval-id", pendingID)
	if code3 != 1 {
		t.Fatalf("exit=%d want 1 (BLOCK) for pending approval; output:\n%s", code3, out3)
	}
	if gateStatus(t, parseApprovalResult(t, out3)) != "BLOCK" {
		t.Fatalf("gate must BLOCK on a pending request:\n%s", out3)
	}

	// 6. reject blocks with approval:rejected (also from a different cwd via
	// --repo, exercising the interspersed-flag path again).
	reqOut3, _ := runCLI(t, bin, dir, "request-approval", "--intent", "reject me", "--source", "agent")
	rejID := extractRequestID(t, reqOut3)
	if rejOut, rejCode := runCLIFrom(t, bin, other, "reject", rejID, "--repo", dir, "--reason", "not now"); rejCode != 0 {
		t.Fatalf("reject exit=%d want 0; output:\n%s", rejCode, rejOut)
	}
	out4, code4 := gApprovalCheckJSON(t, bin, dir, "--source", "agent", "--approval-id", rejID)
	if code4 != 1 {
		t.Fatalf("exit=%d want 1 (BLOCK) for rejected approval; output:\n%s", code4, out4)
	}
	res4 := parseApprovalResult(t, out4)
	foundRejected := false
	for _, f := range res4.Findings {
		if f.RuleID == "approval:rejected" {
			foundRejected = true
		}
	}
	if !foundRejected {
		t.Fatalf("missing approval:rejected finding:\n%s", out4)
	}

	// 7. Error paths: unknown id and already-decided requests are operational
	// errors, not policy violations — exit 3 (also from the foreign cwd so
	// --repo is exercised).
	if out, code := runCLIFrom(t, bin, other, "approve", "apr-does-not-exist", "--repo", dir); code != 3 {
		t.Fatalf("approve unknown id exit=%d want 3 (operational error); output:\n%s", code, out)
	}
	if out, code := runCLIFrom(t, bin, other, "approve", id, "--repo", dir); code != 3 {
		t.Fatalf("second approve exit=%d want 3 (already decided); output:\n%s", code, out)
	}

	// 8. Approval decisions land in the audit trail with kind approval-decision.
	auditData, err := os.ReadFile(dir + "/.blueprint/audit/audit.jsonl")
	if err != nil {
		t.Fatalf("read audit trail: %v", err)
	}
	if !strings.Contains(string(auditData), `"kind":"approval-decision"`) {
		t.Fatalf("audit trail missing approval-decision record:\n%s", auditData)
	}
}

// TestP13_HumanSourceNotGated verifies the default policy does not gate human
// changes (humans are the approvers).
func TestP13_HumanSourceNotGated(t *testing.T) {
	_ = requireKernPath(t)
	bin := g4BuildBinary(t)
	dir := t.TempDir()
	g4GitRepo(t, dir)
	g4WriteFile(t, dir, "main.go", "package main\nfunc main() {}\n")
	g4RunGit(t, dir, "add", "-A")
	g4RunGit(t, dir, "commit", "-qm", "init")

	g4WriteFile(t, dir, ".kern/gate-test.txt", "hello\n")
	g4RunGit(t, dir, "add", ".kern/gate-test.txt")

	out, code := gApprovalCheckJSON(t, bin, dir, "--source", "human")
	if code == 1 {
		t.Fatalf("human change must not BLOCK; output:\n%s", out)
	}
	if gs := gateStatus(t, parseApprovalResult(t, out)); gs != "PASS" {
		t.Fatalf("gate status = %q, want PASS for human source", gs)
	}
}

// runCLI runs the blueprint binary with a subcommand and returns (output, code).
func runCLI(t *testing.T, binPath, dir string, args ...string) (string, int) {
	t.Helper()
	return runCLIFrom(t, binPath, dir, args...)
}

// runCLIFrom runs the blueprint binary from the given working directory. The
// repo under test is passed explicitly via --repo by callers that want to
// exercise the flag path (as opposed to relying on the cwd).
func runCLIFrom(t *testing.T, binPath, cwd string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(binPath, args...)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), "KERN_BINARY="+os.Getenv("KERN_BINARY"))
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		} else {
			t.Fatalf("run blueprint %v: %v\n%s", args, err, out)
		}
	}
	return string(out), code
}

// extractRequestID pulls the request id from `request-approval` output
// ("Request apr-<hex> created ...").
func extractRequestID(t *testing.T, out string) string {
	t.Helper()
	re := regexp.MustCompile(`Request (apr-[0-9a-f]+) created`)
	m := re.FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no request id in output:\n%s", out)
	}
	return m[1]
}
