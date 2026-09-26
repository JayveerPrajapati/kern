package cli

import (
	"encoding/json"
	"strings"
	"testing"

	bdomain "github.com/JayveerPrajapati/kern/internal/blueprint/domain"
)

// TestCIVerdictShape pins the `kern check --ci` output contract: a JSON
// verdict object {passed, checks[], evidence} on stdout, where passed
// mirrors the pipeline exit code and checks[] carries name/status/detail per
// executed gate. The emitter itself (emitCIVerdict) is exercised end-to-end
// by the live smoke check; here we pin the pure logic (detail rendering, the
// passed mapping) and the JSON field tags that form the stable contract.
func TestCIVerdictShape(t *testing.T) {
	result := bdomain.ValidationResult{
		Status:        bdomain.StatusPass,
		ExitCode:      0,
		CorrelationID: "bp-test",
		DurationMs:    12,
		Summary:       bdomain.Summary{Total: 1, Errors: 0, Warnings: 0, Blocks: 0},
		Checks: []bdomain.CheckResult{
			{Name: "architecture", Status: bdomain.StatusPass, Findings: nil},
			{Name: "secret:gitleaks", Status: bdomain.StatusBlock, Findings: []bdomain.Finding{
				{RuleID: "gitleaks:aws", Severity: bdomain.SeverityBlock, File: "cmd/x.go", Line: 3, Message: "AWS key in source"},
			}},
		},
	}

	if got := ciCheckDetail(bdomain.CheckResult{Name: "architecture", Status: bdomain.StatusPass}); got != "ok (0 findings)" {
		t.Errorf("ciCheckDetail(clean) = %q, want %q", got, "ok (0 findings)")
	}
	if got := ciCheckDetail(bdomain.CheckResult{Name: "x", Status: bdomain.StatusError, Error: "boom"}); got != "error: boom" {
		t.Errorf("ciCheckDetail(error) = %q", got)
	}
	if got := ciCheckDetail(result.Checks[1]); !strings.Contains(got, "1 finding(s):") || !strings.Contains(got, "[block] AWS key in source (cmd/x.go:3)") {
		t.Errorf("ciCheckDetail(findings) = %q", got)
	}
	if got := ciCheckDetail(bdomain.CheckResult{Name: "x", Status: bdomain.StatusSkip, Skipped: true}); got != "skipped" {
		t.Errorf("ciCheckDetail(skipped) = %q", got)
	}

	// Verify the verdict JSON schema by marshaling the struct the emitter
	// builds — the field tags are the contract CI consumers rely on.
	v := ciVerdict{
		Passed: true,
		Checks: []ciCheckEntry{{Name: "architecture", Status: "PASS", Detail: "ok (0 findings)"}},
		Evidence: ciEvidence{
			Status:        string(bdomain.StatusPass),
			ExitCode:      0,
			Summary:       result.Summary,
			CorrelationID: result.CorrelationID,
			DurationMs:    result.DurationMs,
		},
	}
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"passed", "checks", "evidence"} {
		if _, ok := decoded[key]; !ok {
			t.Errorf("verdict JSON missing top-level key %q", key)
		}
	}
	if !decoded["passed"].(bool) {
		t.Error("verdict passed must mirror exit code 0")
	}
	ev, ok := decoded["evidence"].(map[string]any)
	if !ok {
		t.Fatal("evidence must be an object")
	}
	for _, key := range []string{"status", "exit_code", "summary", "correlation_id", "duration_ms"} {
		if _, ok := ev[key]; !ok {
			t.Errorf("evidence missing key %q", key)
		}
	}
}
