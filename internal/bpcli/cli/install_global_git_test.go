package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/blueprint/audit"
	"github.com/JayveerPrajapati/kern/internal/blueprint/domain"
)

// TestBypassAuditRecordJSONL unit-tests the emergency-bypass audit record:
// buildBypassAuditRecord is pure (no check pipeline needed), writeApprovalAudit
// persists it to .blueprint/audit/audit.jsonl with Kind "bypass", and the
// P1.4 self-hash chain verifies.
func TestBypassAuditRecordJSONL(t *testing.T) {
	root := t.TempDir()

	rec := buildBypassAuditRecord(root, "prod hotfix needs the commit now", "architecture", 1)

	if rec.Kind != "bypass" {
		t.Errorf("Kind = %q, want %q", rec.Kind, "bypass")
	}
	if rec.Status != domain.StatusWarn {
		t.Errorf("Status = %q, want %q (bypass downgrades the block to WARN)", rec.Status, domain.StatusWarn)
	}
	if rec.ExitCode != 1 {
		t.Errorf("ExitCode = %d, want 1 (the code the pipeline WOULD have returned)", rec.ExitCode)
	}
	if rec.Source != domain.SourceHuman {
		t.Errorf("Source = %q, want %q (break-glass is a human decision)", rec.Source, domain.SourceHuman)
	}
	if rec.Operation != domain.OpCommit {
		t.Errorf("Operation = %q, want %q", rec.Operation, domain.OpCommit)
	}
	if rec.RepoRoot != root {
		t.Errorf("RepoRoot = %q, want %q", rec.RepoRoot, root)
	}
	if rec.Summary.Total != 1 || rec.Summary.Warnings != 1 {
		t.Errorf("Summary = %+v, want Total=1 Warnings=1", rec.Summary)
	}
	if len(rec.Findings) != 1 {
		t.Fatalf("Findings = %d entries, want 1", len(rec.Findings))
	}
	if rec.Findings[0].RuleID != "emergency-bypass" {
		t.Errorf("finding RuleID = %q, want %q", rec.Findings[0].RuleID, "emergency-bypass")
	}
	if rec.Findings[0].Severity != domain.SeverityWarn || rec.Findings[0].Category != domain.CategoryPolicy {
		t.Errorf("finding = %+v, want SeverityWarn + CategoryPolicy", rec.Findings[0])
	}

	writeApprovalAudit(root, rec)

	path := filepath.Join(root, ".blueprint", "audit", "audit.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read audit trail: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 1 {
		t.Fatalf("audit trail has %d records, want 1:\n%s", len(lines), data)
	}
	var got audit.Record
	if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
		t.Fatalf("parse audit record: %v\n%s", err, lines[0])
	}
	if got.Kind != "bypass" {
		t.Errorf("persisted record Kind = %q, want %q", got.Kind, "bypass")
	}
	if got.Findings[0].RuleID != "emergency-bypass" {
		t.Errorf("persisted finding RuleID = %q, want %q", got.Findings[0].RuleID, "emergency-bypass")
	}

	// The P1.4 self-hash chain must verify (genesis record, self-consistent).
	w := audit.NewWriter(path)
	if _, err := w.VerifyChain(); err != nil {
		t.Errorf("audit chain does not verify: %v", err)
	}
}

// TestGlobalPreCommitHookScriptSanitized is the string-level regression pin on
// the embedded global pre-commit hook script: the trivially-abusable bypasses
// (marker files, BLUEPRINT_BYPASS) must never come back, while the env-only
// bypass and the machine-wide audit append must stay. It never creates real
// marker files (machine-global hazard).
func TestGlobalPreCommitHookScriptSanitized(t *testing.T) {
	for _, forbidden := range []string{
		"/tmp/kern-bypass",
		".kern/bypass",
		"BLUEPRINT_BYPASS",
	} {
		if strings.Contains(globalPreCommitHookScript, forbidden) {
			t.Errorf("global pre-commit hook script must NOT contain %q (trivially-abusable bypass)", forbidden)
		}
	}
	for _, required := range []string{
		`"$KERN_BYPASS" = "1"`,
		`"$KERN_ENFORCE" = "0"`,
		"bypass.jsonl",
		"$HOME/.kern/audit",
	} {
		if !strings.Contains(globalPreCommitHookScript, required) {
			t.Errorf("global pre-commit hook script must contain %q (env bypass + machine-wide audit)", required)
		}
	}
}

// TestGlobalPreCommitHookBypassBehavior executes the embedded script with a
// fake KERN_BINARY that always fails: with an env bypass set the hook must
// exit 0 AND append a kern-bypass line to $HOME/.kern/audit/bypass.jsonl;
// without a bypass the fake kern's failure must block the commit (exit 1).
// HOME is overridden per subtest so the real machine-wide audit trail is
// never touched.
func TestGlobalPreCommitHookBypassBehavior(t *testing.T) {
	fakeKern := filepath.Join(t.TempDir(), "kern-fake")
	if err := os.WriteFile(fakeKern, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatalf("write fake kern: %v", err)
	}

	tests := []struct {
		name       string
		env        map[string]string
		wantExit   int
		wantAudit  bool
		wantReason string
	}{
		{"KERN_BYPASS=1 bypasses and audits", map[string]string{"KERN_BYPASS": "1"}, 0, true, "unset"},
		{"KERN_ENFORCE=0 bypasses and audits", map[string]string{"KERN_ENFORCE": "0"}, 0, true, "unset"},
		{"bypass records the reason", map[string]string{"KERN_BYPASS": "1", "KERN_BYPASS_REASON": "prod hotfix"}, 0, true, "prod hotfix"},
		{"no bypass: fake kern failure blocks", map[string]string{}, 1, false, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			hookPath := filepath.Join(t.TempDir(), "pre-commit")
			if err := os.WriteFile(hookPath, []byte(globalPreCommitHookScript), 0o755); err != nil {
				t.Fatalf("write hook: %v", err)
			}

			// Rebuild the child env WITHOUT the real HOME (the hook expands
			// "$HOME", and duplicate env entries are undefined behavior in
			// execve consumers) and without any pre-existing bypass vars so
			// the subtest controls them exclusively.
			env := []string{}
			for _, e := range os.Environ() {
				if strings.HasPrefix(e, "HOME=") ||
					strings.HasPrefix(e, "KERN_BYPASS") ||
					strings.HasPrefix(e, "KERN_ENFORCE") ||
					strings.HasPrefix(e, "KERN_BINARY") {
					continue
				}
				env = append(env, e)
			}
			env = append(env, "HOME="+home, "KERN_BINARY="+fakeKern)
			for k, v := range tc.env {
				env = append(env, k+"="+v)
			}

			cmd := exec.Command(hookPath)
			cmd.Env = env
			out, err := cmd.CombinedOutput()
			if tc.wantExit == 0 && err != nil {
				t.Fatalf("hook exit = non-zero, want 0 (bypass): %v\n%s", err, out)
			}
			if tc.wantExit == 1 {
				if err == nil {
					t.Fatalf("hook exit = 0, want 1 (fake kern failure must block without bypass):\n%s", out)
				}
				if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.ExitCode() != 1 {
					t.Fatalf("hook exit = %v, want 1:\n%s", err, out)
				}
			}

			bypassPath := filepath.Join(home, ".kern", "audit", "bypass.jsonl")
			data, err := os.ReadFile(bypassPath)
			if !tc.wantAudit {
				if err == nil {
					t.Fatalf("bypass audit written without a bypass env: %s", data)
				}
				return
			}
			if err != nil {
				t.Fatalf("bypass audit not appended at %s: %v", bypassPath, err)
			}
			line := strings.TrimSpace(string(data))
			if !strings.Contains(line, `"event":"kern-bypass"`) {
				t.Errorf("audit line missing event kern-bypass: %s", line)
			}
			if !strings.Contains(line, `"reason":"`+tc.wantReason+`"`) {
				t.Errorf("audit line reason = %q, want %q: %s", line, tc.wantReason, line)
			}
		})
	}
}
