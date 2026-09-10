package mcp

import (
	"os/exec"
	"strings"
	"testing"
)

// TestExecEgressArgReachesGate asserts the optional egress tool argument is
// parsed into script.Run.Egress and threaded into the deny-by-default egress
// gate (AUD-01): a network-shaped script that would be refused for declaring
// no egress targets succeeds (or gets the per-target policy decision) once
// structured egress targets are passed via the tool argument.
//
// no_isolate + KERN_ALLOW_NO_ISOLATE=1 makes the run deterministically
// unisolated on every platform (the same trick the script package's egress
// tests use), so the gate is always exercised regardless of whether an
// unprivileged netns is available.
func TestExecEgressArgReachesGate(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not installed")
	}
	// kern_exec is a governed exec surface: opt in. Pin the egress policy to
	// the fail-closed default (local-only) so the per-target checks below are
	// deterministic.
	t.Setenv("KERN_ALLOW_EXEC", "1")
	t.Setenv("KERN_ALLOW_NO_ISOLATE", "1")
	t.Setenv("KERN_EGRESS_POLICY", "")

	// "echo curl" trips the network-call heuristic (`\bcurl\b`) but runs
	// instantly without touching the network.
	code := "echo curl\n"
	args := map[string]any{"code": code, "lang": "bash", "no_isolate": "true"}

	// No declaration (comment or structured): refused by the deny-by-default gate.
	err := mcpToolError(t, "kern_exec", args)
	if !strings.Contains(err, "declares no egress targets") {
		t.Fatalf("expected deny-by-default refusal, got %q", err)
	}

	// Structured egress via the tool argument: the gate is satisfied and the
	// script runs. A declared LOCAL target passes the default local-only policy.
	out := mcpAssertOK(t, "kern_exec", map[string]any{
		"code":       code,
		"lang":       "bash",
		"no_isolate": "true",
		"egress":     []string{"localhost:80"},
	})
	if strings.TrimSpace(out) != "curl" {
		t.Fatalf("expected script stdout, got %q", out)
	}

	// A structured EXTERNAL target is refused by the per-target policy check —
	// the policy decision, not the no-declaration gate.
	err = mcpToolError(t, "kern_exec", map[string]any{
		"code":       code,
		"lang":       "bash",
		"no_isolate": "true",
		"egress":     []string{"8.8.8.8:443"},
	})
	if !strings.Contains(err, "denies target") || !strings.Contains(err, "8.8.8.8:443") {
		t.Fatalf("expected per-target policy denial, got %q", err)
	}
	if strings.Contains(err, "declares no egress targets") {
		t.Fatalf("no-declaration gate fired despite a structured egress argument: %q", err)
	}

	// An invalid structured entry (not host:port) is refused with a clear
	// error naming the offending value.
	err = mcpToolError(t, "kern_exec", map[string]any{
		"code":       code,
		"lang":       "bash",
		"no_isolate": "true",
		"egress":     []string{"not-a-target"},
	})
	if !strings.Contains(err, "invalid egress target") || !strings.Contains(err, "not-a-target") {
		t.Fatalf("expected invalid-target error naming the value, got %q", err)
	}

	// Back-compat: a script that is NOT network-shaped still runs without any
	// egress argument (callers unaffected by the new optional parameter).
	out = mcpAssertOK(t, "kern_exec", map[string]any{
		"code":       "echo hi\n",
		"lang":       "bash",
		"no_isolate": "true",
	})
	if strings.TrimSpace(out) != "hi" {
		t.Fatalf("expected hi, got %q", out)
	}
}
