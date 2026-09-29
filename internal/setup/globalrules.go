package setup

import (
	"embed"
	"os"
	"path/filepath"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/strutil"
)

//go:embed assets/global-rules.md
var globalRulesFS embed.FS

// GlobalRulesPathHosts names the six hosts whose GLOBAL instructions slot
// carries the kern agent-usage policy (research-verified 2026-09-28):
//   - Claude Code  → ~/.claude/CLAUDE.md   (user-global CLAUDE.md + project
//     AGENTS.md are BOTH loaded; only a project CLAUDE.md suppresses AGENTS.md)
//   - Codex CLI    → ~/.codex/AGENTS.md    (global + project concatenated)
//   - opencode     → ~/.config/opencode/AGENTS.md (global + project merged)
//   - Cursor       → ~/.cursor/rules/kern.mdc (user-global rules dir,
//     machine-local, does NOT sync; .mdc needs YAML frontmatter on line 1
//     with alwaysApply: true, OUTSIDE the managed marker block)
//   - Gemini CLI   → ~/.gemini/GEMINI.md   (plain markdown, same hierarchical
//     pattern as Claude's CLAUDE.md; NOT ~/.gemini/config/GEMINI.md)
//   - Windsurf     → ~/.codeium/windsurf/memories/global_rules.md (plain
//     markdown, no frontmatter; the WHOLE FILE is capped at 6000 chars, so
//     the managed block must stay under it)
//
// All six load global AND project rules together, which is why the thin
// repo AGENTS.md mode is the default (--agents-md=thin, --agents-md=full to
// opt out): it prevents the same rules from being double-loaded in one
// session.
//
// Note: none of these tools reads a home-root ~/AGENTS.md natively — the
// ADR-0011 claims (~/.gemini/config/GEMINI.md and
// ~/.windsurf/rules/kern-first.md) were wrong; a later docs pass fixes the
// ADR.
//
// The marker scheme is the single source of truth for these files: content
// between globalRulesMarkerOpen and globalRulesMarkerClose is kern-managed
// and replaced on every run; anything outside the markers belongs to the
// user and is preserved verbatim.
const (
	globalRulesMarkerOpen  = "<!-- kern:global-rules begin (managed by `kern setup --global-rules`; keep personal prefs outside the markers) -->"
	globalRulesMarkerClose = "<!-- kern:global-rules end -->"

	// cursorFrontmatter is the YAML frontmatter Cursor requires at the top of
	// an .mdc rule file. It must sit on line 1, outside the kern-managed
	// marker block; alwaysApply: true makes the rule apply to every session
	// without a user toggle.
	cursorFrontmatter = "---\ndescription: Kern-first operational directives\nalwaysApply: true\n---"
)

// GlobalRulesPaths returns the six host GLOBAL instruction paths where the
// kern agent-usage rules live: Claude Code, Codex CLI, opencode, Cursor,
// Gemini CLI, and Windsurf. All are user-global, never project-scoped.
func GlobalRulesPaths() []string {
	return []string{
		homeConfig(".claude", "CLAUDE.md")(""),
		homeConfig(".codex", "AGENTS.md")(""),
		globalConfig("opencode", "AGENTS.md")(""),
		homeConfig(".cursor", "rules", "kern.mdc")(""),
		homeConfig(".gemini", "GEMINI.md")(""),
		homeConfig(".codeium", "windsurf", "memories", "global_rules.md")(""),
	}
}

// WireGlobalRules manages the kern agent-usage rules in every host's GLOBAL
// instructions slot (Claude Code, Codex CLI, opencode, Cursor, Gemini CLI,
// Windsurf). For each path it replaces ONLY the marker-delimited block —
// preserving user content outside the markers verbatim — creates the file
// with the block when missing, and appends the block with a blank separator
// when no markers are present. Cursor .mdc files additionally get their YAML
// frontmatter ensured on line 1. Idempotent: a re-run with nothing else
// changed produces identical bytes and skips the write.
func WireGlobalRules() []Status {
	var out []Status
	for _, p := range GlobalRulesPaths() {
		out = append(out, wireGlobalRulesFile(p))
	}
	return out
}

// wireGlobalRulesFile manages one global instruction file. Existing files
// keep all content outside the markers; only the kern-managed block is
// replaced. Missing files are created with the canonical block. Cursor .mdc
// paths additionally get the YAML frontmatter ensured at the top of the
// file, outside the markers.
func wireGlobalRulesFile(path string) Status {
	block, err := globalRulesFS.ReadFile("assets/global-rules.md")
	if err != nil {
		return Status{Agent: "global-rules", Path: path, Note: err.Error()}
	}
	content := ""
	if b, rerr := os.ReadFile(path); rerr == nil {
		content = string(b)
	}
	// Strip the existing kern-managed block (if any) so the fresh canonical
	// block can be re-inserted in its place, preserving all user content.
	cleaned := strutil.RemoveMarkedBlock(content, globalRulesMarkerOpen, globalRulesMarkerClose)
	var final string
	if strings.TrimSpace(cleaned) == "" {
		final = string(block)
	} else {
		// Blank separator between user content and the managed block.
		final = strings.TrimRight(cleaned, "\n") + "\n\n" + strings.TrimRight(string(block), "\n") + "\n"
	}
	// Cursor .mdc rules need YAML frontmatter on line 1, outside the markers.
	if strings.HasSuffix(path, ".mdc") {
		final = ensureMDCFrontmatter(final)
	}
	if content != "" && final == content {
		return Status{Agent: "global-rules", Installed: true, Path: path, Note: "global rules already current"}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Status{Agent: "global-rules", Path: path, Note: err.Error()}
	}
	// Global instruction files default to 0644 when created (like the other
	// global rule files); existing files keep their current permissions.
	if err := os.WriteFile(path, []byte(final), ruleFileMode(path)); err != nil {
		return Status{Agent: "global-rules", Path: path, Note: err.Error()}
	}
	return Status{Agent: "global-rules", Installed: true, Path: path, Note: "global rules written"}
}

// ensureMDCFrontmatter guarantees the Cursor frontmatter sits before the
// managed marker block (line 1 in the canonical layout). If the frontmatter
// is already present ahead of the markers — even if the user put personal
// notes above it — the input is returned byte-for-byte unchanged, keeping
// re-runs idempotent and user content outside the markers verbatim. Only
// when it is missing entirely is it prepended.
func ensureMDCFrontmatter(s string) string {
	if marker := strings.Index(s, globalRulesMarkerOpen); marker >= 0 &&
		strings.Index(s[:marker], cursorFrontmatter) >= 0 {
		return s
	}
	return cursorFrontmatter + "\n\n" + s
}

// globalRulesStatus is used by Check to report the state of every global
// rules path.
func globalRulesStatus() []Status {
	var out []Status
	for _, p := range GlobalRulesPaths() {
		out = append(out, fileStatus(p, "global rules"))
	}
	return out
}
