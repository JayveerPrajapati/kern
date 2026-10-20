package agentwire

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"strings"

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

// WireProjectSkills installs kern skills into .agents/skills/ (universal open spec)
// and into any present agent-specific project directories (.claude/skills, .cursor/rules, etc.).
func WireProjectSkills(root string) []Status {
	var statuses []Status

	// 1. Universal open agent skills (.agents/skills) — always installed
	targetDir := filepath.Join(root, ".agents", "skills")
	count, err := InstallSkillsToDir(targetDir)
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
		cCount, cErr := InstallSkillsToDir(cTarget)
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
		curCount, curErr := InstallCursorRulesToDir(rulesDir)
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
		if _, err := InstallSkillsToDir(opTarget); err != nil {
			log.Printf("WARNING: failed to install OpenCode skills into %s: %v", opTarget, err)
		}
	} else if _, err2 := os.Stat(opencodeJSON); err2 == nil {
		opTarget := filepath.Join(opencodeDir, "skills")
		if _, err := InstallSkillsToDir(opTarget); err != nil {
			log.Printf("WARNING: failed to install OpenCode skills into %s: %v", opTarget, err)
		}
	}

	// 5. Codex project skills (.codex/skills) if .codex exists
	codexDir := filepath.Join(root, ".codex")
	if _, err := os.Stat(codexDir); err == nil {
		codexTarget := filepath.Join(codexDir, "skills")
		if _, err := InstallSkillsToDir(codexTarget); err != nil {
			log.Printf("WARNING: failed to install Codex skills into %s: %v", codexTarget, err)
		}
	}

	// 6. Copilot project skills (.github/skills) if .github exists
	githubDir := filepath.Join(root, ".github")
	if _, err := os.Stat(githubDir); err == nil {
		copilotTarget := filepath.Join(githubDir, "skills")
		if _, err := InstallSkillsToDir(copilotTarget); err != nil {
			log.Printf("WARNING: failed to install Copilot skills into %s: %v", copilotTarget, err)
		}
	}

	return statuses
}

// GlobalSkillLocation describes a home directory check and target skills directory for an agent.
type GlobalSkillLocation struct {
	Agent        string
	HomeDirCheck string // relative to GlobalHomeDir()
	SkillsDir    string // relative to GlobalHomeDir()
	IsCursor     bool
}

// GlobalSkillTargets lists the agent global directories that receive skills.
var GlobalSkillTargets = []GlobalSkillLocation{
	{Agent: "skills-antigravity (global)", HomeDirCheck: ".gemini", SkillsDir: filepath.Join(".gemini", "config", "skills")},
	{Agent: "skills-claude (global)", HomeDirCheck: ".claude", SkillsDir: filepath.Join(".claude", "skills")},
	{Agent: "skills-cursor (global)", HomeDirCheck: ".cursor", SkillsDir: filepath.Join(".cursor", "rules"), IsCursor: true},
	{Agent: "skills-opencode (global)", HomeDirCheck: filepath.Join(".config", "opencode"), SkillsDir: filepath.Join(".config", "opencode", "skills")},
	{Agent: "skills-opencode (global ~/.opencode)", HomeDirCheck: ".opencode", SkillsDir: filepath.Join(".opencode", "skills")},
	{Agent: "skills-agents (global)", HomeDirCheck: ".agents", SkillsDir: filepath.Join(".agents", "skills")},
	{Agent: "skills-codex (global)", HomeDirCheck: ".codex", SkillsDir: filepath.Join(".codex", "skills")},
	{Agent: "skills-qwen (global)", HomeDirCheck: ".qwen", SkillsDir: filepath.Join(".qwen", "skills")},
	{Agent: "skills-qoder (global)", HomeDirCheck: ".qoder", SkillsDir: filepath.Join(".qoder", "skills")},
	{Agent: "skills-copilot (global)", HomeDirCheck: ".copilot", SkillsDir: filepath.Join(".copilot", "skills")},
}

// WireGlobalSkills installs the bundled kern skills into all detected agent global directories.
func WireGlobalSkills() []Status {
	var statuses []Status
	home := GlobalHomeDir()

	for _, target := range GlobalSkillTargets {
		checkPath := filepath.Join(home, target.HomeDirCheck)
		if _, err := os.Stat(checkPath); err != nil {
			statuses = append(statuses, Status{Agent: target.Agent, Skipped: true, Path: checkPath, Note: target.HomeDirCheck + " not present — skipped"})
			continue
		}

		targetPath := filepath.Join(home, target.SkillsDir)
		var count int
		var err error
		if target.IsCursor {
			count, err = InstallCursorRulesToDir(targetPath)
		} else {
			count, err = InstallSkillsToDir(targetPath)
		}

		if err != nil {
			statuses = append(statuses, Status{Agent: target.Agent, Path: targetPath, Note: err.Error()})
			continue
		}

		note := "global skills already up to date"
		if count > 0 {
			note = "installed/updated global kern skills in " + target.SkillsDir
		}
		statuses = append(statuses, Status{Agent: target.Agent, Installed: true, Path: targetPath, Note: note})
	}

	return statuses
}

// InstallSkillsToDir copies the embedded skills into destDir.
func InstallSkillsToDir(destDir string) (int, error) {
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

// InstallCursorRulesToDir converts skills to Cursor .mdc rules and writes them to destDir.
func InstallCursorRulesToDir(destDir string) (int, error) {
	updated := 0
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return 0, err
	}
	for _, skill := range skills.SkillNames {
		data, err := skills.ReadSkill(skill)
		if err != nil {
			return 0, err
		}
		mdcContent := MakeCursorMdc(skill, data)
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

// MakeCursorMdc renders a skill's description + body as a Cursor .mdc rule.
func MakeCursorMdc(skillName string, data []byte) []byte {
	desc, body := skills.ExtractDescriptionAndBody(data)
	var buf bytes.Buffer
	buf.WriteString("---\n")
	buf.WriteString("description: " + yamlScalar(desc) + "\n")
	buf.WriteString("globs:\nalwaysApply: false\n---\n")
	buf.WriteString("<!-- kern-skill: generated by kern setup -->\n\n")
	buf.WriteString(body)
	return buf.Bytes()
}

// yamlScalar renders s as a valid YAML single-quoted scalar, doubling embedded
// single quotes per the YAML spec (” inside '…'). Descriptions routinely
// contain colons, apostrophes, and leading dashes; unquoted emission would
// produce frontmatter that strict YAML parsers reject.
func yamlScalar(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// CheckSkills reports whether project and global skills are present.
func CheckSkills(root string) []Status {
	var statuses []Status
	projectSkills := filepath.Join(root, ".agents", "skills")
	pStatus := Status{Agent: "skills (project)", Path: projectSkills}
	if IsSkillsPopulated(projectSkills) {
		pStatus.Installed = true
		pStatus.Note = "kern skills present"
	} else {
		pStatus.Note = "skills not installed"
	}
	statuses = append(statuses, pStatus)

	home := GlobalHomeDir()
	for _, target := range GlobalSkillTargets {
		checkPath := filepath.Join(home, target.HomeDirCheck)
		if _, err := os.Stat(checkPath); err != nil {
			continue // skip reporting if agent not installed
		}
		targetPath := filepath.Join(home, target.SkillsDir)
		gStatus := Status{Agent: target.Agent, Path: targetPath}
		if target.IsCursor {
			if IsCursorRulesPopulated(targetPath) {
				gStatus.Installed = true
				gStatus.Note = "global kern rules present"
			} else {
				gStatus.Note = "rules not installed"
			}
		} else {
			if IsSkillsPopulated(targetPath) {
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

// IsSkillsPopulated reports whether every bundled skill's SKILL.md exists under dir.
func IsSkillsPopulated(dir string) bool {
	for _, skill := range skills.SkillNames {
		target := filepath.Join(dir, skill, "SKILL.md")
		if _, err := os.Stat(target); err != nil {
			return false
		}
	}
	return true
}

// IsCursorRulesPopulated reports whether every bundled skill's .mdc rule exists under dir.
func IsCursorRulesPopulated(dir string) bool {
	for _, skill := range skills.SkillNames {
		target := filepath.Join(dir, skill+".mdc")
		if _, err := os.Stat(target); err != nil {
			return false
		}
	}
	return true
}
