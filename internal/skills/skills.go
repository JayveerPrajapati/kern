package skills

import (
	"embed"
	"path/filepath"
	"strings"
)

//go:embed assets/*
var skillsFS embed.FS

// SkillNames lists the canonical kern skills bundled with kern.
var SkillNames = []string{
	"kern-investigate",
	"kern-safe-change",
	"kern-incident-triage",
}

// ReadSkill returns the content of the bundled skill SKILL.md.
func ReadSkill(name string) ([]byte, error) {
	srcRel := filepath.ToSlash(filepath.Join("assets", name, "SKILL.md"))
	return skillsFS.ReadFile(srcRel)
}

// ReadSkillScript returns the content of a helper script within a skill.
func ReadSkillScript(skillName, scriptName string) ([]byte, error) {
	srcRel := filepath.ToSlash(filepath.Join("assets", skillName, "scripts", scriptName))
	return skillsFS.ReadFile(srcRel)
}

// ListSkillScripts returns names of helper scripts within a skill.
func ListSkillScripts(skillName string) ([]string, error) {
	scriptsRel := filepath.ToSlash(filepath.Join("assets", skillName, "scripts"))
	entries, err := skillsFS.ReadDir(scriptsRel)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			out = append(out, e.Name())
		}
	}
	return out, nil
}

// ExtractDescriptionAndBody parses frontmatter description and markdown body from SKILL.md.
func ExtractDescriptionAndBody(data []byte) (string, string) {
	s := string(data)
	if !strings.HasPrefix(s, "---") {
		return "Kern skill playbook", s
	}
	parts := strings.SplitN(s, "---", 3)
	if len(parts) < 3 {
		return "Kern skill playbook", s
	}
	frontmatter := parts[1]
	body := strings.TrimLeft(parts[2], "\r\n")

	desc := ""
	lines := strings.Split(frontmatter, "\n")
	inDesc := false
	for _, line := range lines {
		line = strings.TrimRight(line, "\r")
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "description:") {
			val := strings.TrimSpace(strings.TrimPrefix(trimmed, "description:"))
			if val == ">-" || val == "|" || val == ">" || val == "" {
				inDesc = true
				continue
			}
			desc = strings.Trim(val, "\"'")
			inDesc = false
		} else if inDesc {
			if strings.HasPrefix(trimmed, "---") || (!strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") && trimmed != "") {
				break
			}
			if trimmed != "" {
				if desc != "" {
					desc += " "
				}
				desc += trimmed
			}
		}
	}
	if desc == "" {
		desc = "Kern skill playbook"
	}
	return desc, body
}
