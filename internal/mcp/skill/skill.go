// Package skill owns agent skill management MCP tool bodies (kern_skill)
// as plain functions.
package skill

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/JayveerPrajapati/kern/internal/mcp/mcpargs"
	"github.com/JayveerPrajapati/kern/internal/skills"
)

func resolveRoot(root string) string {
	if root == "" {
		if cwd, err := os.Getwd(); err == nil {
			return filepath.Clean(cwd)
		}
		return "."
	}
	if abs, err := filepath.Abs(root); err == nil {
		return filepath.Clean(abs)
	}
	return root
}

// Skill manages agent skill runbook inspection and loading.
func Skill(ctx context.Context, args map[string]any) (string, error) {
	action := mcpargs.ArgString(args, "action")
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
		name := mcpargs.ArgString(args, "skill")
		if name == "" {
			return "", fmt.Errorf("skill is required for action=load")
		}
		data, err := skills.ReadSkill(name)
		if err == nil {
			return string(data), nil
		}
		root := resolveRoot(mcpargs.ArgString(args, "root"))
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
