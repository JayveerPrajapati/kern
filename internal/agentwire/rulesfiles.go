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

// ThinAGENTSmd returns the CORE repo AGENTS.md content (the T-05 compact
// default, 2026-11-19): the kern-first routing table, the pre-edit blast
// radius rule, the verify-after-change rule, the per-call fallback rule and
// the opencode guard boundaries — the security-critical subset of the full
// rules in <=2KB. The extended policy (catalog details, examples, lifecycle
// guidance) is opt-in (`kern setup --agents-md full`) and on-demand
// (kern_buddy, kern_meta, kern --help); the host's global instructions slot
// additionally carries the condensed rules when `kern setup --global-rules`
// wired it. A full rules file is never downgraded to this block without an
// explicit choice (WireThinRulesFile).
func ThinAGENTSmd(wired string) string {
	return strings.Join([]string{
		"# kern usage rules — thin (repo)",
		"",
		"Wired agents: " + wired,
		"kern is installed for this repo — call the direct kern tools first: `kern_search`, `kern_explore`, `kern_context`, `kern_probe`, `kern_impact`, `kern_graph`, `kern_review`, `kern_verify`, `kern_buddy`.",
		"Session start: call `kern_buddy` FIRST — the onboarding digest (conventions, layout, entry points).",
		"Routing: read → kern_explore · grep → kern_search · context → kern_context · task context → kern_probe · impact/what-breaks → kern_impact · build/test → kern_verify · unsure → kern_meta.",
		"Before ANY edit: run `kern_impact` for the blast radius and `kern_explore` for the exact symbols — never edit blind.",
		"After ANY change: verify with `kern_verify` (build + tests + security of the touched packages).",
		"Fallback rule: when a kern tool errors, fall back for THAT call only (built-in read/grep) and keep kern-first for the rest.",
		"On opencode, built-in read/glob/grep/bash route to kern via the plugin shadows; GATE commands (writes, installs, network) are blocked — ask the operator (escapes: KERN_BYPASS=1 / KERN_ENFORCE=0, operator-gated).",
		"Default surface: 11 tools; set KERN_MCP_FULL=1 for all 34 (kern_exec is full-mode and governed).",
		"Extended rules: `kern setup --agents-md full` opts this repo into the long policy; on-demand help: `kern_buddy`, `kern_meta`, `kern --help`.",
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
