package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWireProjectSkills(t *testing.T) {
	dir := t.TempDir()
	// Pre-create .claude and .cursor to test agent-specific project wiring
	if err := os.MkdirAll(filepath.Join(dir, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".cursor"), 0o755); err != nil {
		t.Fatal(err)
	}

	sts := wireProjectSkills(dir)
	if len(sts) == 0 {
		t.Fatalf("expected project skills installed, got empty statuses")
	}

	// 1. Universal open agent skills
	for _, name := range SkillNames {
		skillFile := filepath.Join(dir, ".agents", "skills", name, "SKILL.md")
		data, err := os.ReadFile(skillFile)
		if err != nil {
			t.Fatalf("expected universal skill %s at %s: %v", name, skillFile, err)
		}
		if !strings.Contains(string(data), "name: "+name) {
			t.Fatalf("skill %s missing YAML frontmatter name header", name)
		}
	}

	// 2. Claude Code project skills
	for _, name := range SkillNames {
		skillFile := filepath.Join(dir, ".claude", "skills", name, "SKILL.md")
		if _, err := os.Stat(skillFile); err != nil {
			t.Fatalf("expected claude project skill %s: %v", name, err)
		}
	}

	// 3. Cursor rules
	for _, name := range SkillNames {
		ruleFile := filepath.Join(dir, ".cursor", "rules", name+".mdc")
		data, err := os.ReadFile(ruleFile)
		if err != nil {
			t.Fatalf("expected cursor project rule %s: %v", name, err)
		}
		if !strings.Contains(string(data), "alwaysApply: false") {
			t.Fatalf("cursor rule %s missing alwaysApply frontmatter", name)
		}
	}

	// Idempotency: second run should report already up to date
	sts2 := wireProjectSkills(dir)
	for _, s := range sts2 {
		if !s.Installed || !strings.Contains(s.Note, "up to date") {
			t.Fatalf("expected idempotent no-op, got: %+v", s)
		}
	}
}

func TestWireGlobalSkills(t *testing.T) {
	home := withTempHome(t, true)

	// In empty temp home, all agents should skip gracefully
	stSkip := wireGlobalSkills()
	for _, s := range stSkip {
		if !s.Skipped {
			t.Fatalf("expected skip when agent home absent, got: %+v", s)
		}
	}

	// Pre-create agent home dirs: .gemini, .claude, .cursor, .config/opencode, .codex, .qwen, .qoder
	for _, sub := range []string{".gemini", ".claude", ".cursor", filepath.Join(".config", "opencode"), ".codex", ".qwen", ".qoder"} {
		if err := os.MkdirAll(filepath.Join(home, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	sts := wireGlobalSkills()
	for _, s := range sts {
		if s.Skipped {
			// .copilot wasn't created, so copilot skips as expected
			continue
		}
		if !s.Installed {
			t.Fatalf("expected installed for %s, got: %+v", s.Agent, s)
		}
	}

	// Verify Claude skills
	for _, name := range SkillNames {
		skillFile := filepath.Join(home, ".claude", "skills", name, "SKILL.md")
		if _, err := os.Stat(skillFile); err != nil {
			t.Fatalf("expected claude global skill %s: %v", name, err)
		}
	}

	// Verify Cursor rules
	for _, name := range SkillNames {
		ruleFile := filepath.Join(home, ".cursor", "rules", name+".mdc")
		if _, err := os.Stat(ruleFile); err != nil {
			t.Fatalf("expected cursor global rule %s: %v", name, err)
		}
	}

	// Verify OpenCode skills
	for _, name := range SkillNames {
		skillFile := filepath.Join(home, ".config", "opencode", "skills", name, "SKILL.md")
		if _, err := os.Stat(skillFile); err != nil {
			t.Fatalf("expected opencode global skill %s: %v", name, err)
		}
	}

	// Idempotency: second run should report up to date
	sts2 := wireGlobalSkills()
	for _, s := range sts2 {
		if s.Skipped {
			continue
		}
		if !s.Installed || !strings.Contains(s.Note, "up to date") {
			t.Fatalf("expected idempotent no-op, got: %+v", s)
		}
	}
}

func TestCheckSkills(t *testing.T) {
	home := withTempHome(t, true)
	dir := t.TempDir()

	statuses := checkSkills(dir)
	for _, s := range statuses {
		if s.Installed {
			t.Fatalf("empty dir should not report installed: %+v", s)
		}
	}

	// Wire project skills
	wireProjectSkills(dir)
	statuses = checkSkills(dir)
	var projectInstalled bool
	for _, s := range statuses {
		if s.Agent == "skills (project)" && s.Installed {
			projectInstalled = true
		}
	}
	if !projectInstalled {
		t.Fatalf("project skills should report installed: %+v", statuses)
	}

	// Create .claude and wire global
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	wireGlobalSkills()
	statuses = checkSkills(dir)
	var claudeInstalled bool
	for _, s := range statuses {
		if s.Agent == "skills-claude (global)" && s.Installed {
			claudeInstalled = true
		}
	}
	if !claudeInstalled {
		t.Fatalf("claude global skills should report installed: %+v", statuses)
	}
}
