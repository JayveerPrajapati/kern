package main

import (
	"strings"
	"testing"
)

// TestRunExecMasksSecretsInStdout locks the CLI trust-boundary parity: `kern
// exec` must mask secrets in script output the same way the MCP kern_exec
// tool does (internal/mcp/handlers_exec.go). Found by the deep-dive
// validation run (kern_validation_report.md, W-2): CLI automation piping
// `kern exec` output into prompts previously lost the masking guarantee.
func TestRunExecMasksSecretsInStdout(t *testing.T) {
	t.Setenv("KERN_ALLOW_EXEC", "1")
	t.Setenv("KERN_ALLOW_UNISOLATED", "1") // no netns on darwin; test env opt-in
	out := captureStdout(t, func() {
		runExec([]string{
			`echo token=sk-ant-api03-AAAAAAAABBBBBBBBCCCCCCCC and email=jay@x.com`,
			"--lang", "bash",
		})
	})
	if strings.Contains(out, "sk-ant-api03") || strings.Contains(out, "jay@x.com") {
		t.Fatalf("unmasked secret in kern exec output:\n%s", out)
	}
	if !strings.Contains(out, "[MASKED_TOKEN_1]") {
		t.Fatalf("expected masked token placeholder, got:\n%s", out)
	}
	if !strings.Contains(out, "[MASKED_EMAIL_1]") {
		t.Fatalf("expected masked email placeholder, got:\n%s", out)
	}
}

// TestRunExecJSONPathMasksSecrets locks the --json path: the JSON result
// must carry the masked stdout, not the raw script output.
func TestRunExecJSONPathMasksSecrets(t *testing.T) {
	t.Setenv("KERN_ALLOW_EXEC", "1")
	t.Setenv("KERN_ALLOW_UNISOLATED", "1")
	out := captureStdout(t, func() {
		runExec([]string{
			`echo pat=ghp_16C7e42F292c6912E7710c838347Ae178B4a`,
			"--lang", "bash", "--json",
		})
	})
	if strings.Contains(out, "ghp_16C7e42F292c6912") {
		t.Fatalf("unmasked GitHub token in JSON output:\n%s", out)
	}
	if !strings.Contains(out, "[MASKED_GITHUB_1]") {
		t.Fatalf("expected masked GitHub placeholder, got:\n%s", out)
	}
}

// TestRunExecUndetectableLanguageExits2 pins the C-LOW exit-code contract:
// content with no language signal (no --lang, nothing DetectLang can
// classify) is a MISSING REQUIRED ARGUMENT — a usage error that must exit 2,
// not the runtime-error 1. Live repro: `kern exec "hello world foo bar"`.
func TestRunExecUndetectableLanguageExits2(t *testing.T) {
	t.Setenv("KERN_ALLOW_EXEC", "1")
	t.Setenv("KERN_ALLOW_UNISOLATED", "1") // no netns on darwin; test env opt-in
	errOut := captureStderr(t, func() {
		assertExitCode(t, 2, func() {
			runExec([]string{"hello world foo bar"})
		})
	})
	if !strings.Contains(errOut, "cannot detect language") {
		t.Fatalf("expected undetectable-language usage error on stderr, got:\n%s", errOut)
	}
}

// TestRunExecUndetectableLanguageJSONExits2 pins the same C-LOW contract on
// the --json path: the usage error must exit 2 there too (the JSON output
// shape changes, the exit-code contract does not).
func TestRunExecUndetectableLanguageJSONExits2(t *testing.T) {
	t.Setenv("KERN_ALLOW_EXEC", "1")
	t.Setenv("KERN_ALLOW_UNISOLATED", "1")
	errOut := captureStderr(t, func() {
		assertExitCode(t, 2, func() {
			runExec([]string{"hello world foo bar", "--json"})
		})
	})
	if !strings.Contains(errOut, "cannot detect language") {
		t.Fatalf("expected undetectable-language usage error on stderr, got:\n%s", errOut)
	}
}
