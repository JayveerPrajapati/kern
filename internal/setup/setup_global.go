package setup

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/agentwire"
	"github.com/JayveerPrajapati/kern/internal/strutil"
)

// globalHomeDir resolves the user's home directory. The canonical variable
// lives in internal/agentwire (GlobalHomeDir) so the wiring steps extracted
// there share the same overridable home; this forwarding func keeps the
// setup-internal call sites unchanged.
var globalHomeDir = func() string { return agentwire.GlobalHomeDir() }

// GlobalRulesPaths returns the six host GLOBAL instruction paths where the
// kern agent-usage rules live. Implemented in internal/agentwire.
func GlobalRulesPaths() []string {
	return agentwire.GlobalRulesPaths()
}

// WireGlobalRules manages the kern agent-usage rules in every host's GLOBAL
// instructions slot. Implemented in internal/agentwire.
func WireGlobalRules() []Status {
	return agentwire.WireGlobalRules()
}

// globalAGENTSPath returns the path to the global AGENTS.md.
func globalAGENTSPath() string {
	return filepath.Join(globalHomeDir(), "AGENTS.md")
}

// globalClaudePath returns the path to the global Claude instruction file.
func globalClaudePath() string {
	return filepath.Join(globalHomeDir(), ".claude", "CLAUDE.md")
}

// WireGlobal writes the kern-first instruction to globally-read locations so
// agents in ANY project see it, not just the one where setup ran. An empty
// agents list means "all known agents"; otherwise only the listed agents are
// wired. The global AGENTS.md (the universal instruction file) is always
// written, mirroring the project-level WireAgentRules behaviour.
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
	out = append(out, agentwire.WireGlobalSkills()...)
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
	if v := agentwire.ManagedStamp(existing); v != "" && agentwire.ManagedNewerThanRunning(v) {
		return Status{Agent: "global-AGENTS.md", Installed: true, Path: path, Note: "kern section is newer (kern-version " + v + ") — left untouched; remove the stamp line to force a rewrite"}
	}
	existing = strutil.RemoveMarkedBlock(existing, globalRulesMarkerOpen, globalRulesMarkerClose)
	final, err := mergePrepend(existing, string(kern))
	if err != nil {
		return Status{Agent: "global-AGENTS.md", Path: path, Note: err.Error()}
	}
	final = agentwire.InsertManagedStamp(final)
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
	if v := agentwire.ManagedStamp(existing); v != "" && agentwire.ManagedNewerThanRunning(v) {
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
	final = agentwire.InsertManagedStamp(final)
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
	dst := agentwire.GlobalPluginPath()
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
		// Same policy as WireGlobalPlugin (F-DR1): a previously shipped
		// version is kern's own deployment and is updated; an unrecognized
		// copy is treated as user-customized — backed up next to the
		// original, then refreshed, so doctor's "run: kern setup --global"
		// remedy converges instead of leaving a stale copy silently winning
		// opencode's plugin load forever. (This writer previously clobbered
		// ANY non-current copy — including hand edits — with no policy at
		// all.)
		if !isShippedPluginVersion(cur) {
			bak, berr := agentwire.BackupDivergentPlugin(dst, cur)
			if berr != nil {
				return Status{Agent: "opencode-plugin-global", Installed: true, Path: dst, Note: "global plugin is customized — left untouched (backup failed: " + berr.Error() + ")"}
			}
			if err := os.WriteFile(dst, src, 0o644); err != nil {
				return Status{Agent: "opencode-plugin-global", Path: dst, Note: err.Error()}
			}
			return Status{Agent: "opencode-plugin-global", Installed: true, Path: dst, Note: "global plugin customized — backed up to " + bak + " and refreshed (user edits preserved in the backup)"}
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
// keep their current bits, new files default to 0644. Implemented in
// internal/agentwire (RuleFileMode).
func ruleFileMode(path string) os.FileMode {
	return agentwire.RuleFileMode(path)
}

// backupFile copies path to path+".bak.<timestamp>" before a rewrite so a
// destructive edit never destroys the user's original content without recourse.
// Implemented in internal/agentwire (BackupFile).
func backupFile(path string) error {
	return agentwire.BackupFile(path)
}

// removeKernSection removes EVERY kern-managed block from s — both the
// marker-delimited format and the unmarked "# kern usage rules" format —
// preserving all other content. Implemented in internal/agentwire
// (RemoveKernSection); this delegator keeps the setup-internal signature.
func removeKernSection(s string) (string, error) {
	return agentwire.RemoveKernSection(s)
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
// fresh kern block at the end, preserving all other content. Implemented in
// internal/agentwire (MergeAppend).
func mergeAppend(existing, kern string) (string, error) {
	return agentwire.MergeAppend(existing, kern)
}
