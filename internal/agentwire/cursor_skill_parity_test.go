package agentwire

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/skills"
)

// TestCursorSkillCopiesParity verifies the converted Cursor rules remain a
// deterministic rendering of the canonical skills. Cursor files are not
// byte-identical to SKILL.md by design, so compare each tracked .mdc to the
// same MakeCursorMdc conversion used by setup.
func TestCursorSkillCopiesParity(t *testing.T) {
	rulesDir := filepath.Join("..", "..", ".cursor", "rules")
	if _, err := os.Stat(rulesDir); os.IsNotExist(err) {
		t.Skip(".cursor/rules directory not present in repo")
	}
	for _, skill := range skills.SkillNames {
		canonical, err := skills.ReadSkill(skill)
		if err != nil {
			t.Fatalf("read canonical skill %s: %v", skill, err)
		}
		rulePath := filepath.Join(rulesDir, skill+".mdc")
		actual, err := os.ReadFile(rulePath)
		if err != nil {
			t.Fatalf("read Cursor rule %s: %v", rulePath, err)
		}
		if expected := MakeCursorMdc(skill, canonical); !bytes.Equal(actual, expected) {
			t.Errorf("%s drifted from canonical skill %s — run: kern setup to sync", rulePath, skill)
		}
	}
}
