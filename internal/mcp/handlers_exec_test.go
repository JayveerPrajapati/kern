package mcp

import (
	"os/exec"
	"strings"
	"testing"
)

// TestExecErrorPathMasksSecrets locks audit A5: the MCP kern_exec error path
// must mask PII/secrets in the error text (and in stderr when the result
// carries it), matching the stdout path and the CLI (cmd/kern/cmd_exec.go
// masks both streams). A failed script commonly prints the secret to stderr
// before dying, and a raw error would leak it.
func TestExecErrorPathMasksSecrets(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not installed")
	}
	t.Setenv("KERN_ALLOW_EXEC", "1")
	t.Setenv("KERN_ALLOW_UNISOLATED", "1")
	t.Setenv("KERN_EXEC_RISK", "")

	err := mcpToolError(t, "kern_exec", map[string]any{
		"code": "echo token=sk-ant-api03-AAAAAAAABBBBBBBBCCCCCCCC >&2; exit 1",
		"lang": "bash",
	})
	if strings.Contains(err, "sk-ant-api03") {
		t.Fatalf("unmasked secret in kern_exec error path: %q", err)
	}
	if !strings.Contains(err, "MASKED") {
		t.Fatalf("expected a masked placeholder in the error, got: %q", err)
	}
}
