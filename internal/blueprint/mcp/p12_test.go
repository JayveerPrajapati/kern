package mcp

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// P1.2: blueprint consumes kern's retrieval provenance. The agent passes
// result.provenance through the change payload; blueprint records it on the
// audit Record and cites it in the kern chain link. These tests drive the
// real ValidateProposed handler (like the g15 tests) and inspect the audit
// trail it writes.

// p12Provenance returns a governed-mode ContextProvenance matching kern's
// provenance schema exactly (the contract in the P1.2 spec).
func p12Provenance(mode string) map[string]interface{} {
	p := map[string]interface{}{
		"schema_version": 1,
		"mode":           mode,
		"index": map[string]interface{}{
			"tree_oid":          "abc123",
			"content_root":      "sha256:def456",
			"git_commit":        "def456",
			"built_at":          "2026-08-30T11:59:00Z",
			"freshness_verdict": "fresh",
		},
		"symbols": []map[string]interface{}{
			{"name": "FuncName", "qualified": "pkg.FuncName", "file": "path/to/file.go", "line": 42},
		},
	}
	if mode == "governed" {
		p["authorizing_rule"] = map[string]interface{}{
			"policy_source": "task-scope",
			"policy":        "deny-unlisted",
			"fingerprint":   "sha256:abc123",
			"decided_at":    "2026-08-30T12:00:00Z",
		}
	}
	return p
}

// p12LastAuditRecord reads the last JSONL line of <repo>/.blueprint/audit/
// audit.jsonl (each validation appends one record).
func p12LastAuditRecord(t *testing.T, dir string) map[string]interface{} {
	t.Helper()
	f, err := os.Open(filepath.Join(dir, ".blueprint", "audit", "audit.jsonl"))
	if err != nil {
		t.Fatalf("open audit file: %v", err)
	}
	defer f.Close()
	var rec map[string]interface{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	lines := 0
	for sc.Scan() {
		lines++
		var r map[string]interface{}
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			t.Fatalf("parse audit line %d: %v", lines, err)
		}
		rec = r
	}
	if lines == 0 {
		t.Fatal("audit file is empty")
	}
	return rec
}

// TestP12_ValidateProposedCarriesGovernedProvenance: an agent that echoes a
// governed provenance (with authorizing_rule) into blueprint_validate_proposed
// gets it recorded on the audit Record, citing the context authorization that
// informed the change decision.
func TestP12_ValidateProposedCarriesGovernedProvenance(t *testing.T) {
	g5RequireKern(t)
	dir := g5Repo(t)

	args, _ := json.Marshal(map[string]interface{}{
		"repo": dir,
		"files": []map[string]interface{}{
			{"path": "web/extra.go", "content": "package web\nfunc Extra() {}\n"},
		},
		"context_provenance": p12Provenance("governed"),
	})
	tr := g15CallProposed(t, args)
	if tr.IsError {
		t.Fatalf("governed provenance rejected: %s", tr.Content[0].Text)
	}
	m := g15ParseResult(t, tr)
	if status, _ := m["status"].(string); !strings.EqualFold(status, "PASS") {
		t.Fatalf("status=%v, want PASS for schema_version 1; full: %s", m["status"], tr.Content[0].Text)
	}

	rec := p12LastAuditRecord(t, dir)
	prov, ok := rec["context_provenance"].(map[string]interface{})
	if !ok {
		t.Fatalf("audit record missing context_provenance: %v", rec)
	}
	if prov["mode"] != "governed" {
		t.Errorf("mode = %v, want governed", prov["mode"])
	}
	rule, ok := prov["authorizing_rule"].(map[string]interface{})
	if !ok {
		t.Fatalf("authorizing_rule missing from governed audit provenance: %v", prov)
	}
	if rule["policy_source"] != "task-scope" || rule["policy"] != "deny-unlisted" || rule["fingerprint"] != "sha256:abc123" {
		t.Errorf("authorizing_rule = %v", rule)
	}
}

// TestP12_ValidateProposedAcceptsRawProvenance: raw-mode provenance (mode=
// "raw", no authorizing_rule) is accepted and recorded without error.
func TestP12_ValidateProposedAcceptsRawProvenance(t *testing.T) {
	g5RequireKern(t)
	dir := g5Repo(t)

	args, _ := json.Marshal(map[string]interface{}{
		"repo": dir,
		"files": []map[string]interface{}{
			{"path": "web/extra.go", "content": "package web\nfunc Extra() {}\n"},
		},
		"context_provenance": p12Provenance("raw"),
	})
	tr := g15CallProposed(t, args)
	if tr.IsError {
		t.Fatalf("raw-mode provenance rejected: %s", tr.Content[0].Text)
	}
	m := g15ParseResult(t, tr)
	if status, _ := m["status"].(string); !strings.EqualFold(status, "PASS") {
		t.Fatalf("status=%v, want PASS for raw-mode provenance; full: %s", m["status"], tr.Content[0].Text)
	}

	rec := p12LastAuditRecord(t, dir)
	prov, ok := rec["context_provenance"].(map[string]interface{})
	if !ok {
		t.Fatalf("audit record missing context_provenance: %v", rec)
	}
	if prov["mode"] != "raw" {
		t.Errorf("mode = %v, want raw", prov["mode"])
	}
	if _, has := prov["authorizing_rule"]; has {
		t.Errorf("raw audit provenance must not carry authorizing_rule: %v", prov)
	}
	if _, has := prov["index"]; !has {
		t.Errorf("raw audit provenance must carry index: %v", prov)
	}
}

// TestP12_ValidateProposedSchemaSkewWarns: a provenance speaking a different
// schema_version must NOT fail the pipeline — it emits a WARN-only
// provenance:schema-version finding (status at most WARN, never BLOCK/ERROR)
// and the provenance is still recorded.
func TestP12_ValidateProposedSchemaSkewWarns(t *testing.T) {
	g5RequireKern(t)
	dir := g5Repo(t)

	skewed := p12Provenance("governed")
	skewed["schema_version"] = 2 // future contract version
	args, _ := json.Marshal(map[string]interface{}{
		"repo": dir,
		"files": []map[string]interface{}{
			{"path": "web/extra.go", "content": "package web\nfunc Extra() {}\n"},
		},
		"context_provenance": skewed,
	})
	tr := g15CallProposed(t, args)
	if tr.IsError {
		t.Fatalf("schema skew must not fail the pipeline: %s", tr.Content[0].Text)
	}
	m := g15ParseResult(t, tr)
	status, _ := m["status"].(string)
	if strings.EqualFold(status, "BLOCK") || strings.EqualFold(status, "ERROR") {
		t.Fatalf("status=%v, want at most WARN for schema skew", m["status"])
	}
	var skewFinding map[string]interface{}
	findings, _ := m["findings"].([]interface{})
	for _, f := range findings {
		fm, ok := f.(map[string]interface{})
		if !ok {
			continue
		}
		if rule, _ := fm["rule_id"].(string); rule == "provenance:schema-version" {
			skewFinding = fm
		}
	}
	if skewFinding == nil {
		t.Fatalf("expected provenance:schema-version WARN finding; full: %s", tr.Content[0].Text)
	}
	if sev, _ := skewFinding["severity"].(string); sev != "warn" {
		t.Errorf("skew finding severity = %v, want warn", skewFinding["severity"])
	}

	// The skewed provenance is still recorded on the audit record.
	rec := p12LastAuditRecord(t, dir)
	prov, ok := rec["context_provenance"].(map[string]interface{})
	if !ok {
		t.Fatalf("audit record missing context_provenance: %v", rec)
	}
	if sv, _ := prov["schema_version"].(float64); int(sv) != 2 {
		t.Errorf("audit schema_version = %v, want 2 (recorded as received)", prov["schema_version"])
	}
}

// TestP12_ValidateProposedWithoutProvenance: no context_provenance in the
// payload → the audit record simply omits it (existing flows unchanged).
func TestP12_ValidateProposedWithoutProvenance(t *testing.T) {
	g5RequireKern(t)
	dir := g5Repo(t)

	args, _ := json.Marshal(map[string]interface{}{
		"repo": dir,
		"files": []map[string]interface{}{
			{"path": "web/extra.go", "content": "package web\nfunc Extra() {}\n"},
		},
	})
	tr := g15CallProposed(t, args)
	if tr.IsError {
		t.Fatalf("validation without provenance must still work: %s", tr.Content[0].Text)
	}
	rec := p12LastAuditRecord(t, dir)
	if _, has := rec["context_provenance"]; has {
		t.Errorf("audit record must omit context_provenance when none provided: %v", rec)
	}
}
