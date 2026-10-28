package setup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// LaunchIntegrity audits the MCP command that MACHINE-GLOBAL agent configs
// actually contain. The failure it exists to catch is silent: `kern setup` run
// from a terminal used to write bare "kern-mcp", which resolves in that shell
// but NOT in a GUI-launched host (Claude Code, Cursor, Gemini, Qwen, Codex,
// Zed), whose PATH is the minimal launchd PATH. The MCP server then never
// starts, no kern_* tools appear, and the agent silently falls back to
// built-in read/grep. Terminal-launched opencode hid this for a long time.
//
// Returns a doctor-style (level, detail) pair: ok / warn / fail.
func LaunchIntegrity() (string, string) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "ok", "no home directory — global launch config not checked"
	}

	type site struct {
		name string
		path string
		json []string // key path for a JSON config
		toml bool
	}
	sites := []site{
		{"claude", filepath.Join(home, ".claude.json"), []string{"mcpServers", "kern", "command"}, false},
		{"cursor", filepath.Join(home, ".cursor", "mcp.json"), []string{"mcpServers", "kern", "command"}, false},
		{"gemini", filepath.Join(home, ".gemini", "settings.json"), []string{"mcpServers", "kern", "command"}, false},
		{"qwen", filepath.Join(home, ".qwen", "settings.json"), []string{"mcpServers", "kern", "command"}, false},
		{"opencode", filepath.Join(home, ".config", "opencode", "opencode.json"), []string{"mcp", "kern", "command"}, false},
		{"codex", filepath.Join(home, ".codex", "config.toml"), nil, true},
	}

	var bare, missing, absolute []string
	for _, s := range sites {
		data, err := os.ReadFile(s.path)
		if err != nil {
			continue
		}
		var cmd string
		if s.toml {
			cmd = tomlKernCommand(string(data))
		} else {
			cmd = jsonCommand(data, s.json...)
		}
		cmd = strings.TrimSpace(cmd)
		if cmd == "" {
			continue
		}
		if !filepath.IsAbs(cmd) {
			bare = append(bare, s.name)
			continue
		}
		if _, err := os.Stat(cmd); err != nil {
			missing = append(missing, s.name+" ("+cmd+")")
			continue
		}
		absolute = append(absolute, s.name)
	}

	switch {
	case len(missing) > 0:
		return "fail", "global MCP command points at a missing binary: " + strings.Join(missing, ", ") + " — rerun 'kern setup --global'"
	case len(bare) > 0:
		return "warn", "global MCP uses a bare 'kern-mcp' in " + strings.Join(bare, ", ") + " — GUI-launched hosts do not inherit the shell PATH and will not start the server; rerun 'kern setup --global' to write the absolute command"
	case len(absolute) > 0:
		return "ok", "global MCP launches by absolute path: " + strings.Join(absolute, ", ")
	default:
		return "ok", "no machine-global kern MCP entry found (project config may still wire it)"
	}
}

// jsonCommand walks a JSON document along path and returns the first command
// string. An opencode-style array command (["kern-mcp"]) yields its first
// element; anything else yields "".
func jsonCommand(data []byte, path ...string) string {
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return ""
	}
	for _, key := range path {
		m, ok := v.(map[string]any)
		if !ok {
			return ""
		}
		v = m[key]
	}
	switch c := v.(type) {
	case string:
		return c
	case []any:
		if len(c) > 0 {
			if s, ok := c[0].(string); ok {
				return s
			}
		}
	}
	return ""
}

// tomlKernCommand extracts `command = "..."` from the [mcp_servers.kern]
// table in a Codex config.toml. It stops at the next table header so a later
// server's command is never attributed to kern.
func tomlKernCommand(data string) string {
	lines := strings.Split(data, "\n")
	inKern := false
	re := regexp.MustCompile(`^\s*command\s*=\s*"([^"]*)"`)
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			inKern = trimmed == "[mcp_servers.kern]"
			continue
		}
		if !inKern {
			continue
		}
		if m := re.FindStringSubmatch(line); m != nil {
			return m[1]
		}
	}
	return ""
}
