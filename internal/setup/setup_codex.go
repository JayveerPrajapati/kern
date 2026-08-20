package setup

import (
	"os"
	"path/filepath"
	"strings"
)

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
