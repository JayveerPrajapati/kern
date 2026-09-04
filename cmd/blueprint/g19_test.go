package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestG19_CLICheckWritesAudit: `blueprint check --staged` on a fixture leaves
// .blueprint/audit/audit.jsonl with at least one self-hashed JSONL record.
// The audit write lives inside the service, so the CLI adapter is covered
// without any CLI-specific audit code (P1-1).
func TestG19_CLICheckWritesAudit(t *testing.T) {
	bin := g4BuildBinary(t)
	dir := t.TempDir()
	g4GitRepo(t, dir)
	g4WriteBoundaries(t, dir)
	g4WriteFile(t, dir, "db/db.go", "package db\nfunc Query() {}\n")
	g4WriteFile(t, dir, "web/web.go", "package web\nfunc Handle() {}\n")
	g4RunGit(t, dir, "add", "-A")
	g4RunGit(t, dir, "commit", "-qm", "init")

	// Stage a clean change so validation runs (a no-op validation writes no
	// audit record by design).
	g4WriteFile(t, dir, "web/web.go", "package web\nfunc Handle() {}\nfunc Extra() {}\n")
	g4RunGit(t, dir, "add", "web/web.go")

	out, code := g4BlueprintCheck(t, bin, dir)
	if code != 0 {
		t.Fatalf("exit=%d want 0 (clean commit); output:\n%s", code, out)
	}

	auditPath := filepath.Join(dir, ".blueprint", "audit", "audit.jsonl")
	raw, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatalf("read audit trail %s: %v", auditPath, err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) < 1 {
		t.Fatalf("audit trail has %d lines, want >= 1", len(lines))
	}
	// Every line is one self-hashed JSONL record.
	for i, line := range lines {
		if !strings.Contains(line, `"hash"`) {
			t.Errorf("line %d has no self-hash: %s", i, line)
		}
		if strings.Contains(line, `"message"`) || strings.Contains(line, `"snippet"`) {
			t.Errorf("line %d leaks redacted content: %s", i, line)
		}
	}
}

// TestP22_CLICheckNotesResilienceNotRun: `blueprint check` WITHOUT
// --resilience leaves a visible, auditable not-run state — the terminal
// output notes it and the audit record lists resilience in checks_skipped
// (P2-2). The skip must never be a silent omission.
func TestP22_CLICheckNotesResilienceNotRun(t *testing.T) {
	bin := g4BuildBinary(t)
	dir := t.TempDir()
	g4GitRepo(t, dir)
	g4WriteBoundaries(t, dir)
	g4WriteFile(t, dir, "db/db.go", "package db\nfunc Query() {}\n")
	g4WriteFile(t, dir, "web/web.go", "package web\nfunc Handle() {}\n")
	g4RunGit(t, dir, "add", "-A")
	g4RunGit(t, dir, "commit", "-qm", "init")

	// Stage a clean change so validation runs (a no-op writes no audit record).
	g4WriteFile(t, dir, "web/web.go", "package web\nfunc Handle() {}\nfunc Extra() {}\n")
	g4RunGit(t, dir, "add", "web/web.go")

	out, code := g4BlueprintCheck(t, bin, dir) // no --resilience
	if code != 0 {
		t.Fatalf("exit=%d want 0 (clean commit); output:\n%s", code, out)
	}

	// Terminal output notes the absence.
	if !strings.Contains(out, "resilience: not run") {
		t.Errorf("terminal output does not note resilience absence:\n%s", out)
	}

	// Audit record carries the explicit not-run state.
	auditPath := filepath.Join(dir, ".blueprint", "audit", "audit.jsonl")
	raw, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatalf("read audit trail %s: %v", auditPath, err)
	}
	if !strings.Contains(string(raw), `"checks_skipped":["resilience"]`) {
		t.Errorf("audit record lacks explicit resilience not-run state:\n%s", raw)
	}
}
