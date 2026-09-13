package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/JayveerPrajapati/kern/internal/skills"
)

// handleSkill implements kern_skill: the model-facing face of the bundled
// agent-skill catalog. Action "catalog" (default) lists every bundled skill
// with its frontmatter description; action "load" returns the full SKILL.md
// runbook for a named skill so the model can follow the repo's own operating
// procedures. Mirrors dsh's model-facing skill tool (dsh-tool-skill).
func (s *Server) handleSkill(ctx context.Context, args map[string]any) (string, error) {
	action := argString(args, "action")
	if action == "" {
		action = "catalog"
	}
	switch action {
	case "catalog":
		type entry struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		}
		entries := make([]entry, 0, len(skills.SkillNames))
		for _, name := range skills.SkillNames {
			data, err := skills.ReadSkill(name)
			if err != nil {
				continue
			}
			desc, _ := skills.ExtractDescriptionAndBody(data)
			entries = append(entries, entry{Name: name, Description: desc})
		}
		out, err := json.MarshalIndent(entries, "", "  ")
		if err != nil {
			return "", err
		}
		return string(out), nil
	case "load":
		name := argString(args, "skill")
		if name == "" {
			return "", fmt.Errorf("skill is required for action=load")
		}
		data, err := skills.ReadSkill(name)
		if err == nil {
			return string(data), nil
		}
		// Fall back to user skills declared under <root>/.kern/skills (same
		// source the legacy skill listing reads), so kern_skill is the
		// complete skill face.
		root := resolveRoot(argString(args, "root"))
		if userSkills, uerr := skills.LoadSkillsFromDir(filepath.Join(root, ".kern", "skills")); uerr == nil {
			for _, us := range userSkills {
				if us.Name == name || us.Name == "kern-"+name {
					return us.Body, nil
				}
			}
		}
		return "", fmt.Errorf("unknown skill %q (available: %v)", name, skills.SkillNames)
	default:
		return "", fmt.Errorf("unknown action %q (use catalog or load)", action)
	}
}