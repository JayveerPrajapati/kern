package setup

import (
	"bytes"
	"os"
	"path/filepath"
)

func wireMCPJSON(root, bin string) Status {
	path := filepath.Join(root, ".mcp.json")
	err := mergeJSON(path, "mcpServers", map[string]any{
		"command": bin,
		"args":    []string{},
	})
	if err != nil {
		return Status{Agent: "mcp", Path: path, Note: err.Error()}
	}
	return Status{Agent: "mcp", Installed: true, Path: path, Note: "project .mcp.json written (auto-discovered by Claude Code, Cursor, Windsurf, …)"}
}

func wireOpencode(root, bin string) Status {
	path := filepath.Join(root, "opencode.json")
	// For the project-level config, prefer the portable relative path "bin/kern-mcp"
	// so it works on any machine after a build. Only fall back to the absolute
	// bin when the relative binary is not present next to the project root.
	cmd := "bin/kern-mcp"
	if _, err := os.Stat(filepath.Join(root, cmd)); err != nil {
		cmd = bin
	}
	entry := map[string]any{
		"type":    "local",
		"command": []string{cmd},
		"enabled": true,
	}
	if cmd == "bin/kern-mcp" {
		entry["cwd"] = "."
	}
	err := mergeJSON(path, "mcp", entry)
	if err != nil {
		return Status{Agent: "opencode", Path: path, Note: err.Error()}
	}
	return Status{Agent: "opencode", Installed: true, Path: path, Note: "opencode.json kern MCP entry present"}
}

func wireGlobal(bin string) Status {
	path := globalOpencodePath()
	err := mergeJSON(path, "mcp", map[string]any{
		"type":    "local",
		"command": []string{bin},
		"enabled": true,
	})
	if err != nil {
		return Status{Agent: "opencode-global", Path: path, Note: err.Error()}
	}
	return Status{Agent: "opencode-global", Installed: true, Path: path, Note: "global opencode MCP entry present"}
}

func wirePlugin(root string) Status {
	dir := filepath.Join(root, ".opencode", "plugins")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Status{Agent: "opencode-plugin", Note: err.Error()}
	}
	path := filepath.Join(dir, "kern.ts")
	src, err := pluginFS.ReadFile("assets/plugin/kern.ts")
	if err != nil {
		return Status{Agent: "opencode-plugin", Path: path, Note: err.Error()}
	}
	// Compare-and-write so re-running setup never torches user edits.
	if cur, rerr := os.ReadFile(path); rerr == nil {
		if bytes.Equal(cur, src) {
			return Status{Agent: "opencode-plugin", Installed: true, Path: path, Note: "plugin already current"}
		}
		return Status{Agent: "opencode-plugin", Path: path, Note: "plugin is customized — left untouched"}
	}
	if err := os.WriteFile(path, src, 0o644); err != nil {
		return Status{Agent: "opencode-plugin", Path: path, Note: err.Error()}
	}
	return Status{Agent: "opencode-plugin", Installed: true, Path: path, Note: "plugin installed"}
}

// hostRuleFiles are the per-agent rule files that peer agents read directly
// (Claude Code: CLAUDE.md, Gemini/CodeBuddy: GEMINI.md). AGENTS.md is the
// universal source; the same single-source block is instantiated into these
// host files so every agent sees the rules, but only when the file already
// exists — setup never creates new rule files unprompted.
var hostRuleFiles = []string{"CLAUDE.md", "GEMINI.md"}

func wireAgentRules(root string) Status {
	status := wireRulesFile(root, "AGENTS.md")
	// Same content, per host. Errors here are informational: the universal
	// AGENTS.md is the primary delivery mechanism.
	for _, name := range hostRuleFiles {
		path := filepath.Join(root, name)
		if _, err := os.Stat(path); err != nil {
			continue
		}
		wireRulesFile(root, name)
	}
	return status
}
