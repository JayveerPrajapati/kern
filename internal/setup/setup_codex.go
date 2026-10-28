package setup

import (
	"os"
	"path/filepath"
	"strings"
)

// wireCodex writes the Codex MCP server entry into ~/.codex/config.toml and
// ensures the `[features] codex_hooks = true` flag is present so the codex
// PreToolUse hook (wireCodexHooks) actually activates. Codex uses TOML rather
// than JSON, so it gets a small dedicated writer instead of the JSON adapters.
//
// The kern table is kern-owned: every run rewrites it wholesale. That is what
// repairs two live regressions — a stale bare `kern-mcp` command (unresolvable
// in GUI-launched Codex, which does not inherit the shell PATH) and the old
// `KERN_ALLOW_EXEC = "1"` env block (exec must stay fail-closed and opt-in per
// host).
func wireCodex(bin string) Status {
	home, err := os.UserHomeDir()
	if err != nil {
		return Status{Agent: "codex", Note: err.Error()}
	}
	path := filepath.Join(home, ".codex", "config.toml")

	content := ""
	if b, err := os.ReadFile(path); err == nil {
		content = string(b)
	}

	updated := rewriteCodexKern(content, bin)
	updated, featuresChanged := ensureCodexFeatures(path, updated)
	if updated == content {
		return Status{Agent: "codex", Installed: true, Path: path, Note: "codex config already registers kern"}
	}
	_ = featuresChanged

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Status{Agent: "codex", Path: path, Note: err.Error()}
	}
	if err := os.WriteFile(path, []byte(updated), 0o600); err != nil {
		return Status{Agent: "codex", Path: path, Note: err.Error()}
	}
	return Status{Agent: "codex", Installed: true, Path: path, Note: "codex config updated"}
}

// rewriteCodexKern removes the whole kern-owned table (base and every
// mcp_servers.kern.* subtable, including a stale env block) and appends a clean
// one with the given command. Other servers, tables and user content are
// preserved verbatim.
func rewriteCodexKern(content, bin string) string {
	var kept []string
	skipping := false
	for _, line := range strings.Split(content, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "[") {
			skipping = t == "[mcp_servers.kern]" || strings.HasPrefix(t, "[mcp_servers.kern.")
		}
		if skipping {
			continue
		}
		kept = append(kept, line)
	}
	cleaned := strings.TrimRight(strings.Join(kept, "\n"), "\n")
	escaped := strings.ReplaceAll(bin, `\`, `\\`)
	entry := "\n[mcp_servers.kern]\ncommand = \"" + escaped + "\"\n"
	return cleaned + entry
}

// ensureCodexFeatures returns content with `[features] codex_hooks = true`
// present, reporting whether anything changed. TOML has no append-only merge:
// tables may appear in any order, but a duplicate `[features]` table would be
// invalid. So when a `[features]` table already exists (without the key) the
// key is inserted right after the `[features]` line; otherwise a full
// `[features]` block is appended at EOF. A substring check short-circuits when
// the flag is already set.
func ensureCodexFeatures(path, content string) (string, bool) {
	if strings.Contains(content, "codex_hooks = true") {
		return content, false
	}
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) == "[features]" {
			lines = append(lines[:i+1], append([]string{"codex_hooks = true"}, lines[i+1:]...)...)
			return strings.Join(lines, "\n"), true
		}
	}
	return content + "\n[features]\ncodex_hooks = true\n", true
}

// codexStatus is the read-only setup-status probe for Codex.
func codexStatus() Status {
	home, err := os.UserHomeDir()
	if err != nil {
		return Status{Agent: "codex", Note: err.Error()}
	}
	path := filepath.Join(home, ".codex", "config.toml")
	features := "off"
	if b, err := os.ReadFile(path); err == nil {
		if strings.Contains(string(b), "codex_hooks = true") {
			features = "on"
		}
		if strings.Contains(string(b), "[mcp_servers.kern]") {
			return Status{Agent: "codex", Installed: true, Path: path, Note: "kern MCP registered; codex_hooks feature: " + features}
		}
	}
	return Status{Agent: "codex", Path: path, Note: "codex config.toml has no kern MCP; codex_hooks feature: " + features}
}
