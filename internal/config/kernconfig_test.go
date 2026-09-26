package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestKernConfigLoadAndProfiles(t *testing.T) {
	tempDir := t.TempDir()
	kernDir := filepath.Join(tempDir, ".kern")
	if err := os.MkdirAll(kernDir, 0755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}

	yamlContent := `
profiles:
  default:
    truncate_rules:
      - match: "console.log"
        action: strip_completely
  nodejs-backend:
    exclude_patterns:
      - "node_modules/**"
      - ".env"
    truncate_rules:
      - match: "ValidationError:"
        keep_lines_before: 2
        keep_lines_after: 5
      - match: "console.debug"
        action: strip_completely
`
	if err := os.WriteFile(filepath.Join(kernDir, "kern.yaml"), []byte(yamlContent), 0644); err != nil {
		t.Fatalf("write kern.yaml failed: %v", err)
	}

	ResetCache()

	cfg := Load(tempDir)
	if cfg == nil {
		t.Fatalf("expected non-nil config")
	}

	// 1. Default profile
	def := cfg.DefaultProfile()
	if len(def.TruncateRules) != 1 {
		t.Fatalf("expected 1 truncate rule in default profile, got %d", len(def.TruncateRules))
	}
	if def.TruncateRules[0].Match != "console.log" || def.TruncateRules[0].Action != "strip_completely" {
		t.Errorf("unexpected default rule: %+v", def.TruncateRules[0])
	}

	// 2. Named profile nodejs-backend
	nodeProf := cfg.Profile("nodejs-backend")
	if len(nodeProf.ExcludePatterns) != 2 {
		t.Errorf("expected 2 exclude patterns, got %d", len(nodeProf.ExcludePatterns))
	}
	if len(nodeProf.TruncateRules) != 2 {
		t.Fatalf("expected 2 truncate rules, got %d", len(nodeProf.TruncateRules))
	}
	if nodeProf.TruncateRules[0].Match != "ValidationError:" || nodeProf.TruncateRules[0].KeepLinesAfter != 5 {
		t.Errorf("unexpected nodejs-backend rule 0: %+v", nodeProf.TruncateRules[0])
	}
	if nodeProf.TruncateRules[1].Action != "strip_completely" {
		t.Errorf("unexpected nodejs-backend rule 1: %+v", nodeProf.TruncateRules[1])
	}

	// 3. Unknown profile falls back to default
	unknownProf := cfg.Profile("non-existent")
	if len(unknownProf.TruncateRules) != 1 || unknownProf.TruncateRules[0].Match != "console.log" {
		t.Errorf("expected fallback to default profile for unknown name, got %+v", unknownProf)
	}

	// 4. Cache hit
	cfg2 := Load(tempDir)
	if cfg != cfg2 {
		t.Errorf("expected cache hit returning same pointer")
	}
}

func TestKernConfigMissingFile(t *testing.T) {
	tempDir := t.TempDir()
	ResetCache()

	cfg := Load(tempDir)
	if cfg == nil {
		t.Fatalf("expected non-nil config on missing file")
	}
	def := cfg.DefaultProfile()
	if len(def.TruncateRules) != 0 {
		t.Errorf("expected empty rules on missing file")
	}
}
