package approvalcli

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/storage"
)

// TestAuditAppend_ConsumesValidationOutcome_Block: an entry with a BLOCK
// validation outcome invalidates the blocked context entities (in-memory)
// with the contract reason/source at the entry's timestamp.
func TestAuditAppend_ConsumesValidationOutcome_Block(t *testing.T) {
	root := t.TempDir()
	ts := time.Date(2026, 8, 31, 10, 0, 0, 0, time.UTC)

	entities, reason, source, at, called, restore := stubAuditInvalidate()
	defer restore()

	stdout, _ := appendAuditEntryJSON(t, root, map[string]any{
		"ID": "t1", "Timestamp": ts.Format(time.RFC3339), "AgentID": "blueprint",
		"Action": "commit", "Resource": "/repo", "Result": "BLOCK",
		"ValidationOutcome": map[string]any{
			"Status": "BLOCK", "ExitCode": 1, "BlockedFiles": []string{"foo.go"},
			"CorrelationID": "c1", "Findings": 2,
		},
	})
	if !strings.Contains(stdout, "appended ") {
		t.Fatalf("expected appended line, got %q", stdout)
	}
	if !*called {
		t.Fatal("auditInvalidate was not called for a BLOCK outcome")
	}
	if len(*entities) != 1 || (*entities)[0] != "foo.go" {
		t.Fatalf("invalidation entities = %v, want [foo.go]", *entities)
	}
	if *reason != "blueprint-validation-failed" || *source != "blueprint" {
		t.Errorf("invalidation reason/source = %q/%q, want blueprint-validation-failed/blueprint", *reason, *source)
	}
	if !at.Equal(ts) {
		t.Errorf("invalidation at = %v, want entry timestamp %v", *at, ts)
	}
}

// TestAuditAppend_ConsumesValidationOutcome_Pass: a PASS outcome keeps the
// context fresh — no invalidation.
func TestAuditAppend_ConsumesValidationOutcome_Pass(t *testing.T) {
	root := t.TempDir()
	ts := time.Now().UTC().Add(-time.Minute)

	_, _, _, _, called, restore := stubAuditInvalidate()
	defer restore()

	stdout, _ := appendAuditEntryJSON(t, root, map[string]any{
		"ID": "t2", "Timestamp": ts.Format(time.RFC3339), "AgentID": "blueprint",
		"Action": "commit", "Resource": "/repo", "Result": "PASS",
		"ValidationOutcome": map[string]any{
			"Status": "PASS", "ExitCode": 0, "BlockedFiles": []string{},
			"CorrelationID": "c2", "Findings": 0,
		},
	})
	if !strings.Contains(stdout, "appended ") {
		t.Fatalf("expected appended line, got %q", stdout)
	}
	if *called {
		t.Fatal("PASS must not invalidate context")
	}
}

// TestAuditAppend_ValidationOutcomeNil: a legacy entry without the
// ValidationOutcome field parses fine and invalidates nothing (backward
// compat).
func TestAuditAppend_ValidationOutcomeNil(t *testing.T) {
	root := t.TempDir()
	ts := time.Now().UTC().Add(-time.Minute)

	_, _, _, _, called, restore := stubAuditInvalidate()
	defer restore()

	stdout, _ := appendAuditEntryJSON(t, root, map[string]any{
		"ID": "t3", "Timestamp": ts.Format(time.RFC3339), "AgentID": "legacy",
		"Action": "commit", "Resource": "/repo", "Result": "allowed",
	})
	if !strings.Contains(stdout, "appended ") {
		t.Fatalf("expected appended line, got %q", stdout)
	}
	if *called {
		t.Fatal("entry without ValidationOutcome must not invalidate")
	}
}

// TestAuditAppend_ConsumesValidationOutcome_Warn: a WARN outcome is logged at
// INFO but does not invalidate.
func TestAuditAppend_ConsumesValidationOutcome_Warn(t *testing.T) {
	root := t.TempDir()
	ts := time.Now().UTC().Add(-time.Minute)

	_, _, _, _, called, restore := stubAuditInvalidate()
	defer restore()

	// The stdlib log package holds its own writer reference captured at init,
	// so os.Stderr redirection cannot intercept it — redirect explicitly for
	// the duration of the call.
	var logBuf strings.Builder
	log.SetOutput(&logBuf)
	defer log.SetOutput(os.Stderr)

	stdout, _ := appendAuditEntryJSON(t, root, map[string]any{
		"ID": "t4", "Timestamp": ts.Format(time.RFC3339), "AgentID": "blueprint",
		"Action": "commit", "Resource": "/repo", "Result": "WARN",
		"ValidationOutcome": map[string]any{
			"Status": "WARN", "ExitCode": 0, "BlockedFiles": []string{},
			"CorrelationID": "c4", "Findings": 1,
		},
	})
	if !strings.Contains(stdout, "appended ") {
		t.Fatalf("expected appended line, got %q", stdout)
	}
	if *called {
		t.Fatal("WARN must not invalidate context")
	}
	if !strings.Contains(logBuf.String(), "blueprint validation warning (status=WARN, correlation=c4)") {
		t.Errorf("log = %q, want INFO WARN log line", logBuf.String())
	}
}

// TestAuditRepairCommand: `kern audit repair` re-chains a store with a broken
// link on the first run and reports an already-verified chain on the second.
func TestAuditRepairCommand(t *testing.T) {
	root := t.TempDir()
	auditDir := filepath.Join(root, ".kern", "audit")
	if err := os.MkdirAll(auditDir, 0o755); err != nil {
		t.Fatal(err)
	}
	lock := filepath.Join(auditDir, ".lock")
	store := storage.NewLocal(auditDir)
	l := governance.NewAuditLog().WithStore(store).WithLockPath(lock)
	for i := 0; i < 3; i++ {
		l.Record(governance.AuditEntry{
			AgentID: "agent-x", Action: "write", Resource: "source", Result: "allowed",
		})
	}

	// Corrupt the middle entry's stored hash in the persisted file.
	if err := corruptAuditHash(t, store, "audit-audit-2"); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() {
		runAuditRepair([]string{"--root", root})
	})
	if !strings.Contains(out, "re-chained") {
		t.Fatalf("first repair output = %q, want re-chained", out)
	}

	out = captureStdout(t, func() {
		runAuditRepair([]string{"--root", root})
	})
	if !strings.Contains(out, "already verified") {
		t.Fatalf("second repair output = %q, want already verified", out)
	}
}

// TestRunAuditWarnsOnTamperedChain locks validation W-3: the audit viewer
// verifies the tamper-evident hash chain and warns on stderr when persisted
// records were modified, instead of presenting forged entries as genuine.
func TestRunAuditWarnsOnTamperedChain(t *testing.T) {
	root := t.TempDir()
	ts := time.Now().UTC()
	for i, id := range []string{"t-view-1", "t-view-2"} {
		appendAuditEntryJSON(t, root, map[string]any{
			"ID": id, "Timestamp": ts.Add(time.Duration(i) * time.Second).Format(time.RFC3339),
			"AgentID": "tester", "Action": "tool_call", "Resource": "kern_search", "Result": "allowed",
		})
	}
	var stderr string
	captureStdout(t, func() {
		stderr = captureStderr(t, func() { RunAudit([]string{"--root", root}) })
	})
	if strings.Contains(stderr, "tamper chain verification FAILED") {
		t.Fatalf("intact chain must not warn, got:\n%s", stderr)
	}
	// Tamper with the first historical record (simulate forged history).
	chainPath := filepath.Join(root, ".kern", "audit", "chain.jsonl")
	data, err := os.ReadFile(chainPath)
	if err != nil {
		t.Fatal(err)
	}
	tampered := strings.Replace(string(data), `"allowed"`, `"forged"`, 1)
	if tampered == string(data) {
		t.Fatal("tamper substitution found nothing to rewrite")
	}
	if err := os.WriteFile(chainPath, []byte(tampered), 0o644); err != nil {
		t.Fatal(err)
	}
	captureStdout(t, func() {
		stderr = captureStderr(t, func() { RunAudit([]string{"--root", root}) })
	})
	if !strings.Contains(stderr, "tamper chain verification FAILED") {
		t.Fatalf("tampered chain must warn on stderr, got:\n%s", stderr)
	}
}

func TestAuditRenderSurfacesExternalAppendFields(t *testing.T) {
	root := t.TempDir()
	stdout, _ := appendAuditEntryJSON(t, root, map[string]any{
		"Timestamp": time.Now().Format(time.RFC3339),
		"event":     "deploy",
		"by":        "ops-bot",
		"note":      "rolled out v2 to staging",
		"task":      "t-ext-1",
	})
	if !strings.Contains(stdout, "appended ") {
		t.Fatalf("expected appended line, got %q", stdout)
	}

	out := captureStdout(t, func() { RunAudit([]string{"--root", root}) })
	if !strings.Contains(out, "ops-bot") {
		t.Errorf("rendered audit does not surface AGENT from the appended entry, got:\n%s", out)
	}
	if !strings.Contains(out, "deploy") {
		t.Errorf("rendered audit does not surface ACTION from the appended entry, got:\n%s", out)
	}
	if !strings.Contains(out, "rolled out v2 to staging") {
		t.Errorf("rendered audit does not surface the note from the appended entry, got:\n%s", out)
	}
	if !strings.Contains(out, "t-ext-1") {
		t.Errorf("rendered audit does not surface the task reference from the appended entry, got:\n%s", out)
	}
}
