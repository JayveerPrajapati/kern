package setup

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// globalHomeDir resolves the user's home directory. It is a variable so tests
// can point it at a temp directory and never touch the real home.
var globalHomeDir = func() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return home
}

// globalAGENTSPath returns the path to the global AGENTS.md.
func globalAGENTSPath() string {
	return filepath.Join(globalHomeDir(), "AGENTS.md")
}

// globalClaudePath returns the path to the global Claude instruction file.
func globalClaudePath() string {
	return filepath.Join(globalHomeDir(), ".claude", "CLAUDE.md")
}

// globalPluginPath returns the path to the global opencode kern plugin.
func globalPluginPath() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		base = filepath.Join(globalHomeDir(), ".config")
	}
	return filepath.Join(base, "opencode", "plugins", "kern.ts")
}

// WireGlobal writes the kern-first instruction to globally-read locations so
// agents in ANY project see it, not just the one where setup ran. An empty
// agents list means "all known agents"; otherwise only the listed agents are
// wired. The global AGENTS.md (the universal instruction file) is always
// written, mirroring the project-level wireAgentRules behaviour.
func WireGlobal(agents []string) []Status {
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
	out = append(out, writeGlobalAGENTS())
	if enabled("claude") {
		out = append(out, writeGlobalClaude())
	}
	if enabled("opencode") {
		out = append(out, copyGlobalPlugin())
	}
	return out
}

// writeGlobalAGENTS merges the kern-first block into ~/AGENTS.md. An existing
// kern section is removed and the fresh block prepended at the top, preserving
// all other content. The merge is idempotent.
func writeGlobalAGENTS() Status {
	path := globalAGENTSPath()
	kern, err := rulesFS.ReadFile("assets/AGENTS.md")
	if err != nil {
		return Status{Agent: "global-AGENTS.md", Path: path, Note: err.Error()}
	}
	existing := ""
	if b, rerr := os.ReadFile(path); rerr == nil {
		existing = string(b)
	}
	final := mergePrepend(existing, string(kern))
	if existing != "" && final == existing {
		return Status{Agent: "global-AGENTS.md", Installed: true, Path: path, Note: "kern-first policy already present"}
	}
	if existing != "" {
		if err := backupFile(path); err != nil {
			return Status{Agent: "global-AGENTS.md", Path: path, Note: "backup: " + err.Error()}
		}
	}
	if err := os.WriteFile(path, []byte(final), ruleFileMode(path)); err != nil {
		return Status{Agent: "global-AGENTS.md", Path: path, Note: err.Error()}
	}
	return Status{Agent: "global-AGENTS.md", Installed: true, Path: path, Note: "kern-first policy written to ~/AGENTS.md"}
}

// writeGlobalClaude appends the kern-first block to ~/.claude/CLAUDE.md,
// creating the file when absent. Skipped when Claude isn't installed.
func writeGlobalClaude() Status {
	dir := filepath.Join(globalHomeDir(), ".claude")
	if _, err := os.Stat(dir); err != nil {
		return Status{Agent: "claude-global", Skipped: true, Path: dir, Note: "~/.claude not present — skipped"}
	}
	path := globalClaudePath()
	kern, err := rulesFS.ReadFile("assets/AGENTS.md")
	if err != nil {
		return Status{Agent: "claude-global", Path: path, Note: err.Error()}
	}
	existing := ""
	if b, rerr := os.ReadFile(path); rerr == nil {
		existing = string(b)
	}
	final := mergeAppend(existing, string(kern))
	if existing != "" && final == existing {
		return Status{Agent: "claude-global", Installed: true, Path: path, Note: "kern-first policy already present"}
	}
	if existing != "" {
		if err := backupFile(path); err != nil {
			return Status{Agent: "claude-global", Path: path, Note: "backup: " + err.Error()}
		}
	}
	if err := os.WriteFile(path, []byte(final), ruleFileMode(path)); err != nil {
		return Status{Agent: "claude-global", Path: path, Note: err.Error()}
	}
	return Status{Agent: "claude-global", Installed: true, Path: path, Note: "kern-first policy appended to ~/.claude/CLAUDE.md"}
}

// copyGlobalPlugin copies the embedded opencode plugin to the global opencode
// plugins directory so in-place output compression and session memory work in
// every project. Skipped when opencode isn't installed.
func copyGlobalPlugin() Status {
	dst := globalPluginPath()
	opencodeDir := filepath.Dir(filepath.Dir(dst))
	if _, err := os.Stat(opencodeDir); err != nil {
		return Status{Agent: "opencode-plugin-global", Skipped: true, Path: dst, Note: "opencode not installed — skipped"}
	}
	src, err := pluginFS.ReadFile("assets/plugin/kern.ts")
	if err != nil {
		return Status{Agent: "opencode-plugin-global", Path: dst, Note: err.Error()}
	}
	if cur, rerr := os.ReadFile(dst); rerr == nil && bytes.Equal(cur, src) {
		return Status{Agent: "opencode-plugin-global", Installed: true, Path: dst, Note: "global plugin already current"}
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return Status{Agent: "opencode-plugin-global", Path: dst, Note: err.Error()}
	}
	if err := os.WriteFile(dst, src, 0o644); err != nil {
		return Status{Agent: "opencode-plugin-global", Path: dst, Note: err.Error()}
	}
	return Status{Agent: "opencode-plugin-global", Installed: true, Path: dst, Note: "global opencode plugin installed"}
}

// ruleFileMode returns the permission bits for a rule file: existing files
// keep their current bits, new files default to 0644.
func ruleFileMode(path string) os.FileMode {
	if info, err := os.Stat(path); err == nil {
		return info.Mode().Perm()
	}
	return 0o644
}

// backupFile copies path to path+".bak.<timestamp>" before a rewrite so a
// destructive edit never destroys the user's original content without recourse.
func backupFile(path string) error {
	b, err := os.ReadFile(path)
	if err != nil || len(b) == 0 {
		return nil
	}
	ts := time.Now().Format("20060102-150405")
	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	return os.WriteFile(path+".bak."+ts, b, mode)
}

// removeKernSection strips any "kern usage rules" block from s, matching from
// the "# kern usage rules" header to the next level-1 header or EOF. Returns s
// unchanged when no kern block is present.
func removeKernSection(s string) string {
	idx := strings.Index(s, "# kern usage rules")
	if idx < 0 {
		return s
	}
	end := len(s)
	if next := strings.Index(s[idx+1:], "\n# "); next >= 0 {
		end = idx + 1 + next + 1
	}
	return s[:idx] + s[end:]
}

// mergePrepend removes any existing kern section from existing and prepends
// the fresh kern block at the top, preserving all other content.
func mergePrepend(existing, kern string) string {
	cleaned := strings.TrimSpace(removeKernSection(existing))
	kern = strings.TrimRight(kern, "\n")
	if cleaned == "" {
		return kern + "\n"
	}
	return kern + "\n\n" + cleaned + "\n"
}

// mergeAppend removes any existing kern section from existing and appends the
// fresh kern block at the end, preserving all other content.
func mergeAppend(existing, kern string) string {
	cleaned := strings.TrimRight(removeKernSection(existing), "\n")
	kern = strings.TrimRight(kern, "\n")
	if cleaned == "" {
		return kern + "\n"
	}
	return cleaned + "\n\n" + kern + "\n"
}
