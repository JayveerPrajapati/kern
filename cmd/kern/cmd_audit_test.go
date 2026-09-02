package main

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	kctx "github.com/JayveerPrajapati/kern/internal/context"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/storage"
)

// captureStderr runs fn with os.Stderr redirected to a pipe and returns the
// captured output (stdlib log writes there by default).
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	defer func() { os.Stderr = old }()
	fn()
	_ = w.Close()
	os.Stderr = old
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// appendAuditEntryJSON writes the entry map as an AuditEntry JSON file and
// runs `kern audit append --root <root> --file <path>` in-process, capturing
// stdout and stderr.
func appendAuditEntryJSON(t *testing.T, root string, entry map[string]any) (string, string) {
	t.Helper()
	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "entry.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr string
	stdout = captureStdout(t, func() {
		stderr = captureStderr(t, func() {
			runAuditAppend([]string{"--root", root, "--file", path})
		})
	})
	return stdout, stderr
}

// stubAuditInvalidate replaces the auditInvalidate seam with a recording
// stub and returns pointers to what was observed plus the restore function.
// called reports whether the seam was invoked at all.
func stubAuditInvalidate() (entities *[]string, reason, source *string, at *time.Time, called *bool, restore func()) {
	var es []string
	var r, s string
	var t time.Time
	var invoked bool
	orig := auditInvalidate
	auditInvalidate = func(in []string, reasonIn, sourceIn string, atIn time.Time) []kctx.InvalidationMarker {
		invoked = true
		es, r, s, t = in, reasonIn, sourceIn, atIn
		return make([]kctx.InvalidationMarker, len(in))
	}
	return &es, &r, &s, &t, &invoked, func() { auditInvalidate = orig }
}

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

// corruptAuditHash rewrites a stored entry with a bogus hash to simulate a
// broken chain link.
func corruptAuditHash(t *testing.T, store storage.Store, key string) error {
	t.Helper()
	raw, err := store.Get(context.Background(), key)
	if err != nil {
		return err
	}
	var ent governance.AuditEntry
	if err := json.Unmarshal(raw, &ent); err != nil {
		return err
	}
	ent.Hash = strings.Repeat("0", 64)
	data, err := json.Marshal(ent)
	if err != nil {
		return err
	}
	return store.Put(context.Background(), key, data)
}
