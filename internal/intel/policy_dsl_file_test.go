package intel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestParsePolicySpecReadsFile: a --policy value that names an existing file
// must be READ and parsed from that file — never silently treated as inline
// text (which yielded 0 rules and a vacuous ALLOWED) (F7).
func TestParsePolicySpecReadsFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pol-block.json")
	content := `{"name":"block-test","rules":[{"id":"r1","category":"security","severity":"block","description":"block secrets dir","protected_paths":["secrets/*"]}]}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	spec, err := ParsePolicySpec(path)
	if err != nil {
		t.Fatalf("ParsePolicySpec(%q): %v", path, err)
	}
	if len(spec.Rules) != 1 || spec.Rules[0].ID != "r1" {
		t.Fatalf("expected the file's rule to be loaded, got %+v", spec.Rules)
	}
	// The rule must actually evaluate to a BLOCK for a violating path.
	eval := EvaluatePolicy(spec, []string{"secrets/x.go"}, "", nil)
	if eval.Allowed {
		t.Fatal("block rule from file must evaluate to not-allowed")
	}
	if len(eval.Violations) == 0 {
		t.Fatal("expected a violation from the file's block rule")
	}
}

// TestParsePolicySpecFileWithNoRules: a policy FILE that parses to zero rules
// must fail loud — never evaluate a vacuous ALLOWED.
func TestParsePolicySpecFileWithNoRules(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.yaml")
	if err := os.WriteFile(path, []byte("# no rules here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ParsePolicySpec(path); err == nil {
		t.Fatal("expected an error for a policy file with no rules")
	} else if !strings.Contains(err.Error(), "no rules") {
		t.Fatalf("error should mention the vacuous-allow refusal: %v", err)
	}
}

// TestParsePolicySpecMissingFile: a path-like value that does not exist is a
// hard error, not a silent 0-rule allow.
func TestParsePolicySpecMissingFile(t *testing.T) {
	cases := []string{
		"/tmp/definitely-not-a-policy-xyz.json",
		"policies/block.yaml",
		"missing-policy.yml",
	}
	for _, c := range cases {
		if _, err := ParsePolicySpec(c); err == nil {
			t.Errorf("ParsePolicySpec(%q): expected a hard error for a missing policy file", c)
		} else if !strings.Contains(err.Error(), "policy file not found") {
			t.Errorf("ParsePolicySpec(%q): error should name the missing file: %v", c, err)
		}
	}
}

// TestParsePolicySpecInlineTextKeepsBehavior: inline JSON/YAML text is parsed
// as before — including values that contain "/" (protected paths) and
// multi-line YAML — and is never mistaken for a file path.
func TestParsePolicySpecInlineTextKeepsBehavior(t *testing.T) {
	inlineJSON := `{"name":"x","rules":[{"id":"r1","severity":"block","protected_paths":[".github/workflows/*"]}]}`
	spec, err := ParsePolicySpec(inlineJSON)
	if err != nil {
		t.Fatalf("inline JSON with slashes must parse inline: %v", err)
	}
	if len(spec.Rules) != 1 {
		t.Fatalf("expected 1 rule from inline JSON, got %d", len(spec.Rules))
	}

	inlineYAML := `name: guardrails
rules:
- id: r1
  severity: block
  protected_path: .github/workflows/*
`
	spec, err = ParsePolicySpec(inlineYAML)
	if err != nil {
		t.Fatalf("multi-line inline YAML with slashes must parse inline: %v", err)
	}
	if len(spec.Rules) != 1 || len(spec.Rules[0].ProtectedPaths) != 1 {
		t.Fatalf("expected 1 rule with 1 protected path from inline YAML, got %+v", spec.Rules)
	}

	// Single-line YAML rule stays inline too.
	spec, err = ParsePolicySpec("- id: r1\n  severity: warn\n  category: arch\n")
	if err != nil || len(spec.Rules) != 1 {
		t.Fatalf("single-line YAML rule must parse inline: %+v err=%v", spec.Rules, err)
	}
}
