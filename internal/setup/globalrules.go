package setup

import (
	"embed"
	"os"
	"path/filepath"
	"strings"
)

//go:embed assets/global-rules.md
var globalRulesFS embed.FS

// GlobalRulesPathHosts names the three hosts whose GLOBAL instructions slot
// carries the kern agent-usage policy (research-verified 2026-09-23):
//   - Claude Code  → ~/.claude/CLAUDE.md   (user-global CLAUDE.md + project
//     AGENTS.md are BOTH loaded; only a project CLAUDE.md suppresses AGENTS.md)
//   - Codex CLI    → ~/.codex/AGENTS.md    (global + project concatenated)
//   - opencode     → ~/.config/opencode/AGENTS.md (global + project merged)
//
// All three load global AND project rules together, which is why the thin
// repo AGENTS.md mode (--agents-md=thin) exists as an opt-in: it prevents
// the same rules from being double-loaded in one session.
//
// The marker scheme is the single source of truth for these files: content
// between globalRulesMarkerOpen and globalRulesMarkerClose is kern-managed
// and replaced on every run; anything outside the markers belongs to the
// user and is preserved verbatim.
const (
	globalRulesMarkerOpen  = "<!-- kern:global-rules begin (managed by `kern setup --global-rules`; keep personal prefs outside the markers) -->"
	globalRulesMarkerClose = "<!-- kern:global-rules end -->"
)

// GlobalRulesPaths returns the three host GLOBAL instruction paths where the
// kern agent-usage rules live: Claude Code, Codex CLI, and opencode. All are
// user-global, never project-scoped.
func GlobalRulesPaths() []string {
	return []string{
		homeConfig(".claude", "CLAUDE.md")(""),
		homeConfig(".codex", "AGENTS.md")(""),
		globalConfig("opencode", "AGENTS.md")(""),
	}
}

// WireGlobalRules manages the kern agent-usage rules in every host's GLOBAL
// instructions slot (Claude Code, Codex CLI, opencode). For each path it
// replaces ONLY the marker-delimited block — preserving user content outside
// the markers verbatim — creates the file with the block when missing, and
// appends the block with a blank separator when no markers are present.
// Idempotent: a re-run with nothing else changed produces identical bytes
// and skips the write.
func WireGlobalRules() []Status {
	var out []Status
	for _, p := range GlobalRulesPaths() {
		out = append(out, wireGlobalRulesFile(p))
	}
	return out
}

// wireGlobalRulesFile manages one global instruction file. Existing files
// keep all content outside the markers; only the kern-managed block is
// replaced. Missing files are created with the canonical block.
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
	cleaned := removeMarkedBlock(content, globalRulesMarkerOpen, globalRulesMarkerClose)
	var final string
	if strings.TrimSpace(cleaned) == "" {
		final = string(block)
	} else {
		// Blank separator between user content and the managed block.
		final = strings.TrimRight(cleaned, "\n") + "\n\n" + strings.TrimRight(string(block), "\n") + "\n"
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

// globalRulesStatus is used by Check to report the state of every global
// rules path.
func globalRulesStatus() []Status {
	var out []Status
	for _, p := range GlobalRulesPaths() {
		out = append(out, fileStatus(p, "global rules"))
	}
	return out
}
