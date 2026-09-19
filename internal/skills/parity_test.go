package skills

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestSkillCopiesParity verifies that all copies of canonical skills across
// .agents/skills, .github/skills, .opencode/skills, and internal/skills/assets
// remain 100% byte-identical to prevent prompt and tool contract drift.
func TestSkillCopiesParity(t *testing.T) {
	roots := []string{
		filepath.Join("..", "..", ".agents", "skills"),
		filepath.Join("..", "..", ".github", "skills"),
		filepath.Join("..", "..", ".opencode", "skills"),
		filepath.Join("..", "..", "internal", "skills", "assets"),
	}

	for _, s := range SkillNames {
		var canonical []byte
		var canonicalPath string

		for _, r := range roots {
			p := filepath.Join(r, s, "SKILL.md")
			data, err := os.ReadFile(p)
			if err != nil {
				t.Fatalf("missing skill file %s: %v", p, err)
			}
			if canonical == nil {
				canonical = data
				canonicalPath = p
			} else if !bytes.Equal(canonical, data) {
				t.Errorf("%s drifted from %s — run: kern setup to sync or copy the canonical file", p, canonicalPath)
			}
		}
	}
}
