package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestG25_FindingJSONOmitsZeroValueProvenance verifies the Kern 2.0 Evidence
// provenance fields are omitempty: zero values (empty strings, 0 confidence)
// are absent from the marshaled JSON, so existing consumers see no shape
// change, while populated fields serialize.
func TestG25_FindingJSONOmitsZeroValueProvenance(t *testing.T) {
	f := Finding{
		RuleID:   "secret:hardcoded-secret",
		Severity: SeverityBlock,
		Category: CategorySecret,
		File:     "main.go",
		Message:  "hardcoded API key detected",
	}
	b, err := json.Marshal(f)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(b)
	for _, absent := range []string{"rule_version", "kern_version", "index_freshness", "confidence", "scope"} {
		if strings.Contains(s, absent) {
			t.Errorf("JSON %s contains %q, want omitted (zero value)", s, absent)
		}
	}

	// A populated finding marshals all provenance fields.
	f2 := f
	f2.RuleVersion = "1"
	f2.KernVersion = "dev"
	f2.IndexFreshness = "fresh"
	f2.Confidence = 0.95
	f2.Scope = "file"
	b2, err := json.Marshal(f2)
	if err != nil {
		t.Fatalf("marshal populated: %v", err)
	}
	s2 := string(b2)
	for _, present := range []string{`"rule_version":"1"`, `"kern_version":"dev"`, `"index_freshness":"fresh"`, `"confidence":0.95`, `"scope":"file"`} {
		if !strings.Contains(s2, present) {
			t.Errorf("JSON %s missing %s", s2, present)
		}
	}
}
