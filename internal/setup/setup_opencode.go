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
		// No KERN_ALLOW_EXEC default: exec is opt-in per host. The shipped
		// .mcp.json must not silently enable arbitrary command execution for
		// every agent that opens the repo; governance.CheckExec already fails
		// closed without it, and the approval workflow still gates.
		"env": map[string]string{},
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
		// No KERN_ALLOW_EXEC default (C1): exec is opt-in per host, matching
		// wireMCPJSON's contract — `kern setup` must not silently enable
		// arbitrary command execution for every agent session in the repo.
		// governance.CheckExec fails closed without it, and hosts that want
		// exec set KERN_ALLOW_EXEC=1 themselves. mergeJSON replaces the kern
		// entry wholesale, so re-running setup also removes a stale
		// KERN_ALLOW_EXEC=1 written by an older kern.
		"environment": map[string]string{},
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
		// No KERN_ALLOW_EXEC default (C1): same opt-in contract as the
		// project-level wiring — the global config is machine-wide, so
		// silently enabling exec there would be even broader.
		"environment": map[string]string{},
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
		// F-DR1: a copy matching a previously SHIPPED version is kern's own
		// deployment (from an older kern), not user customization — safe and
		// necessary to update. Only an unrecognized copy is treated as
		// customized and left untouched.
		if isShippedPluginVersion(cur) {
			if err := os.WriteFile(path, src, 0o644); err != nil {
				return Status{Agent: "opencode-plugin", Path: path, Note: err.Error()}
			}
			return Status{Agent: "opencode-plugin", Installed: true, Path: path, Note: "plugin updated from an older kern version (was a previously shipped copy)"}
		}
		// Customized copies are intentionally left untouched; the plugin IS
		// deployed, so this is a note, not a setup failure.
		return Status{Agent: "opencode-plugin", Installed: true, Path: path, Note: "plugin is customized — left untouched"}
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
// to the embedded asset are left alone, previously-shipped copies are updated
// (F-DR1), and user-customized copies are never overwritten.
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
			// F-DR1: a previously shipped version is kern's own deployment
			// (from an older kern) — update it instead of leaving doctor's
			// prescribed remedy unable to converge. Only an unrecognized copy
			// is treated as user-customized and never overwritten.
			if isShippedPluginVersion(cur) {
				if err := os.WriteFile(path, src, 0o644); err != nil {
					notes = append(notes, tildePath(path)+": "+err.Error())
					continue
				}
				notes = append(notes, tildePath(path)+" updated from an older kern version")
				installed = true
				continue
			}
			// Customized copy present: deployed, deliberately not overwritten.
			notes = append(notes, tildePath(path)+" customized — left untouched")
			installed = true
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

// wireAgentRules writes the kern usage rules to the universal repo AGENTS.md
// (and any existing per-host rule files). mode selects the AGENTS.md variant:
// "thin" (default) writes the thin wiring-only file (full rules live in the
// host's global instructions, managed by `kern setup --global-rules`);
// anything else writes the full rules. wired names the agents this run wired,
// used only by the thin variant's wiring-facts line. explicitThin marks a
// deliberate thin choice (--agents-md thin or a persisted preference); the
// default never downgrades an existing full rules file to thin.
func wireAgentRules(root, mode, wired string, explicitThin bool) Status {
	if mode == "thin" {
		status := wireThinAgentRules(root, wired, explicitThin)
		// Same thin content, per host. Errors here are informational: the
		// universal AGENTS.md is the primary delivery mechanism.
		for _, name := range hostRuleFiles {
			path := filepath.Join(root, name)
			if _, err := os.Stat(path); err != nil {
				continue
			}
			wireThinRulesFile(root, name, wired, explicitThin)
		}
		return status
	}
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
