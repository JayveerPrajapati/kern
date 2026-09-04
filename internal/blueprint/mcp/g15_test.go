package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// G15 (gate "pre-write validation for agents"): blueprint_validate_proposed
// validates proposed file CONTENT (not yet written to disk) via path+content
// pairs. These tests mirror the g5_test.go style: they call the handler
// directly and require the real kern binary (skipped otherwise, like g5).

// g15CallProposed calls the ValidateProposedHandler directly with args.
func g15CallProposed(t *testing.T, args json.RawMessage) ToolResult {
	t.Helper()
	h := ValidateProposedHandler{}
	return h.Handle(context.Background(), args)
}

// g15ParseResult parses the ToolResult's text content as JSON. Fails the test
// if the result is an error result.
func g15ParseResult(t *testing.T, tr ToolResult) map[string]interface{} {
	t.Helper()
	if tr.IsError {
		t.Fatalf("expected success result, got error: %s", tr.Content[0].Text)
	}
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(tr.Content[0].Text), &m); err != nil {
		t.Fatalf("parse result JSON: %v\nraw: %s", err, tr.Content[0].Text)
	}
	return m
}

// TestG15_ProposedSecretBlocked: proposed content containing a hardcoded
// secret must produce a BLOCK with a redacted finding — the token string must
// not appear anywhere in the serialized result.
func TestG15_ProposedSecretBlocked(t *testing.T) {
	g5RequireKern(t)
	g5RequireFingerprint(t)
	dir := g5Repo(t)

	const secret = "AKIA1234567890ABCDEF"
	args, _ := json.Marshal(map[string]interface{}{
		"repo": dir,
		"files": []map[string]interface{}{
			{"path": "config.go", "content": "package main\nconst AWSAccessKey = \"" + secret + "\"\n"},
		},
	})
	tr := g15CallProposed(t, args)
	if tr.IsError {
		t.Fatalf("expected result, got error: %s", tr.Content[0].Text)
	}
	raw := tr.Content[0].Text
	if strings.Contains(raw, secret) {
		t.Fatalf("secret leaked into result JSON:\n%s", raw)
	}
	if strings.Contains(raw, "snippet") {
		t.Fatalf("result JSON must not contain a snippet field:\n%s", raw)
	}

	m := g15ParseResult(t, tr)
	status, _ := m["status"].(string)
	if !strings.EqualFold(status, "BLOCK") {
		t.Fatalf("status=%v, want BLOCK; full: %s", m["status"], raw)
	}
	findings, _ := m["findings"].([]interface{})
	if len(findings) == 0 {
		t.Fatalf("expected findings in blocked result")
	}
	f0, ok := findings[0].(map[string]interface{})
	if !ok {
		t.Fatalf("finding is not an object: %v", findings[0])
	}
	if redacted, _ := f0["redacted"].(bool); !redacted {
		t.Errorf("finding.redacted = false, want true (snippet must never propagate)")
	}
	if f0["category"] != "secret" {
		t.Errorf("finding.category = %v, want secret", f0["category"])
	}
}

// TestG15_ProposedCleanPass: clean proposed content passes.
func TestG15_ProposedCleanPass(t *testing.T) {
	g5RequireKern(t)
	g5RequireFingerprint(t)
	dir := g5Repo(t)

	args, _ := json.Marshal(map[string]interface{}{
		"repo":   dir,
		"source": "agent",
		"files": []map[string]interface{}{
			{"path": "web/extra.go", "content": "package web\nfunc Extra() {}\n"},
		},
	})
	tr := g15CallProposed(t, args)
	if tr.IsError {
		t.Fatalf("expected result, got error: %s", tr.Content[0].Text)
	}
	m := g15ParseResult(t, tr)
	status, _ := m["status"].(string)
	if !strings.EqualFold(status, "PASS") {
		t.Fatalf("status=%v, want PASS; full: %s", m["status"], tr.Content[0].Text)
	}
}

// TestG15_ProposedMalformedArgs: bad JSON, empty files, files without content,
// and files without path are all rejected with an error result.
func TestG15_ProposedMalformedArgs(t *testing.T) {
	// Bad JSON.
	tr := g15CallProposed(t, json.RawMessage(`{bad json`))
	if !tr.IsError {
		t.Fatal("expected error result for malformed JSON")
	}
	if !strings.Contains(strings.ToLower(tr.Content[0].Text), "invalid arguments") {
		t.Errorf("error should mention invalid arguments: %s", tr.Content[0].Text)
	}

	// Empty files array.
	tr = g15CallProposed(t, json.RawMessage(`{"repo":"/tmp/x","files":[]}`))
	if !tr.IsError {
		t.Fatal("expected error result for empty files")
	}
	if !strings.Contains(strings.ToLower(tr.Content[0].Text), "invalid arguments") {
		t.Errorf("error should mention invalid arguments: %s", tr.Content[0].Text)
	}

	// File without content.
	tr = g15CallProposed(t, json.RawMessage(`{"repo":"/tmp/x","files":[{"path":"a.go"}]}`))
	if !tr.IsError {
		t.Fatal("expected error result for file without content")
	}
	if !strings.Contains(strings.ToLower(tr.Content[0].Text), "content") {
		t.Errorf("error should mention missing content: %s", tr.Content[0].Text)
	}

	// File without path.
	tr = g15CallProposed(t, json.RawMessage(`{"repo":"/tmp/x","files":[{"content":"package main\n"}]}`))
	if !tr.IsError {
		t.Fatal("expected error result for file without path")
	}
	if !strings.Contains(strings.ToLower(tr.Content[0].Text), "path") {
		t.Errorf("error should mention missing path: %s", tr.Content[0].Text)
	}

	// Invalid op.
	tr = g15CallProposed(t, json.RawMessage(`{"repo":"/tmp/x","files":[{"path":"a.go","content":"x","op":"explode"}]}`))
	if !tr.IsError {
		t.Fatal("expected error result for invalid op")
	}
	if !strings.Contains(strings.ToLower(tr.Content[0].Text), "op") {
		t.Errorf("error should mention invalid op: %s", tr.Content[0].Text)
	}
}

// TestG15_ProposedSecretScanUsesContent: the file's DISK state is clean, but
// the proposed Content carries a secret — the content scan (not the disk scan)
// must catch it and block.
func TestG15_ProposedSecretScanUsesContent(t *testing.T) {
	g5RequireKern(t)
	g5RequireFingerprint(t)
	dir := g5Repo(t)

	// Disk state is clean and committed.
	g5Write(t, dir, "config.go", "package main\nconst AWSAccessKey = \"clean-placeholder\"\n")
	g5Git(t, dir, "add", "config.go")
	g5Git(t, dir, "commit", "-qm", "clean config")

	args, _ := json.Marshal(map[string]interface{}{
		"repo": dir,
		"files": []map[string]interface{}{
			{"path": "config.go", "content": "package main\nconst AWSAccessKey = \"AKIA1234567890ABCDEF\"\n"},
		},
	})
	tr := g15CallProposed(t, args)
	if tr.IsError {
		t.Fatalf("expected result, got error: %s", tr.Content[0].Text)
	}
	m := g15ParseResult(t, tr)
	status, _ := m["status"].(string)
	if !strings.EqualFold(status, "BLOCK") {
		t.Fatalf("status=%v, want BLOCK (content scan must catch the proposed secret); full: %s", m["status"], tr.Content[0].Text)
	}
	findings, _ := m["findings"].([]interface{})
	if len(findings) == 0 {
		t.Fatalf("expected findings in blocked result")
	}
}

// TestG15_ProposedDuplicationWarn: proposed content that duplicates an
// existing function surfaces a duplication finding through the proposed-content
// handler. Under the two-pass triage model (P1.1), a byte-identical duplicate
// escalates to BLOCK (duplication:confirmed-block) when jscpd confirms the
// same file pair; without the jscpd binary it stays advisory WARN (pure
// advisory fallback). Under P1.5 the duplication category's default
// enforcement was reverted from "block" to "warn" (loader.go): warn
// enforcement downgrades the AGGREGATE STATUS to WARN while a finding keeps
// its intrinsic severity — so a confirmed-block finding may surface as
// status=WARN with severity=block. Either outcome is valid — what must never
// happen is a BLOCK that is not a two-pass confirmed finding, or a
// severity=block finding whose rule is not duplication:confirmed-block.
func TestG15_ProposedDuplicationWarn(t *testing.T) {
	g5RequireKern(t)
	g5RequireFingerprint(t)
	dir := g5Repo(t)

	// Give db/db.go a real function body, then propose an identical function
	// in a new file: structural fingerprints match -> at least a WARN advisory.
	// (Trivial one-line functions are discounted below the WARN threshold by
	// design.)
	const existing = "package db\n\nfunc Query() error {\n\terr := open()\n\tif err != nil {\n\t\treturn err\n\t}\n\treturn nil\n}\n"
	g5Write(t, dir, "db/db.go", existing)
	g5Git(t, dir, "add", "db/db.go")
	g5Git(t, dir, "commit", "-qm", "richer db")

	args, _ := json.Marshal(map[string]interface{}{
		"repo": dir,
		"files": []map[string]interface{}{
			{"path": "web/dup.go", "content": "package web\n\nfunc Query() error {\n\terr := open()\n\tif err != nil {\n\t\treturn err\n\t}\n\treturn nil\n}\n"},
		},
	})
	tr := g15CallProposed(t, args)
	if tr.IsError {
		t.Fatalf("expected result, got error: %s", tr.Content[0].Text)
	}
	m := g15ParseResult(t, tr)
	status, _ := m["status"].(string)

	var dupFinding map[string]interface{}
	findings, _ := m["findings"].([]interface{})
	for _, f := range findings {
		fm, ok := f.(map[string]interface{})
		if !ok {
			continue
		}
		if rule, _ := fm["rule_id"].(string); strings.HasPrefix(rule, "duplication:") {
			dupFinding = fm
			break
		}
	}
	if dupFinding == nil {
		t.Fatalf("expected a duplication finding; full: %s", tr.Content[0].Text)
	}

	rule, _ := dupFinding["rule_id"].(string)
	sev, _ := dupFinding["severity"].(string)
	switch {
	case strings.EqualFold(status, "BLOCK"):
		// Two-pass confirmed: only duplication:confirmed-block may block.
		if rule != "duplication:confirmed-block" || sev != "block" {
			t.Fatalf("BLOCK must come from duplication:confirmed-block, got rule=%s severity=%s; full: %s", rule, sev, tr.Content[0].Text)
		}
	case strings.EqualFold(status, "WARN"):
		// P1.5 warn enforcement (loader.go): the aggregate status is downgraded
		// to WARN, but a finding keeps its intrinsic severity. So a two-pass
		// confirmed finding (duplication:confirmed-block, severity=block) may
		// legitimately surface under a WARN aggregate — that is the documented
		// policy downgrade, not a bug. severity=block under any OTHER
		// duplication rule would be a real bug (unconfirmed findings must
		// never claim block severity).
		if sev == "block" && rule != "duplication:confirmed-block" {
			t.Fatalf("severity=block under WARN must come from duplication:confirmed-block, got rule=%s severity=%s: %v", rule, sev, dupFinding)
		}
	default:
		t.Fatalf("status=%v, want BLOCK (jscpd confirmed) or WARN (advisory fallback); full: %s", m["status"], tr.Content[0].Text)
	}
}
