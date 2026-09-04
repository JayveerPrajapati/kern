package domain

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// schemaPath resolves the shared findings schema relative to this package
// (internal/blueprint/domain -> repo root).
func schemaPath() string {
	return filepath.Join("..", "..", "..", "schema", "findings-schema.json")
}

// TestFindingsSchemaParses verifies the machine-readable contract is valid
// JSON with the documented required fields and additive evolution rule.
func TestFindingsSchemaParses(t *testing.T) {
	b, err := os.ReadFile(schemaPath())
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	var schema map[string]any
	if err := json.Unmarshal(b, &schema); err != nil {
		t.Fatalf("schema is not valid JSON: %v", err)
	}
	req, ok := schema["required"].([]any)
	if !ok {
		t.Fatal("schema missing required array")
	}
	want := map[string]bool{"rule_id": true, "severity": true, "category": true, "message": true}
	for _, r := range req {
		delete(want, r.(string))
	}
	if len(want) > 0 {
		t.Errorf("schema required fields missing: %v", want)
	}
	if ap, _ := schema["additionalProperties"].(bool); !ap {
		t.Error("schema must allow additional properties (additive evolution)")
	}
}

// TestRepresentativeFindingConforms marshals a finding with the full
// provenance surface (P2-4) and asserts the contract fields are present with
// enum-conforming values and no snippet ever leaks.
func TestRepresentativeFindingConforms(t *testing.T) {
	f := Finding{
		RuleID:       "duplication:advisory",
		Severity:     SeverityWarn,
		Category:     CategoryDuplication,
		File:         "payments/retry.go",
		Line:         12,
		Message:      "duplicate-candidate: DoRetry (similarity 0.88) matches shared/retry.go::RetryRequest",
		Explanation:  "structurally similar to an existing function",
		SuggestedFix: "Reuse shared/retry.go::RetryRequest",
		Evidence: []Evidence{{
			Kind:        "structural-fingerprint",
			Description: "similarity score: 0.88, bucket: warning",
			Location:    "payments/retry.go:12 (new) vs shared/retry.go:12 (existing)",
		}},
		RuleVersion: "1",
		KernVersion: "dev",
		Confidence:  0.88,
		Scope:       "file",
	}
	b, err := json.Marshal(f)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, k := range []string{"rule_id", "severity", "category", "message"} {
		if _, ok := m[k]; !ok {
			t.Errorf("required key %q missing from finding JSON", k)
		}
	}
	for _, k := range []string{"rule_version", "kern_version", "confidence", "scope", "evidence"} {
		if _, ok := m[k]; !ok {
			t.Errorf("provenance key %q missing from finding JSON", k)
		}
	}
	if m["severity"] != "warn" || m["category"] != "duplication" {
		t.Errorf("enum values drifted: severity=%v category=%v", m["severity"], m["category"])
	}
	if _, ok := m["snippet"]; ok {
		t.Error("finding JSON must never carry a snippet key (redaction invariant)")
	}
}

// TestZeroValueFindingOmitsProvenance verifies the omitempty contract: an
// unstamped finding carries no provenance keys in its JSON.
func TestZeroValueFindingOmitsProvenance(t *testing.T) {
	b, err := json.Marshal(Finding{RuleID: "x", Severity: SeverityWarn, Category: CategoryPolicy, Message: "m"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, k := range []string{"rule_version", "kern_version", "confidence", "scope", "index_freshness"} {
		if _, ok := m[k]; ok {
			t.Errorf("zero-value provenance key %q must be omitted (omitempty), got: %v", k, m[k])
		}
	}
}
