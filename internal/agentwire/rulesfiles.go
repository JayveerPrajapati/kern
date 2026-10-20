package agentwire

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/strutil"
)

// FullRulesHeader is the first line of the full AGENTS.md variant. Its
// presence in an existing file means the full usage rules are installed —
// replacing them with the thin stub is a destructive downgrade.
const FullRulesHeader = "# kern usage rules for agents — READ FIRST"

// ThinAGENTSmd returns the thin repo AGENTS.md content: the critical
// kern-first routing facts in a few lines — kern_meta as the single entry
// point, the decision-table essentials (read→kern_explore, grep→kern_search,
// build/test→kern_verify, unsure→kern_meta), the session-start kern_buddy
// call, the default 7-tool surface, and the env vars that widen it. The full
// kern usage rules live in the host's GLOBAL instructions slot (managed by
// `kern setup --global-rules`), so a thin repo file avoids loading the same
// rules twice in one session on hosts that merge global + project rules.
func ThinAGENTSmd(wired string) string {
	return strings.Join([]string{
		"# kern usage rules — thin (repo)",
		"",
		"Wired agents: " + wired,
		"kern is installed for this repo — call `kern_meta` FIRST for everything; it routes to the right kern_* tool.",
		"Kern-first routing: read → kern_explore · grep → kern_search · build/test → kern_verify · unsure → kern_meta.",
		"Session start: call `kern_buddy` FIRST — the onboarding digest (conventions, layout, entry points).",
		"Default surface: 7 tools (kern_meta, kern_explore, kern_impact, kern_search, kern_verify, kern_buddy, kern_exec); set KERN_MCP_FULL=1 for all 117 (KERN_MCP_PHASE for a phase subset).",
		"On opencode, built-in read/glob/grep/bash route to kern via the plugin shadows.",
	}, "\n") + "\n"
}

// WireRulesFile writes the embedded kern rules block to a single rule file
// (AGENTS.md). On every run it removes any existing kern section and re-inserts
// the fresh embedded rules, preserving all user content outside the block.
// This is overwrite-always: an upgrade with new rules always replaces the old
// block rather than skipping when a "kern usage rules" header is present.
// The same per-agent files (CLAUDE.md, GEMINI.md, …) can also be written by
// writeInstructionFile using a marked block (instructionMarkerOpen/Close).
// To avoid duplicate blocks when both paths run, WireRulesFile strips BOTH the
// unmarked kern section AND any marked instruction block before re-inserting.
func WireRulesFile(root, name string) Status {
	path := filepath.Join(root, name)
	content := ""
	if b, err := os.ReadFile(path); err == nil {
		content = string(b)
	}
	rules, err := rulesFS.ReadFile("assets/AGENTS.md")
	if err != nil {
		return Status{Agent: name, Path: path, Note: err.Error()}
	}
	cleaned := strutil.RemoveMarkedBlock(content, InstructionMarkerOpen, InstructionMarkerClose)
	cleaned, err = RemoveKernSection(cleaned)
	if err != nil {
		return Status{Agent: name, Path: path, Note: err.Error()}
	}
	// If the block is unchanged (same content after remove+reinsert), skip the
	// write to avoid needlessly bumping mtime. This mirrors the global path.
	final, err := MergeAppend(cleaned, string(rules))
	if err != nil {
		return Status{Agent: name, Path: path, Note: err.Error()}
	}
	if content != "" && final == content {
		return Status{Agent: name, Installed: true, Path: path, Note: "rules already current"}
	}
	if err := os.WriteFile(path, []byte(final), WritePerm(path)); err != nil {
		return Status{Agent: name, Path: path, Note: err.Error()}
	}
	return Status{Agent: name, Installed: true, Path: path, Note: "rules written"}
}

// WireThinRulesFile writes the thin variant to a single rule file, replacing
// an existing kern-managed THIN section (or writing fresh) while preserving
// user content outside it. A present FULL rules file is never downgraded
// unless the run carries an explicit thin choice (the --agents-md flag or a
// persisted .kern/config.json preference) — an unrequested default must not
// silently delete the full usage rules (a tracked full AGENTS.md,
// e.g. the kern repo's own, stays byte-stable across setup runs). Idempotent:
// an unchanged thin block skips the write.
func WireThinRulesFile(root, name, wired string, explicitThin bool) Status {
	path := filepath.Join(root, name)
	content := ""
	if b, err := os.ReadFile(path); err == nil {
		content = string(b)
	}
	if !explicitThin && strings.Contains(content, FullRulesHeader) {
		return Status{Agent: name, Installed: true, Path: path, Note: "kept existing full rules (pass --agents-md thin to switch)"}
	}
	cleaned := strutil.RemoveMarkedBlock(content, InstructionMarkerOpen, InstructionMarkerClose)
	final, err := MergeAppend(cleaned, ThinAGENTSmd(wired))
	if err != nil {
		return Status{Agent: name, Path: path, Note: err.Error()}
	}
	if content != "" && final == content {
		return Status{Agent: name, Installed: true, Path: path, Note: "thin rules already current"}
	}
	if err := os.WriteFile(path, []byte(final), WritePerm(path)); err != nil {
		return Status{Agent: name, Path: path, Note: err.Error()}
	}
	return Status{Agent: name, Installed: true, Path: path, Note: "thin rules written"}
}
