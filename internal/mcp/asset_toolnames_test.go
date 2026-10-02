package mcp

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestAssetToolNamesInCatalog is the permanent form of the Phase-3 content
// lint (F14 family): every kern_* MCP tool name referenced in the embedded
// instruction and skill assets must exist in the live catalog. The
// 2026-10-01 consolidation renamed/removed tools (kern_code_graph,
// kern_run_build, kern_doc_fetch, kern_check, kern_sec, ...) and the assets
// were left stale until the readiness campaign caught it — this test makes
// that class of drift fail CI instead of surviving into every generated
// persona file.
func TestAssetToolNamesInCatalog(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)

	assets := []string{
		filepath.Join(root, "internal", "setup", "assets", "global-rules.md"),
		filepath.Join(root, "internal", "setup", "assets", "AGENTS.md"),
	}
	// Bespoke per-agent instruction rules (continue/windsurf/kiro).
	instructionGlob, err := filepath.Glob(filepath.Join(root, "internal", "setup", "assets", "instructions", "*.md"))
	if err != nil {
		t.Fatalf("glob instructions: %v", err)
	}
	assets = append(assets, instructionGlob...)
	// Skill assets (4 skills).
	skillGlob, err := filepath.Glob(filepath.Join(root, "internal", "skills", "assets", "*", "SKILL.md"))
	if err != nil {
		t.Fatalf("glob skills: %v", err)
	}
	assets = append(assets, skillGlob...)

	toolNames := map[string]bool{}
	for _, n := range ToolNames() {
		toolNames[n] = true
	}

	// kern_<lower_snake> tokens; CLI forms like "kern setup" / "kern run"
	// don't match (no underscore after kern), and hyphenated skill names
	// (kern-team-orchestration) don't match either.
	tokenRe := regexp.MustCompile(`kern_[a-z][a-z0-9_]*`)
	var violations []string
	for _, p := range assets {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read asset %s: %v", p, err)
		}
		seen := map[string]bool{}
		for _, tok := range tokenRe.FindAllString(string(b), -1) {
			if seen[tok] {
				continue
			}
			seen[tok] = true
			if !toolNames[tok] {
				violations = append(violations, filepath.ToSlash(strings.TrimPrefix(p, root+"/"))+": "+tok)
			}
		}
	}
	if len(violations) > 0 {
		sort.Strings(violations)
		t.Fatalf("%d stale tool name(s) referenced in embedded assets:\n%s", len(violations), strings.Join(violations, "\n"))
	}
}
