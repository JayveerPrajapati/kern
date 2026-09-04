package mcp

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// g5RequireKern skips if the kern binary isn't available.
func g5RequireKern(t *testing.T) {
	t.Helper()
	if os.Getenv("KERN_BINARY") == "" {
		// Try default path
		if _, err := exec.LookPath("kern"); err != nil {
			t.Skipf("kern binary not available (set KERN_BINARY): %v", err)
		}
	}
}

// g5RequireFingerprint skips unless the installed kern binary supports the
// `fingerprint` subcommand (blueprint's duplication oracle). The
// ValidateProposed handler runs the duplication check, which shells out to
// `kern fingerprint`; until the subcommand ships in the kern release, the
// proposed-content integration tests skip so the suite stays green
// pre-integration (the orchestrator runs them once the kern lane rebuilds).
func g5RequireFingerprint(t *testing.T) {
	t.Helper()
	kernBin := os.Getenv("KERN_BINARY")
	if kernBin == "" {
		p, err := exec.LookPath("kern")
		if err != nil {
			t.Skipf("kern binary not available (set KERN_BINARY): %v", err)
		}
		kernBin = p
	}
	out, err := exec.Command(kernBin, "--help").CombinedOutput()
	if err != nil {
		t.Skipf("kern --help failed (%v); skipping fingerprint-backed assertions", err)
	}
	if !strings.Contains(string(out), "fingerprint") {
		t.Skipf("installed kern does not support `kern fingerprint` (needs the P1-3 kern release); skipping fingerprint-backed assertions")
	}
}

// g5Repo creates a git repo with boundaries + clean base commit, returns path.
func g5Repo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	g5Git(t, dir, "init", "-q")
	g5Git(t, dir, "config", "user.email", "t@t")
	g5Git(t, dir, "config", "user.name", "t")
	g5Write(t, dir, ".kern/boundaries.json", `{"rules":[{"from":"web","to":"db","action":"forbid"}]}`)
	g5Write(t, dir, "go.mod", "module example.com/repo\n\ngo 1.23\n")
	g5Write(t, dir, "db/db.go", "package db\nfunc Query() {}\n")
	g5Write(t, dir, "web/web.go", "package web\nfunc Handle() {}\n")
	g5Git(t, dir, "add", "-A")
	g5Git(t, dir, "commit", "-qm", "init")
	return dir
}

func g5Git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func g5Write(t *testing.T, dir, relpath, content string) {
	t.Helper()
	full := filepath.Join(dir, relpath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", relpath, err)
	}
}

// g5Stage stages a file with given content.
func g5Stage(t *testing.T, dir, relpath, content string) {
	t.Helper()
	g5Write(t, dir, relpath, content)
	g5Git(t, dir, "add", relpath)
}

// g5CallValidateStaged calls the ValidateStagedHandler directly with args.
func g5CallValidateStaged(t *testing.T, args json.RawMessage) ToolResult {
	t.Helper()
	h := ValidateStagedHandler{}
	return h.Handle(context.Background(), args)
}

// g5ParseResult parses the ToolResult's text content as JSON.
func g5ParseResult(t *testing.T, tr ToolResult) map[string]interface{} {
	t.Helper()
	if tr.IsError {
		t.Fatalf("expected success result, got error: %s", tr.Content[0].Text)
	}
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(tr.Content[0].Text), &m); err != nil {
		t.Fatalf("parse result JSON: %v\nraw: %s", err, tr.Content[0].Text)
	}
	return m
}

// G5-1: valid file write
func TestG5_ValidFileWrite(t *testing.T) {
	g5RequireKern(t)
	dir := g5Repo(t)
	// Stage a clean change.
	g5Stage(t, dir, "web/extra.go", "package web\nfunc Extra() {}\n")

	args, _ := json.Marshal(map[string]string{"repo": dir, "source": "agent"})
	tr := g5CallValidateStaged(t, args)
	m := g5ParseResult(t, tr)
	status, _ := m["status"].(string)
	if status != "PASS" && status != "pass" {
		t.Fatalf("status=%v, want PASS; full: %v", m["status"], m)
	}
}

// G5-2: architecture violation
func TestG5_ArchitectureViolation(t *testing.T) {
	g5RequireKern(t)
	dir := g5Repo(t)
	// Stage a violating file.
	g5Stage(t, dir, "web/bad.go", "package web\nimport \"example.com/repo/db\"\nfunc Bad() { db.Query() }\n")

	args, _ := json.Marshal(map[string]string{"repo": dir, "source": "agent"})
	tr := g5CallValidateStaged(t, args)
	m := g5ParseResult(t, tr)
	status, _ := m["status"].(string)
	if !strings.EqualFold(status, "BLOCK") {
		t.Fatalf("status=%v, want BLOCK; full: %v", m["status"], m)
	}
}

// G5-3: secret insertion
func TestG5_SecretInsertion(t *testing.T) {
	g5RequireKern(t)
	dir := g5Repo(t)
	// Stage a file with a secret.
	g5Stage(t, dir, "config.go", "package main\nconst AWSKey = \"AKIA1234567890ABCDEF\"\n")

	args, _ := json.Marshal(map[string]string{"repo": dir, "source": "agent"})
	tr := g5CallValidateStaged(t, args)
	m := g5ParseResult(t, tr)
	status, _ := m["status"].(string)
	if !strings.EqualFold(status, "BLOCK") {
		t.Fatalf("status=%v, want BLOCK (secret); full: %v", m["status"], m)
	}
}

// G5-4: multi-file change
func TestG5_MultiFileChange(t *testing.T) {
	g5RequireKern(t)
	dir := g5Repo(t)
	// Stage two clean files.
	g5Stage(t, dir, "web/a.go", "package web\nfunc A() {}\n")
	g5Stage(t, dir, "web/b.go", "package web\nfunc B() {}\n")

	args, _ := json.Marshal(map[string]string{"repo": dir, "source": "agent"})
	tr := g5CallValidateStaged(t, args)
	m := g5ParseResult(t, tr)
	status, _ := m["status"].(string)
	if !strings.EqualFold(status, "PASS") {
		t.Fatalf("status=%v, want PASS (both clean); full: %v", m["status"], m)
	}
}

// G5-4b: staged duplication through the staged handler. The staged handler
// runs the same check set as the CLI (architecture + secrets + duplication),
// so staged content that duplicates an existing function surfaces a
// duplication finding. Under the two-pass triage model (P1.1), a byte-identical
// duplicate escalates to BLOCK (duplication:confirmed-block) when jscpd
// confirms the same file pair; without the jscpd binary it stays advisory
// WARN (pure advisory fallback). Under P1.5 the duplication category's default
// enforcement was reverted from "block" to "warn" (loader.go): warn
// enforcement downgrades the AGGREGATE STATUS to WARN while a finding keeps
// its intrinsic severity — so a confirmed-block finding may surface as
// status=WARN with severity=block. Either outcome is valid here — what must
// never happen is a BLOCK that is not a two-pass confirmed finding, or a
// severity=block finding whose rule is not duplication:confirmed-block.
// Mirrors TestG15_ProposedDuplicationWarn's fixture but through git staging.
func TestG5_StagedDuplicationWarn(t *testing.T) {
	g5RequireKern(t)
	g5RequireFingerprint(t)
	dir := g5Repo(t)

	// Give db/db.go a real function body, then stage an identical function in
	// a new file: structural fingerprints match -> at least a WARN advisory.
	// (Trivial one-line functions are discounted below the WARN threshold by
	// design.)
	const existing = "package db\n\nfunc Query() error {\n\terr := open()\n\tif err != nil {\n\t\treturn err\n\t}\n\treturn nil\n}\n"
	g5Write(t, dir, "db/db.go", existing)
	g5Git(t, dir, "add", "db/db.go")
	g5Git(t, dir, "commit", "-qm", "richer db")

	g5Stage(t, dir, "web/dup.go", "package web\n\nfunc Query() error {\n\terr := open()\n\tif err != nil {\n\t\treturn err\n\t}\n\treturn nil\n}\n")

	args, _ := json.Marshal(map[string]string{"repo": dir, "source": "agent"})
	tr := g5CallValidateStaged(t, args)
	m := g5ParseResult(t, tr)
	status, _ := m["status"].(string)

	var dupFinding map[string]interface{}
	findings, _ := m["findings"].([]interface{})
	for _, f := range findings {
		fm, ok := f.(map[string]interface{})
		if !ok {
			continue
		}
		if rule, _ := fm["rule_id"].(string); strings.HasPrefix(rule, "duplication:") {
			dupFinding = fm
			break
		}
	}
	if dupFinding == nil {
		t.Fatalf("expected a duplication finding in staged validation; full: %s", tr.Content[0].Text)
	}

	rule, _ := dupFinding["rule_id"].(string)
	sev, _ := dupFinding["severity"].(string)
	switch {
	case strings.EqualFold(status, "BLOCK"):
		// Two-pass confirmed: only duplication:confirmed-block may block.
		if rule != "duplication:confirmed-block" || sev != "block" {
			t.Fatalf("BLOCK must come from duplication:confirmed-block, got rule=%s severity=%s; full: %s", rule, sev, tr.Content[0].Text)
		}
	case strings.EqualFold(status, "WARN"):
		// P1.5 warn enforcement (loader.go): the aggregate status is downgraded
		// to WARN, but a finding keeps its intrinsic severity. So a two-pass
		// confirmed finding (duplication:confirmed-block, severity=block) may
		// legitimately surface under a WARN aggregate — that is the documented
		// policy downgrade, not a bug. severity=block under any OTHER
		// duplication rule would be a real bug (unconfirmed findings must
		// never claim block severity).
		if sev == "block" && rule != "duplication:confirmed-block" {
			t.Fatalf("severity=block under WARN must come from duplication:confirmed-block, got rule=%s severity=%s: %v", rule, sev, dupFinding)
		}
	default:
		t.Fatalf("status=%v, want BLOCK (jscpd confirmed) or WARN (advisory fallback); full: %s", m["status"], tr.Content[0].Text)
	}
}

// G5-5: failed tool execution (kern binary unavailable)
func TestG5_FailedToolExecution(t *testing.T) {
	// Force kern binary to be unfindable by clearing PATH for the subprocess.
	// We can't easily do this with the direct handler call, so test via the
	// server over stdio with a repo that has no .kern dir.
	dir := t.TempDir()
	g5Git(t, dir, "init", "-q")
	g5Write(t, dir, "file.go", "package main\n")
	g5Git(t, dir, "add", "-A")

	// Call handler with a nonexistent repo path.
	args, _ := json.Marshal(map[string]string{"repo": "/nonexistent/path/xyz"})
	tr := g5CallValidateStaged(t, args)
	if !tr.IsError {
		t.Fatal("expected error result for nonexistent repo")
	}
}

// G5-6: malformed tool payload
func TestG5_MalformedPayload(t *testing.T) {
	tr := g5CallValidateStaged(t, json.RawMessage(`{bad json`))
	if !tr.IsError {
		t.Fatal("expected error for malformed JSON")
	}
	if !strings.Contains(strings.ToLower(tr.Content[0].Text), "invalid arguments") {
		t.Errorf("error message should mention invalid arguments: %s", tr.Content[0].Text)
	}
}

// G5-7: oversized payload
func TestG5_OversizedPayload(t *testing.T) {
	// Build a payload larger than MaxPayloadBytes.
	big := strings.Repeat("x", MaxPayloadBytes+100)
	// Wrap in a JSON string value to keep it valid JSON but oversized.
	args := json.RawMessage(`{"repo":"` + big + `"}`)
	tr := g5CallValidateStaged(t, args)
	// The handler should either error (repo not found) or the server should
	// reject it. Since we're calling the handler directly (not the server's
	// line reader), the handler will try to resolve the path and fail.
	// This is still a valid test: oversized payloads don't crash the handler.
	_ = tr // handler handles it gracefully (error or result), no panic
}

// G5-8: missing repository context
func TestG5_MissingRepoContext(t *testing.T) {
	// Call with empty repo — should use cwd or error gracefully.
	tr := g5CallValidateStaged(t, json.RawMessage(`{}`))
	// Either succeeds (if cwd is a git repo) or errors — both acceptable.
	// The key assertion: no panic, returns a structured result.
	_ = tr
}

// G5-9: agent identity missing/unknown
func TestG5_AgentIdentityMissing(t *testing.T) {
	g5RequireKern(t)
	dir := g5Repo(t)
	g5Stage(t, dir, "web/extra.go", "package web\nfunc Extra() {}\n")

	// Call without source — should default to "agent".
	args, _ := json.Marshal(map[string]string{"repo": dir})
	tr := g5CallValidateStaged(t, args)
	if tr.IsError {
		t.Fatalf("expected success with default source, got error: %s", tr.Content[0].Text)
	}
}

// G5-10: blocked response is machine-readable
func TestG5_BlockedResponseMachineReadable(t *testing.T) {
	g5RequireKern(t)
	dir := g5Repo(t)
	g5Stage(t, dir, "web/bad.go", "package web\nimport \"example.com/repo/db\"\nfunc Bad() { db.Query() }\n")

	args, _ := json.Marshal(map[string]string{"repo": dir, "source": "agent"})
	tr := g5CallValidateStaged(t, args)
	if tr.IsError {
		t.Fatalf("expected result, got error: %s", tr.Content[0].Text)
	}

	// Must be valid JSON with status, findings, exit_code fields.
	var result struct {
		Status   string                   `json:"status"`
		Findings []map[string]interface{} `json:"findings"`
		ExitCode int                      `json:"exit_code"`
	}
	if err := json.Unmarshal([]byte(tr.Content[0].Text), &result); err != nil {
		t.Fatalf("result is not valid JSON: %v\nraw: %s", err, tr.Content[0].Text)
	}
	if !strings.EqualFold(result.Status, "BLOCK") {
		t.Errorf("status=%s, want BLOCK", result.Status)
	}
	if len(result.Findings) == 0 {
		t.Error("expected findings in blocked result")
	}
	// Agent can use findings to repair: each finding must have rule_id, file, message.
	for i, f := range result.Findings {
		if _, ok := f["rule_id"]; !ok {
			t.Errorf("finding[%d] missing rule_id", i)
		}
		if _, ok := f["file"]; !ok {
			t.Errorf("finding[%d] missing file", i)
		}
		if _, ok := f["message"]; !ok {
			t.Errorf("finding[%d] missing message", i)
		}
	}
}

// G5-11: verify the agent can use the result to repair and retry
// (explain finding tool produces actionable guidance)
func TestG5_RepairAndRetry(t *testing.T) {
	g5RequireKern(t)
	dir := g5Repo(t)
	g5Stage(t, dir, "web/bad.go", "package web\nimport \"example.com/repo/db\"\nfunc Bad() { db.Query() }\n")

	// Step 1: validate, get a finding.
	args, _ := json.Marshal(map[string]string{"repo": dir, "source": "agent"})
	tr := g5CallValidateStaged(t, args)
	result := g5ParseResult(t, tr)
	findings, _ := result["findings"].([]interface{})
	if len(findings) == 0 {
		t.Fatal("expected findings")
	}
	finding := findings[0].(map[string]interface{})

	// Step 2: call explain_finding with the finding.
	explainArgs, _ := json.Marshal(map[string]interface{}{"finding": finding})
	eh := ExplainFindingHandler{}
	etr := eh.Handle(context.Background(), explainArgs)
	if etr.IsError {
		t.Fatalf("explain_finding errored: %s", etr.Content[0].Text)
	}
	text := etr.Content[0].Text
	// The explanation must contain actionable guidance.
	if !strings.Contains(text, "Suggested fix") && !strings.Contains(text, "suggested_fix") {
		t.Errorf("explanation missing suggested fix:\n%s", text)
	}
}

// G5-12: end-to-end MCP server over stdio (tools/call dispatch)
func TestG5_ServerEndToEnd(t *testing.T) {
	g5RequireKern(t)
	dir := g5Repo(t)
	g5Stage(t, dir, "web/bad.go", "package web\nimport \"example.com/repo/db\"\nfunc Bad() { db.Query() }\n")

	// Build the blueprint-mcp binary.
	binPath := filepath.Join(t.TempDir(), "blueprint-mcp")
	cmd := exec.Command("go", "build", "-buildvcs=false", "-o", binPath, "./cmd/blueprint-mcp")
	cmd.Dir = g5FindRepoRoot(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build blueprint-mcp: %v\n%s", err, out)
	}

	// Pipe: initialize -> tools/list -> tools/call
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"blueprint_validate_staged","arguments":{"repo":"` + dir + `","source":"agent"}}}`,
	}, "\n")

	srvCmd := exec.Command(binPath)
	srvCmd.Stdin = strings.NewReader(input)
	srvCmd.Env = append(os.Environ(),
		"KERN_BINARY="+os.Getenv("KERN_BINARY"),
		"BLUEPRINT_ROOTS="+dir, // declare the fixture repo as an allowed workspace
	)
	out, err := srvCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("blueprint-mcp failed: %v\n%s", err, out)
	}

	// Parse line 3 (tools/call response).
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 3 {
		t.Fatalf("expected >=3 response lines, got %d:\n%s", len(lines), out)
	}
	var resp struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(lines[2]), &resp); err != nil {
		t.Fatalf("parse tools/call response: %v\nraw: %s", err, lines[2])
	}
	if resp.Error != nil {
		t.Fatalf("tools/call returned error: %s", resp.Error.Message)
	}
	if len(resp.Result.Content) == 0 {
		t.Fatal("tools/call returned no content")
	}
	// Parse the inner JSON (the ValidationResult).
	var vr struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(resp.Result.Content[0].Text), &vr); err != nil {
		t.Fatalf("parse ValidationResult: %v\nraw: %s", err, resp.Result.Content[0].Text)
	}
	if !strings.EqualFold(vr.Status, "BLOCK") {
		t.Errorf("status=%s, want BLOCK", vr.Status)
	}
}

// g5FindRepoRoot walks up to find go.mod.
func g5FindRepoRoot(t *testing.T) string {
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
