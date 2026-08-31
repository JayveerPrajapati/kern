package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// guardCheckFixture writes a tiny Go module so `kern guard check` has an
// index to authorize against (intel.ReadIndex builds it on demand). It
// reuses the shared CLI fixture from json_flags_test.go.
func guardCheckFixture(t *testing.T) string {
	t.Helper()
	return jsonCliFixture(t)
}

// guardCheckJSON runs `kern guard check --json` in-process against root with
// the given extra flags and returns the parsed JSON output. Only safe for
// paths that do not os.Exit (allowed verdicts, omitted verdicts, schema
// checks); exit-code paths use runGuardHelper.
func guardCheckJSON(t *testing.T, root string, extra ...string) map[string]any {
	t.Helper()
	args := append([]string{"check", root, "--json"}, extra...)
	out := captureStdout(t, func() { runGuard(args) })
	return assertValidJSON(t, out)
}

// TestGuardCheckHelperProcess is the subprocess entry point for guard tests
// that exercise os.Exit paths (authz denial, usage errors). It runs runGuard
// in a fresh process rooted at the fixture dir so the exit code is
// observable.
func TestGuardCheckHelperProcess(t *testing.T) {
	if os.Getenv("GUARD_HELPER") != "1" {
		return
	}
	// Mirror production main(): runGuard raises exitError (sentinel panic)
	// on exit-gated paths. Recover it and exit with the gate's code; a
	// normal return exits 0. (A plain `defer os.Exit(0)` would run during
	// panic unwinding and mask the gate's exit code.)
	defer func() {
		if r := recover(); r != nil {
			if e, ok := r.(exitError); ok {
				os.Exit(e.code)
			}
			panic(r)
		}
		os.Exit(0)
	}()
	args := []string{"check", ".", "--json", "--file", "main.go"}
	if a := os.Getenv("GUARD_HELPER_AGENT"); a != "" {
		args = append(args, "--agent-id", a)
	}
	if a := os.Getenv("GUARD_HELPER_TASK"); a != "" {
		args = append(args, "--task", a)
	}
	runGuard(args)
}

// runGuardHelper spawns the test binary as a guard-check subprocess rooted at
// dir and returns its stdout, stderr and exit code (0 on success).
func runGuardHelper(t *testing.T, dir string, env ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=TestGuardCheckHelperProcess")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GUARD_HELPER=1")
	cmd.Env = append(cmd.Env, env...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("run guard helper: %v", err)
		}
		code = ee.ExitCode()
	}
	return stdout.String(), stderr.String(), code
}

// TestGuardCheck_AuthzVerdict_Allowed: a registered agent (the idempotently
// registered default) with --agent-id/--task yields an allowed verdict nested
// in the v2 guard JSON. The allowed path returns normally (no exit gate).
func TestGuardCheck_AuthzVerdict_Allowed(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := guardCheckFixture(t)
	out := guardCheckJSON(t, root, "--file", "main.go", "--agent-id", "default", "--task", "test")
	if sv, ok := out["schema_version"].(float64); !ok || int(sv) != 2 {
		t.Fatalf("schema_version = %v, want 2", out["schema_version"])
	}
	av, ok := out["authz_verdict"].(map[string]any)
	if !ok {
		t.Fatalf("authz_verdict missing from JSON output: %v", out)
	}
	if d, _ := av["decision"].(string); d != "allowed" {
		t.Fatalf("authz_verdict.decision = %q, want allowed", d)
	}
	if av["agent_id"] != "default" || av["task"] != "test" {
		t.Errorf("authz_verdict agent/task = %v/%v, want default/test", av["agent_id"], av["task"])
	}
	if av["schema_version"] != float64(1) {
		t.Errorf("authz_verdict.schema_version = %v, want 1", av["schema_version"])
	}
	if ps, _ := av["policy_source"].(string); ps == "" {
		t.Error("authz_verdict.policy_source is empty")
	}
	if fp, _ := av["fingerprint"].(string); fp == "" {
		t.Error("authz_verdict.fingerprint is empty")
	}
	if df, ok := av["denied_files"].([]any); !ok || len(df) != 0 {
		t.Errorf("authz_verdict.denied_files = %v, want []", av["denied_files"])
	}
	if dt, ok := av["decided_at"].(string); !ok || dt == "" {
		t.Errorf("authz_verdict.decided_at = %v, want RFC3339 timestamp", av["decided_at"])
	}
}

// TestGuardCheck_AuthzVerdict_Denied: an unknown agent fails closed — denied
// verdict, every requested file denied, exit 2, and the boundary check is
// skipped entirely.
func TestGuardCheck_AuthzVerdict_Denied(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := guardCheckFixture(t)
	stdout, _, code := runGuardHelper(t, root, "GUARD_HELPER_AGENT=ghost", "GUARD_HELPER_TASK=test")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (denied verdict is a blocking gate)", code)
	}
	out := assertValidJSON(t, stdout)
	av, ok := out["authz_verdict"].(map[string]any)
	if !ok {
		t.Fatalf("authz_verdict missing from JSON output: %v", out)
	}
	if d, _ := av["decision"].(string); d != "denied" {
		t.Fatalf("authz_verdict.decision = %q, want denied", d)
	}
	df, ok := av["denied_files"].([]any)
	if !ok || len(df) != 1 || df[0] != "main.go" {
		t.Errorf("authz_verdict.denied_files = %v, want [main.go]", av["denied_files"])
	}
	// The boundary check must NOT have run: violations stay empty.
	if v, ok := out["violations"].([]any); !ok || len(v) != 0 {
		t.Errorf("violations = %v, want empty (boundary check skipped on denial)", out["violations"])
	}
}

// TestGuardCheck_AuthzVerdict_OmittedWhenNoAgentId: callers that do not send
// --agent-id see no authz_verdict key (backward compat), while the schema
// version still bumps to 2.
func TestGuardCheck_AuthzVerdict_OmittedWhenNoAgentId(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := guardCheckFixture(t)
	out := guardCheckJSON(t, root, "--file", "main.go")
	if _, ok := out["authz_verdict"]; ok {
		t.Fatalf("authz_verdict must be omitted when --agent-id is absent: %v", out)
	}
	if sv, ok := out["schema_version"].(float64); !ok || int(sv) != 2 {
		t.Fatalf("schema_version = %v, want 2", out["schema_version"])
	}
}

// TestGuardCheck_AuthzVerdict_RequiresTask: --agent-id without --task is a
// usage error (exit 2) with a clear message.
func TestGuardCheck_AuthzVerdict_RequiresTask(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := guardCheckFixture(t)
	_, stderr, code := runGuardHelper(t, root, "GUARD_HELPER_AGENT=default")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (usage error)", code)
	}
	if !strings.Contains(stderr, "--agent-id requires --task") {
		t.Fatalf("stderr = %q, want %q", stderr, "--agent-id requires --task")
	}
}

// TestGuardCheck_SchemaVersionBumped: the top-level guard contract is v2
// regardless of the authz gate.
func TestGuardCheck_SchemaVersionBumped(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := guardCheckFixture(t)
	out := guardCheckJSON(t, root, "--file", "main.go")
	if sv, ok := out["schema_version"].(float64); !ok || int(sv) != 2 {
		t.Fatalf("schema_version = %v, want 2", out["schema_version"])
	}
}
