package skills

import (
	"strings"
	"testing"
)

func TestSkillsReadAndScripts(t *testing.T) {
	if len(SkillNames) != 4 {
		t.Fatalf("expected 4 skills, got %d", len(SkillNames))
	}

	for _, name := range SkillNames {
		data, err := ReadSkill(name)
		if err != nil {
			t.Fatalf("ReadSkill(%s) failed: %v", name, err)
		}
		if !strings.Contains(string(data), "name: "+name) {
			t.Fatalf("skill %s missing name header", name)
		}

		desc, body := ExtractDescriptionAndBody(data)
		if desc == "" || strings.Contains(desc, "---") {
			t.Fatalf("skill %s invalid extracted description: %q", name, desc)
		}
		if len(body) == 0 {
			t.Fatalf("skill %s has empty body", name)
		}

		scripts, err := ListSkillScripts(name)
		if err != nil {
			t.Fatalf("ListSkillScripts(%s) failed: %v", name, err)
		}
		for _, sName := range scripts {
			sData, err := ReadSkillScript(name, sName)
			if err != nil || len(sData) == 0 {
				t.Fatalf("ReadSkillScript(%s, %s) failed: %v", name, sName, err)
			}
		}
	}
}
