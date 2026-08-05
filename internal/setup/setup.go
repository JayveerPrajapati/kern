// Package setup wires kern into code agents with a single command.
//
// It writes the standard project-level .mcp.json (auto-discovered by Claude
// Code, Cursor, Windsurf and most MCP-compatible agents), the opencode
// project/global MCP config, the opencode plugin, and the AGENTS.md usage
// rules. All operations are idempotent: existing entries are left untouched.
package setup

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

//go:embed assets/plugin/kern.ts
var pluginFS embed.FS

//go:embed assets/AGENTS.md
var rulesFS embed.FS

// Status reports one agent's wiring state.
type Status struct {
	Agent     string
	Installed bool
	Path      string
	Note      string
}

// Bin returns the absolute path to the kern-mcp binary that ships next to the
// running executable, falling back to the bare "kern-mcp" command.
func Bin() string {
	exe, err := os.Executable()
	if err != nil {
		return "kern-mcp"
	}
	abs := filepath.Join(filepath.Dir(exe), "kern-mcp")
	if _, err := os.Stat(abs); err == nil {
		return abs
	}
	return "kern-mcp"
}

// adapter describes a JSON-config agent: where its config lives, which key
// holds servers, and the entry shape.
type adapter struct {
	name  string
	path  func(root string) string
	key   string
	entry func(bin string) map[string]any
}

// adapters is the registry of JSON-config agents supported by setup.
var adapters = []adapter{
	{name: "continue", path: globalConfig("continue", "config.json"), key: "mcpServers", entry: stdioEntry},
	{name: "windsurf", path: globalConfig(".codeium", "windsurf", "mcp_config.json"), key: "mcpServers", entry: stdioEntry},
	{name: "zed", path: globalConfig("zed", "settings.json"), key: "context_servers", entry: cmdEntry},
	{name: "vscode", path: projectConfig(".vscode", "mcp.json"), key: "servers", entry: stdioEntry},
	{name: "cursor", path: projectConfig(".cursor", "mcp.json"), key: "mcpServers", entry: stdioEntry},
	{name: "gemini", path: projectConfig(".gemini", "settings.json"), key: "mcpServers", entry: stdioEntry},
	{name: "antigravity", path: globalConfig(".gemini", "antigravity", "mcp_config.json"), key: "mcpServers", entry: stdioEntry},
	{name: "qwen", path: homeConfig(".qwen", "settings.json"), key: "mcpServers", entry: stdioEntry},
	{name: "qoder", path: homeConfig(".qoder", "mcp.json"), key: "mcpServers", entry: stdioEntry},
	{name: "kiro", path: projectConfig(".kiro", "settings", "mcp.json"), key: "mcpServers", entry: stdioEntry},
	{name: "copilot", path: projectConfig(".vscode", "mcp.json"), key: "servers", entry: stdioEntry},
	{name: "copilot-cli", path: globalConfig(".copilot", "mcp-config.json"), key: "mcpServers", entry: stdioEntry},
}

func stdioEntry(bin string) map[string]any {
	return map[string]any{"type": "stdio", "command": bin, "args": []string{}}
}

func cmdEntry(bin string) map[string]any {
	return map[string]any{"command": bin, "args": []string{}}
}

// globalConfig resolves a path under the user config dir.
func globalConfig(sub ...string) func(string) string {
	return func(_ string) string {
		base := os.Getenv("XDG_CONFIG_HOME")
		if base == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				home = "."
			}
			base = filepath.Join(home, ".config")
		}
		return filepath.Join(append([]string{base}, sub...)...)
	}
}

// projectConfig resolves a path inside the project root.
func projectConfig(sub ...string) func(string) string {
	return func(root string) string {
		return filepath.Join(append([]string{root}, sub...)...)
	}
}

// homeConfig resolves a path directly under the user home dir (used by agents
// that keep their config at ~/.name rather than under ~/.config).
func homeConfig(sub ...string) func(string) string {
	return func(_ string) string {
		home, err := os.UserHomeDir()
		if err != nil {
			home = "."
		}
		return filepath.Join(append([]string{home}, sub...)...)
	}
}

// Check reports the current wiring state without changing anything.
func Check(root string) []Status {
	out := []Status{
		fileStatus(filepath.Join(root, ".mcp.json"), "mcp (project .mcp.json)"),
		fileStatus(filepath.Join(root, "opencode.json"), "opencode (project)"),
		fileStatus(filepath.Join(root, ".opencode", "plugins", "kern.ts"), "opencode plugin"),
		fileStatus(filepath.Join(root, "AGENTS.md"), "AGENTS.md rules"),
		fileStatus(globalOpencodePath(), "opencode (global config)"),
	}
	for _, a := range adapters {
		out = append(out, fileStatus(a.path(root), a.name))
	}
	out = append(out, claudeStatus())
	out = append(out, codexStatus())
	return out
}

// Wire configures the requested agents. An empty agents list means all.
func Wire(root string, agents []string) []Status {
	bin := Bin()
	enabled := func(name string) bool {
		if len(agents) == 0 {
			return true
		}
		for _, a := range agents {
			if a == name {
				return true
			}
		}
		return false
	}
	var out []Status
	if enabled("mcp") {
		out = append(out, wireMCPJSON(root, bin))
	}
	if enabled("opencode") {
		out = append(out, wireOpencode(root, bin))
		out = append(out, wirePlugin(root))
		out = append(out, wireAgentRules(root))
		out = append(out, wireGlobal(bin))
	}
	if enabled("claude") {
		out = append(out, wireClaude(bin))
	}
	if enabled("codex") {
		out = append(out, wireCodex(bin))
	}
	for _, a := range adapters {
		if enabled(a.name) {
			out = append(out, wireAdapter(a, root, bin))
		}
	}
	return out
}

func wireAdapter(a adapter, root, bin string) Status {
	path := a.path(root)
	err := mergeJSON(path, a.key, a.entry(bin))
	if err != nil {
		return Status{Agent: a.name, Path: path, Note: err.Error()}
	}
	return Status{Agent: a.name, Installed: true, Path: path, Note: a.name + " config updated"}
}

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
	err := mergeJSON(path, "mcp", map[string]any{
		"type":    "local",
		"command": []string{bin},
		"enabled": true,
	})
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
	if err := os.WriteFile(path, src, 0o644); err != nil {
		return Status{Agent: "opencode-plugin", Path: path, Note: err.Error()}
	}
	return Status{Agent: "opencode-plugin", Installed: true, Path: path, Note: "plugin installed"}
}

func wireAgentRules(root string) Status {
	path := filepath.Join(root, "AGENTS.md")
	content := ""
	if b, err := os.ReadFile(path); err == nil {
		content = string(b)
	}
	if strings.Contains(content, "kern usage rules") {
		return Status{Agent: "AGENTS.md", Installed: true, Path: path, Note: "rules already present"}
	}
	rules, err := rulesFS.ReadFile("assets/AGENTS.md")
	if err != nil {
		return Status{Agent: "AGENTS.md", Path: path, Note: err.Error()}
	}
	var joined string
	if content == "" {
		joined = string(rules)
	} else {
		joined = strings.TrimRight(content, "\n") + "\n\n" + string(rules)
	}
	if err := os.WriteFile(path, []byte(joined), 0o644); err != nil {
		return Status{Agent: "AGENTS.md", Path: path, Note: err.Error()}
	}
	return Status{Agent: "AGENTS.md", Installed: true, Path: path, Note: "rules appended"}
}

func wireClaude(bin string) Status {
	path, err := exec.LookPath("claude")
	if err != nil {
		return Status{Agent: "claude", Note: "claude not on PATH — project .mcp.json still covers Claude Code"}
	}
	cmd := exec.Command(path, "mcp", "add", "kern", "--", bin)
	if out, err := cmd.CombinedOutput(); err != nil {
		return Status{Agent: "claude", Path: path, Note: fmt.Sprintf("claude mcp add failed: %s", strings.TrimSpace(string(out)))}
	}
	return Status{Agent: "claude", Installed: true, Path: path, Note: "claude mcp add ok"}
}

// wireCodex writes the Codex MCP server entry into ~/.codex/config.toml.
// Codex uses TOML rather than JSON, so it gets a small dedicated writer
// instead of the JSON adapters.
func wireCodex(bin string) Status {
	home, err := os.UserHomeDir()
	if err != nil {
		return Status{Agent: "codex", Note: err.Error()}
	}
	path := filepath.Join(home, ".codex", "config.toml")
	needle := "[mcp_servers.kern]"
	if b, err := os.ReadFile(path); err == nil && strings.Contains(string(b), needle) {
		return Status{Agent: "codex", Installed: true, Path: path, Note: "codex config already registers kern"}
	}
	entry := "\n[mcp_servers.kern]\ncommand = \"" + strings.ReplaceAll(bin, `\`, `\\`) + "\"\n"
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Status{Agent: "codex", Path: path, Note: err.Error()}
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return Status{Agent: "codex", Path: path, Note: err.Error()}
	}
	defer f.Close()
	if _, err := f.WriteString(entry); err != nil {
		return Status{Agent: "codex", Path: path, Note: err.Error()}
	}
	return Status{Agent: "codex", Installed: true, Path: path, Note: "codex config updated"}
}

func codexStatus() Status {
	home, err := os.UserHomeDir()
	if err != nil {
		return Status{Agent: "codex", Note: err.Error()}
	}
	path := filepath.Join(home, ".codex", "config.toml")
	if b, err := os.ReadFile(path); err == nil && strings.Contains(string(b), "[mcp_servers.kern]") {
		return Status{Agent: "codex", Installed: true, Path: path, Note: "kern MCP registered"}
	}
	return Status{Agent: "codex", Path: path, Note: "codex config.toml has no kern MCP"}
}

// mergeJSON reads a JSON file, inserts entry under key unless "kern" already
// exists there, and writes it back. Missing or empty files are created.
func mergeJSON(path, key string, entry map[string]any) error {
	var m map[string]any
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := json.Unmarshal(data, &m); err != nil {
			return fmt.Errorf("%s is not valid JSON: %w", path, err)
		}
	case errors.Is(err, os.ErrNotExist):
		m = map[string]any{}
	default:
		return err
	}
	if m == nil {
		m = map[string]any{}
	}
	existing, _ := m[key].(map[string]any)
	if existing == nil {
		existing = map[string]any{}
	}
	if _, ok := existing["kern"]; !ok {
		existing["kern"] = entry
	}
	m[key] = existing
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err = json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func fileStatus(path, label string) Status {
	b, err := os.ReadFile(path)
	if err != nil {
		return Status{Agent: label, Path: path, Note: "not present"}
	}
	installed := strings.Contains(string(b), "kern")
	note := "kern entry present"
	if !installed {
		note = "file exists but no kern entry"
	}
	return Status{Agent: label, Installed: installed, Path: path, Note: note}
}

func claudeStatus() Status {
	path, err := exec.LookPath("claude")
	if err != nil {
		return Status{Agent: "claude", Note: "not installed"}
	}
	if b, err := os.ReadFile(claudeConfigPath()); err == nil && strings.Contains(string(b), `"kern"`) {
		return Status{Agent: "claude", Installed: true, Path: path, Note: "kern MCP registered (project or user scope)"}
	}
	return Status{Agent: "claude", Path: path, Note: "claude available — run kern setup to add kern MCP"}
}

func claudeConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude.json")
}

func globalOpencodePath() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "opencode", "opencode.jsonc")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "opencode", "opencode.jsonc")
}
