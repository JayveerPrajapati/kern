package setup

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
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

func wireOpencode(root string) Status {
	path := filepath.Join(root, "opencode.json")
	// PortableMCPCommand returns bare "kern-mcp" when it is on PATH (the agent
	// re-resolves it at launch, so the config survives relocation) and falls
	// back to the absolute sibling Bin() path otherwise. It never emits the
	// fragile "bin/kern-mcp" relative path, which breaks agents whenever the
	// repo has no bin/kern-mcp binary.
	cmd := PortableMCPCommand()
	entry := map[string]any{
		"type":    "local",
		"command": []string{cmd},
		"enabled": true,
		// No cwd field: opencode resolves opencode.json from the project root
		// and launches MCP servers with cwd = project root by default. Writing
		// an absolute cwd here would leak a machine-specific path into a
		// committed config file, breaking portability.
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

// wireGlobalPlugin installs the opencode plugin into every global plugin
// location (GlobalPluginPaths: ~/.config/opencode/plugins and ~/.opencode/
// plugins). opencode 1.18.x loads plugins from the config root
// (~/.opencode/plugins), where a stale copy silently wins over the project
// one — installing both keeps every copy in sync with the embedded asset.
// Uses the same compare-and-write semantics as wirePlugin: copies identical
// to the embedded asset are left alone and user-customized copies are never
// overwritten.
func wireGlobalPlugin() Status {
	src, err := pluginFS.ReadFile("assets/plugin/kern.ts")
	if err != nil {
		return Status{Agent: "opencode-plugin-global", Note: err.Error()}
	}
	var notes []string
	installed := false
	for _, path := range GlobalPluginPaths() {
		if cur, rerr := os.ReadFile(path); rerr == nil {
			if bytes.Equal(cur, src) {
				notes = append(notes, tildePath(path)+" current")
				installed = true
				continue
			}
			notes = append(notes, tildePath(path)+" customized — left untouched")
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			notes = append(notes, tildePath(path)+": "+err.Error())
			continue
		}
		if err := os.WriteFile(path, src, 0o644); err != nil {
			notes = append(notes, tildePath(path)+": "+err.Error())
			continue
		}
		notes = append(notes, tildePath(path)+" installed")
		installed = true
	}
	return Status{Agent: "opencode-plugin-global", Installed: installed, Note: strings.Join(notes, "; ")}
}

// tildePath renders an absolute path with the home directory abbreviated as
// "~" for human-readable status notes.
func tildePath(p string) string {
	if h, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, h) {
		return filepath.Join("~", strings.TrimPrefix(p, h))
	}
	return p
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
