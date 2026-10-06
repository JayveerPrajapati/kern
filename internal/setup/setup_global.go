package setup

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/rulesblock"
	"github.com/JayveerPrajapati/kern/internal/strutil"
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
	if enabled("antigravity") {
		out = append(out, wireAntigravityHooks())
	}
	if enabled("opencode") {
		out = append(out, copyGlobalPlugin())
	}
	out = append(out, wireGlobalGitHooks())
	out = append(out, wireGlobalSkills()...)
	return out
}

// writeGlobalAGENTS merges the kern-first block into ~/AGENTS.md. An existing
// kern section is removed and the fresh block prepended at the top, preserving
// all other content. The merge is idempotent. Both managed formats are
// stripped (unmarked "# kern usage rules" blocks + the marker-delimited block
// from `kern setup --global-rules`) so the file converges to one kern section.
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
	// Version guard (C7): never clobber wiring written by a NEWER kern.
	if v := managedStamp(existing); v != "" && managedNewerThanRunning(v) {
		return Status{Agent: "global-AGENTS.md", Installed: true, Path: path, Note: "kern section is newer (kern-version " + v + ") — left untouched; remove the stamp line to force a rewrite"}
	}
	existing = strutil.RemoveMarkedBlock(existing, globalRulesMarkerOpen, globalRulesMarkerClose)
	final, err := mergePrepend(existing, string(kern))
	if err != nil {
		return Status{Agent: "global-AGENTS.md", Path: path, Note: err.Error()}
	}
	final = insertManagedStamp(final)
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
	// Version guard (C7): never clobber wiring written by a NEWER kern.
	if v := managedStamp(existing); v != "" && managedNewerThanRunning(v) {
		return Status{Agent: "claude-global", Installed: true, Path: path, Note: "kern section is newer (kern-version " + v + ") — left untouched; remove the stamp line to force a rewrite"}
	}
	// Strip BOTH managed formats so the file converges to a single kern
	// section: the unmarked "# kern usage rules" blocks (this writer's own
	// output, possibly accumulated across old runs) AND the marker-delimited
	// block written by `kern setup --global-rules` (F15 cross-format fix —
	// without it the two writers stack two coexisting kern sections).
	existing = strutil.RemoveMarkedBlock(existing, globalRulesMarkerOpen, globalRulesMarkerClose)
	final, err := mergeAppend(existing, string(kern))
	if err != nil {
		return Status{Agent: "claude-global", Path: path, Note: err.Error()}
	}
	final = insertManagedStamp(final)
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
	if cur, rerr := os.ReadFile(dst); rerr == nil {
		if bytes.Equal(cur, src) {
			return Status{Agent: "opencode-plugin-global", Installed: true, Path: dst, Note: "global plugin already current"}
		}
		// Same policy as wireGlobalPlugin (F-DR1): a previously shipped
		// version is kern's own deployment and is updated; only an
		// unrecognized copy is treated as user-customized and never
		// overwritten. This writer previously clobbered ANY non-current
		// copy — including hand edits — with no policy at all.
		if !isShippedPluginVersion(cur) {
			return Status{Agent: "opencode-plugin-global", Installed: true, Path: dst, Note: "global plugin is customized — left untouched"}
		}
		if err := os.WriteFile(dst, src, 0o644); err != nil {
			return Status{Agent: "opencode-plugin-global", Path: dst, Note: err.Error()}
		}
		return Status{Agent: "opencode-plugin-global", Installed: true, Path: dst, Note: "global plugin updated from an older kern version"}
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

// removeKernSection removes EVERY kern-managed block from s — both the
// marker-delimited format and the unmarked "# kern usage rules" format —
// preserving all other content. The pure text manipulation lives in
// internal/rulesblock; this wrapper keeps the setup-internal signature.
func removeKernSection(s string) (string, error) {
	return rulesblock.ExciseKernBlock(s)
}

// mergePrepend removes any existing kern section from existing and prepends
// the fresh kern block at the top, preserving all other content.
func mergePrepend(existing, kern string) (string, error) {
	cleaned, err := removeKernSection(existing)
	if err != nil {
		return "", err
	}
	cleaned = strings.TrimSpace(cleaned)
	kern = strings.TrimRight(kern, "\n")
	if cleaned == "" {
		return kern + "\n", nil
	}
	return kern + "\n\n" + cleaned + "\n", nil
}

// mergeAppend removes any existing kern section from existing and appends the
// fresh kern block at the end, preserving all other content.
func mergeAppend(existing, kern string) (string, error) {
	cleaned, err := removeKernSection(existing)
	if err != nil {
		return "", err
	}
	cleaned = strings.TrimRight(cleaned, "\n")
	kern = strings.TrimRight(kern, "\n")
	if cleaned == "" {
		return kern + "\n", nil
	}
	return cleaned + "\n\n" + kern + "\n", nil
}
