package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runCheckDegradedJSON runs `blueprint check --staged --repo dir
// --format=json <extraArgs>` with KERN_BINARY=/nonexistent so the run is
// deterministic (degraded mode, exit 0 for a clean change).
func runCheckDegradedJSON(t *testing.T, binPath, dir string, extraArgs ...string) (string, int) {
	t.Helper()
	args := append([]string{"--format=json"}, extraArgs...)
	return runCheckWithEnv(t, binPath, dir, args, []string{"KERN_BINARY=/nonexistent/kern/binary"})
}

// lastAuditAgentID reads the final audit JSONL record and returns its
// agent_id ("" and false when the field is omitted).
func lastAuditAgentID(t *testing.T, dir string) (string, bool) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, ".blueprint", "audit", "audit.jsonl"))
	if err != nil {
		t.Fatalf("read audit jsonl: %v", err)
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		t.Fatal("audit jsonl is empty; expected a validation record")
	}
	lines := strings.Split(trimmed, "\n")
	var rec map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &rec); err != nil {
		t.Fatalf("parse last audit record: %v\n%s", err, lines[len(lines)-1])
	}
	aid, ok := rec["agent_id"].(string)
	return aid, ok
}

// p04CheckRepo builds a tiny git repo with a staged clean change.
func p04CheckRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	g4GitRepo(t, dir)
	g4WriteFile(t, dir, "a.go", "package a\nfunc A() {}\n")
	g4RunGit(t, dir, "add", "-A")
	g4RunGit(t, dir, "commit", "-qm", "init")
	g4WriteFile(t, dir, "a.go", "package a\nfunc A() {}\nfunc B() {}\n")
	g4RunGit(t, dir, "add", "a.go")
	return dir
}

// TestCheck_AgentIDFlag_PopulatesAgentID: `--agent-id myagent` flows into the
// ChangeRequest (observed via the audit record's agent_id).
func TestCheck_AgentIDFlag_PopulatesAgentID(t *testing.T) {
	if testing.Short() {
		t.Skip("E2E gate test — full pipeline; runs in nightly non-short suite")
	}
	bin := g4BuildBinary(t)
	dir := p04CheckRepo(t)

	out, code := runCheckDegradedJSON(t, bin, dir, "--source", "agent", "--agent-id", "myagent")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (degraded WARN); output:\n%s", code, out)
	}
	aid, ok := lastAuditAgentID(t, dir)
	if !ok || aid != "myagent" {
		t.Fatalf("audit agent_id = %q (present=%v), want myagent", aid, ok)
	}
}

// TestCheck_AgentSourceDefaultsToAgent: `--source agent` without --agent-id
// defaults the identity to "agent" so agent-sourced changes always carry one.
func TestCheck_AgentSourceDefaultsToAgent(t *testing.T) {
	if testing.Short() {
		t.Skip("E2E gate test — full pipeline; runs in nightly non-short suite")
	}
	bin := g4BuildBinary(t)
	dir := p04CheckRepo(t)

	out, code := runCheckDegradedJSON(t, bin, dir, "--source", "agent")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (degraded WARN); output:\n%s", code, out)
	}
	aid, ok := lastAuditAgentID(t, dir)
	if !ok || aid != "agent" {
		t.Fatalf("audit agent_id = %q (present=%v), want default \"agent\"", aid, ok)
	}
}

// TestCheck_AgentIDEnvVar: BLUEPRINT_AGENT_ID is the fallback for
// agent-sourced changes without an explicit --agent-id.
func TestCheck_AgentIDEnvVar(t *testing.T) {
	if testing.Short() {
		t.Skip("E2E gate test — full pipeline; runs in nightly non-short suite")
	}
	bin := g4BuildBinary(t)
	dir := p04CheckRepo(t)

	out, code := runCheckWithEnv(t, bin, dir, []string{"--format=json", "--source", "agent"}, []string{"BLUEPRINT_AGENT_ID=envagent", "KERN_BINARY=/nonexistent/kern/binary"})
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (degraded WARN); output:\n%s", code, out)
	}
	aid, ok := lastAuditAgentID(t, dir)
	if !ok || aid != "envagent" {
		t.Fatalf("audit agent_id = %q (present=%v), want envagent", aid, ok)
	}
}

// TestCheck_HumanSourceNoAgentID: non-agent sources carry no identity, so the
// authz gate stays off and the audit record omits agent_id.
func TestCheck_HumanSourceNoAgentID(t *testing.T) {
	if testing.Short() {
		t.Skip("E2E gate test — full pipeline; runs in nightly non-short suite")
	}
	bin := g4BuildBinary(t)
	dir := p04CheckRepo(t)

	out, code := runCheckDegradedJSON(t, bin, dir, "--source", "human")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (degraded WARN); output:\n%s", code, out)
	}
	aid, ok := lastAuditAgentID(t, dir)
	if ok && aid != "" {
		t.Fatalf("audit agent_id = %q, want omitted for human source", aid)
	}
}
