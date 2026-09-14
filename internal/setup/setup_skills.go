package setup

import (
	"bytes"
	"os"
	"path/filepath"

	"github.com/JayveerPrajapati/kern/internal/skills"
)

// SkillNames lists the canonical kern skills bundled with setup.
var SkillNames = skills.SkillNames

// ReadSkill returns the content of the bundled skill SKILL.md.
func ReadSkill(name string) ([]byte, error) {
	return skills.ReadSkill(name)
}

// ExtractDescriptionAndBody parses frontmatter description and markdown body from SKILL.md.
func ExtractDescriptionAndBody(data []byte) (string, string) {
	return skills.ExtractDescriptionAndBody(data)
}

// WireProjectSkills is the exported wrapper around wireProjectSkills.
func WireProjectSkills(root string) []Status {
	return wireProjectSkills(root)
}

// WireGlobalSkills is the exported wrapper around wireGlobalSkills.
func WireGlobalSkills() []Status {
	return wireGlobalSkills()
}

// CheckSkills is the exported wrapper around checkSkills.
func CheckSkills(root string) []Status {
	return checkSkills(root)
}

// wireProjectSkills installs kern skills into .agents/skills/ (universal open spec)
// and into any present agent-specific project directories (.claude/skills, .cursor/rules, etc.).
func wireProjectSkills(root string) []Status {
	var statuses []Status

	// 1. Universal open agent skills (.agents/skills) — always installed
	targetDir := filepath.Join(root, ".agents", "skills")
	count, err := installSkillsToDir(targetDir)
	if err != nil {
		statuses = append(statuses, Status{Agent: "skills (project universal)", Path: targetDir, Note: err.Error()})
	} else if count == 0 {
		statuses = append(statuses, Status{Agent: "skills (project universal)", Installed: true, Path: targetDir, Note: "skills already up to date"})
	} else {
		statuses = append(statuses, Status{Agent: "skills (project universal)", Installed: true, Path: targetDir, Note: "installed/updated kern skills in .agents/skills"})
	}

	// 2. Claude Code project skills (.claude/skills) if .claude exists
	claudeDir := filepath.Join(root, ".claude")
	if _, err := os.Stat(claudeDir); err == nil {
		cTarget := filepath.Join(claudeDir, "skills")
		cCount, cErr := installSkillsToDir(cTarget)
		if cErr == nil {
			note := "claude skills up to date"
			if cCount > 0 {
				note = "installed/updated kern skills in .claude/skills"
			}
			statuses = append(statuses, Status{Agent: "skills-claude (project)", Installed: true, Path: cTarget, Note: note})
		}
	}

	// 3. Cursor project rules (.cursor/rules/*.mdc) if .cursor exists
	cursorDir := filepath.Join(root, ".cursor")
	if _, err := os.Stat(cursorDir); err == nil {
		rulesDir := filepath.Join(cursorDir, "rules")
		curCount, curErr := installCursorRulesToDir(rulesDir)
		if curErr == nil {
			note := "cursor skill rules up to date"
			if curCount > 0 {
				note = "installed/updated kern skill rules in .cursor/rules"
			}
			statuses = append(statuses, Status{Agent: "skills-cursor (project)", Installed: true, Path: rulesDir, Note: note})
		}
	}

	// 4. OpenCode project skills (.opencode/skills) if opencode.json or .opencode exists
	opencodeDir := filepath.Join(root, ".opencode")
	opencodeJSON := filepath.Join(root, "opencode.json")
	if _, err1 := os.Stat(opencodeDir); err1 == nil {
		opTarget := filepath.Join(opencodeDir, "skills")
		_, _ = installSkillsToDir(opTarget)
	} else if _, err2 := os.Stat(opencodeJSON); err2 == nil {
		opTarget := filepath.Join(opencodeDir, "skills")
		_, _ = installSkillsToDir(opTarget)
	}

	// 5. Codex project skills (.codex/skills) if .codex exists
	codexDir := filepath.Join(root, ".codex")
	if _, err := os.Stat(codexDir); err == nil {
		codexTarget := filepath.Join(codexDir, "skills")
		_, _ = installSkillsToDir(codexTarget)
	}

	// 6. Copilot project skills (.github/skills) if .github exists
	githubDir := filepath.Join(root, ".github")
	if _, err := os.Stat(githubDir); err == nil {
		copilotTarget := filepath.Join(githubDir, "skills")
		_, _ = installSkillsToDir(copilotTarget)
	}

	return statuses
}

// globalSkillLocation describes a home directory check and target skills directory for an agent.
type globalSkillLocation struct {
	agent        string
	homeDirCheck string // relative to globalHomeDir()
	skillsDir    string // relative to globalHomeDir()
	isCursor     bool
}

var globalSkillTargets = []globalSkillLocation{
	{agent: "skills-antigravity (global)", homeDirCheck: ".gemini", skillsDir: filepath.Join(".gemini", "config", "skills")},
	{agent: "skills-claude (global)", homeDirCheck: ".claude", skillsDir: filepath.Join(".claude", "skills")},
	{agent: "skills-cursor (global)", homeDirCheck: ".cursor", skillsDir: filepath.Join(".cursor", "rules"), isCursor: true},
	{agent: "skills-opencode (global)", homeDirCheck: filepath.Join(".config", "opencode"), skillsDir: filepath.Join(".config", "opencode", "skills")},
	{agent: "skills-codex (global)", homeDirCheck: ".codex", skillsDir: filepath.Join(".codex", "skills")},
	{agent: "skills-qwen (global)", homeDirCheck: ".qwen", skillsDir: filepath.Join(".qwen", "skills")},
	{agent: "skills-qoder (global)", homeDirCheck: ".qoder", skillsDir: filepath.Join(".qoder", "skills")},
	{agent: "skills-copilot (global)", homeDirCheck: ".copilot", skillsDir: filepath.Join(".copilot", "skills")},
}

// wireGlobalSkills installs the bundled kern skills into all detected agent global directories.
func wireGlobalSkills() []Status {
	var statuses []Status
	home := globalHomeDir()

	for _, target := range globalSkillTargets {
		checkPath := filepath.Join(home, target.homeDirCheck)
		if _, err := os.Stat(checkPath); err != nil {
			statuses = append(statuses, Status{Agent: target.agent, Skipped: true, Path: checkPath, Note: target.homeDirCheck + " not present — skipped"})
			continue
		}

		targetPath := filepath.Join(home, target.skillsDir)
		var count int
		var err error
		if target.isCursor {
			count, err = installCursorRulesToDir(targetPath)
		} else {
			count, err = installSkillsToDir(targetPath)
		}

		if err != nil {
			statuses = append(statuses, Status{Agent: target.agent, Path: targetPath, Note: err.Error()})
			continue
		}

		note := "global skills already up to date"
		if count > 0 {
			note = "installed/updated global kern skills in " + target.skillsDir
		}
		statuses = append(statuses, Status{Agent: target.agent, Installed: true, Path: targetPath, Note: note})
	}

	return statuses
}

// installSkillsToDir copies the embedded skills into destDir.
func installSkillsToDir(destDir string) (int, error) {
	updated := 0
	for _, skill := range skills.SkillNames {
		data, err := skills.ReadSkill(skill)
		if err != nil {
			return 0, err
		}

		skillDir := filepath.Join(destDir, skill)
		if err := os.MkdirAll(skillDir, 0o755); err != nil {
			return 0, err
		}

		targetFile := filepath.Join(skillDir, "SKILL.md")
		writtenFile := false
		if existing, err := os.ReadFile(targetFile); err != nil || !bytes.Equal(existing, data) {
			if err := os.WriteFile(targetFile, data, 0o644); err != nil {
				return 0, err
			}
			updated++
			writtenFile = true
		}

		// Copy scripts if present
		scriptNames, err := skills.ListSkillScripts(skill)
		if err == nil && len(scriptNames) > 0 {
			targetScriptsDir := filepath.Join(skillDir, "scripts")
			if err := os.MkdirAll(targetScriptsDir, 0o755); err == nil {
				for _, sName := range scriptNames {
					sData, err := skills.ReadSkillScript(skill, sName)
					if err == nil {
						targetScript := filepath.Join(targetScriptsDir, sName)
						if existing, err := os.ReadFile(targetScript); err == nil && bytes.Equal(existing, sData) {
							continue
						}
						if err := os.WriteFile(targetScript, sData, 0o755); err == nil && !writtenFile {
							updated++
						}
					}
				}
			}
		}
	}
	return updated, nil
}

// installCursorRulesToDir converts skills to Cursor .mdc rules and writes them to destDir.
func installCursorRulesToDir(destDir string) (int, error) {
	updated := 0
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return 0, err
	}
	for _, skill := range skills.SkillNames {
		data, err := skills.ReadSkill(skill)
		if err != nil {
			return 0, err
		}
		mdcContent := makeCursorMdc(skill, data)
		targetFile := filepath.Join(destDir, skill+".mdc")
		if existing, err := os.ReadFile(targetFile); err == nil {
			if bytes.Equal(existing, mdcContent) {
				continue
			}
		}
		if err := os.WriteFile(targetFile, mdcContent, 0o644); err != nil {
			return 0, err
		}
		updated++
	}
	return updated, nil
}

func makeCursorMdc(skillName string, data []byte) []byte {
	desc, body := skills.ExtractDescriptionAndBody(data)
	var buf bytes.Buffer
	buf.WriteString("---\n")
	buf.WriteString("description: " + desc + "\n")
	buf.WriteString("globs:\nalwaysApply: false\n---\n")
	buf.WriteString("<!-- kern-skill: generated by kern setup -->\n\n")
	buf.WriteString(body)
	return buf.Bytes()
}

// checkSkills reports whether project and global skills are present.
func checkSkills(root string) []Status {
	var statuses []Status
	projectSkills := filepath.Join(root, ".agents", "skills")
	pStatus := Status{Agent: "skills (project)", Path: projectSkills}
	if isSkillsPopulated(projectSkills) {
		pStatus.Installed = true
		pStatus.Note = "kern skills present"
	} else {
		pStatus.Note = "skills not installed"
	}
	statuses = append(statuses, pStatus)

	home := globalHomeDir()
	for _, target := range globalSkillTargets {
		checkPath := filepath.Join(home, target.homeDirCheck)
		if _, err := os.Stat(checkPath); err != nil {
			continue // skip reporting if agent not installed
		}
		targetPath := filepath.Join(home, target.skillsDir)
		gStatus := Status{Agent: target.agent, Path: targetPath}
		if target.isCursor {
			if isCursorRulesPopulated(targetPath) {
				gStatus.Installed = true
				gStatus.Note = "global kern rules present"
			} else {
				gStatus.Note = "rules not installed"
			}
		} else {
			if isSkillsPopulated(targetPath) {
				gStatus.Installed = true
				gStatus.Note = "global kern skills present"
			} else {
				gStatus.Note = "skills not installed"
			}
		}
		statuses = append(statuses, gStatus)
	}

	return statuses
}

func isSkillsPopulated(dir string) bool {
	for _, skill := range skills.SkillNames {
		target := filepath.Join(dir, skill, "SKILL.md")
		if _, err := os.Stat(target); err != nil {
			return false
		}
	}
	return true
}

func isCursorRulesPopulated(dir string) bool {
	for _, skill := range skills.SkillNames {
		target := filepath.Join(dir, skill+".mdc")
		if _, err := os.Stat(target); err != nil {
			return false
		}
	}
	return true
}
