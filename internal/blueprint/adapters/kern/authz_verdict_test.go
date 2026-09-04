package kern

import (
	"context"
	"strings"
	"testing"
)

// --- P0.4 authz verdict contract tests ---
//
// These tests use fakeRunner (architecture_test.go) so they never depend on a
// real kern binary. The contract: `kern guard check --file <files>
// --agent-id <id> --task <task> --json` returns the v2 payload with an
// optional "authz_verdict" object; exit 2 is a RESULT (denied), not an error.

func TestAuthzVerdict_Allowed(t *testing.T) {
	const out = `{"schema_version":2,"violations":[],"authz_verdict":{
		"schema_version":1,"agent_id":"default","task":"test","decision":"allowed",
		"policy_source":"task-scope","denied_files":[],"fingerprint":"sha256:abc","decided_at":"2026-08-31T10:00:00Z"}}`
	client := &KernClient{binaryPath: "kern", runner: fakeRunner(out, "", 0, nil)}

	verdict, err := client.AuthzVerdict(context.Background(), t.TempDir(), "default", "test", []string{"a.go", "b.go"})
	if err != nil {
		t.Fatalf("AuthzVerdict returned error: %v", err)
	}
	if verdict == nil {
		t.Fatal("AuthzVerdict = nil, want parsed verdict")
	}
	if verdict.Decision != "allowed" {
		t.Errorf("Decision = %q, want allowed", verdict.Decision)
	}
	if verdict.AgentID != "default" {
		t.Errorf("AgentID = %q, want default", verdict.AgentID)
	}
	if verdict.Task != "test" {
		t.Errorf("Task = %q, want test", verdict.Task)
	}
	if verdict.PolicySource != "task-scope" {
		t.Errorf("PolicySource = %q, want task-scope", verdict.PolicySource)
	}
	if len(verdict.DeniedFiles) != 0 {
		t.Errorf("DeniedFiles = %v, want empty", verdict.DeniedFiles)
	}
}

func TestAuthzVerdict_Denied(t *testing.T) {
	// kern exits 2 on a denied verdict: that is a RESULT, not an error.
	const out = `{"schema_version":2,"violations":[],"authz_verdict":{
		"schema_version":1,"agent_id":"default","task":"test","decision":"denied",
		"policy_source":"default-scoped","denied_files":["web/web.go","db/db.go"],
		"fingerprint":"sha256:def","decided_at":"2026-08-31T10:00:00Z"}}`
	client := &KernClient{binaryPath: "kern", runner: fakeRunner(out, "", 2, nil)}

	verdict, err := client.AuthzVerdict(context.Background(), t.TempDir(), "default", "test", []string{"web/web.go"})
	if err != nil {
		t.Fatalf("AuthzVerdict returned error for exit 2 (denied is a result): %v", err)
	}
	if verdict == nil {
		t.Fatal("AuthzVerdict = nil, want parsed denied verdict")
	}
	if verdict.Decision != "denied" {
		t.Errorf("Decision = %q, want denied", verdict.Decision)
	}
	if len(verdict.DeniedFiles) != 2 || verdict.DeniedFiles[0] != "web/web.go" {
		t.Errorf("DeniedFiles = %v, want [web/web.go db/db.go]", verdict.DeniedFiles)
	}
}

func TestAuthzVerdict_Omitted(t *testing.T) {
	// v2 payload WITHOUT the authz_verdict key (kern too old / no agent-id
	// path): nil, nil — the caller proceeds without a verdict.
	const out = `{"schema_version":2,"violations":[]}`
	client := &KernClient{binaryPath: "kern", runner: fakeRunner(out, "", 0, nil)}

	verdict, err := client.AuthzVerdict(context.Background(), t.TempDir(), "default", "test", []string{"a.go"})
	if err != nil {
		t.Fatalf("AuthzVerdict returned error when verdict key absent: %v", err)
	}
	if verdict != nil {
		t.Errorf("AuthzVerdict = %+v, want nil (no verdict available)", verdict)
	}
}

func TestAuthzVerdict_VersionMismatch(t *testing.T) {
	// schema_version 1 (old contract): fail closed, never silently misparse.
	const out = `{"schema_version":1,"violations":[],"authz_verdict":{
		"schema_version":1,"agent_id":"default","task":"test","decision":"allowed",
		"policy_source":"task-scope","denied_files":[],"fingerprint":"","decided_at":""}}`
	client := &KernClient{binaryPath: "kern", runner: fakeRunner(out, "", 0, nil)}

	if _, err := client.AuthzVerdict(context.Background(), t.TempDir(), "default", "test", []string{"a.go"}); err == nil {
		t.Fatal("AuthzVerdict returned no error for schema_version 1, want contract mismatch")
	} else if !strings.Contains(err.Error(), "contract") {
		t.Errorf("error = %q, want mention of contract", err.Error())
	}
}

func TestAuthzVerdict_EmptyTask_NoVerdict(t *testing.T) {
	// kern's guard requires --agent-id AND --task together (P0.4 usage rule).
	// Without a task scope there is nothing to authorize: nil, nil, and the
	// runner must NOT be invoked.
	called := false
	client := &KernClient{binaryPath: "kern", runner: func(ctx context.Context, name string, args []string, workdir string) (string, string, int, error) {
		called = true
		return "", "", 0, nil
	}}

	verdict, err := client.AuthzVerdict(context.Background(), t.TempDir(), "default", "", []string{"a.go"})
	if err != nil {
		t.Fatalf("AuthzVerdict returned error for empty task: %v", err)
	}
	if verdict != nil {
		t.Errorf("AuthzVerdict = %+v, want nil for empty task scope", verdict)
	}
	if called {
		t.Error("runner invoked for empty task scope; authz must not probe kern without a task")
	}
}

func TestAuthzVerdict_EmptyAgentID_NoVerdict(t *testing.T) {
	// No agent identity: kern cannot (and should not) evaluate authz.
	called := false
	client := &KernClient{binaryPath: "kern", runner: func(ctx context.Context, name string, args []string, workdir string) (string, string, int, error) {
		called = true
		return "", "", 0, nil
	}}

	verdict, err := client.AuthzVerdict(context.Background(), t.TempDir(), "", "test", []string{"a.go"})
	if err != nil {
		t.Fatalf("AuthzVerdict returned error for empty agent id: %v", err)
	}
	if verdict != nil {
		t.Errorf("AuthzVerdict = %+v, want nil for empty agent id", verdict)
	}
	if called {
		t.Error("runner invoked for empty agent id")
	}
}

func TestAuthzVerdict_Exit3_IsError(t *testing.T) {
	// Any exit code other than 0/2 is a tool failure, never a verdict.
	client := &KernClient{binaryPath: "kern", runner: fakeRunner("", "boom", 3, nil)}
	if _, err := client.AuthzVerdict(context.Background(), t.TempDir(), "default", "test", []string{"a.go"}); err == nil {
		t.Fatal("AuthzVerdict returned no error for exit 3, want tool failure error")
	}
}

func TestAuthzVerdict_LaunchFailure_IsError(t *testing.T) {
	client := &KernClient{binaryPath: "kern", runner: fakeRunner("", "", 0, context.Canceled)}
	if _, err := client.AuthzVerdict(context.Background(), t.TempDir(), "default", "test", []string{"a.go"}); err == nil {
		t.Fatal("AuthzVerdict returned no error for launch failure")
	}
}
