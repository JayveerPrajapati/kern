package agentwire

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/strutil"
)

// GlobalRulesPathHosts names the seven hosts whose GLOBAL instructions slot
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
// All seven load global AND project rules together, which is why the thin
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
// between GlobalRulesMarkerOpen and GlobalRulesMarkerClose is kern-managed
// and replaced on every run; anything outside the markers belongs to the
// user and is preserved verbatim.
const (
	GlobalRulesMarkerOpen  = "<!-- kern:global-rules begin (managed by `kern setup --global-rules`; keep personal prefs outside the markers) -->"
	GlobalRulesMarkerClose = "<!-- kern:global-rules end -->"

	// GlobalOmitBegin / GlobalOmitEnd bracket the repo-verbose sections of
	// the canonical rules file (assets/AGENTS.md) that must NOT appear in the
	// condensed global block — they cannot fit the Windsurf 6000-char
	// whole-file cap. CondenseGlobalRules strips everything between each pair.
	GlobalOmitBegin = "<!-- kern:global-omit:begin -->"
	GlobalOmitEnd   = "<!-- kern:global-omit:end -->"

	// CursorFrontmatter is the YAML frontmatter Cursor requires at the top of
	// an .mdc rule file. It must sit on line 1, outside the kern-managed
	// marker block; alwaysApply: true makes the rule apply to every session
	// without a user toggle.
	CursorFrontmatter = "---\ndescription: Kern-first operational directives\nalwaysApply: true\n---"
)

// GlobalRulesPaths returns the seven host GLOBAL instruction paths where the
// kern agent-usage rules live: Claude Code, Codex CLI, opencode, Cursor,
// Gemini CLI, Windsurf, and GitHub Copilot. All are user-global, never
// project-scoped.
func GlobalRulesPaths() []string {
	return []string{
		HomeConfig(".claude", "CLAUDE.md")(""),
		HomeConfig(".codex", "AGENTS.md")(""),
		GlobalConfig("opencode", "AGENTS.md")(""),
		HomeConfig(".cursor", "rules", "kern.mdc")(""),
		HomeConfig(".gemini", "GEMINI.md")(""),
		HomeConfig(".codeium", "windsurf", "memories", "global_rules.md")(""),
		HomeConfig(".github", "copilot-instructions.md")(""),
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
		out = append(out, WireGlobalRulesFile(p))
	}
	return out
}

// CondenseGlobalRules derives the condensed global-host rules block from the
// single canonical rules file (assets/AGENTS.md). Sections wrapped in
// <!-- kern:global-omit:begin --> / <!-- kern:global-omit:end --> are
// stripped (repo-verbose content that cannot fit the Windsurf 6000-char
// whole-file cap); everything else is shared verbatim. The result is wrapped
// in the global-rules markers so WireGlobalRulesFile can use it exactly as
// it used the old embedded file.
func CondenseGlobalRules() ([]byte, error) {
	full, err := rulesFS.ReadFile("assets/AGENTS.md")
	if err != nil {
		return nil, err
	}
	s := string(full)
	var b strings.Builder
	for {
		begin := strings.Index(s, GlobalOmitBegin)
		if begin < 0 {
			b.WriteString(s)
			break
		}
		b.WriteString(s[:begin])
		s = s[begin+len(GlobalOmitBegin):]
		end := strings.Index(s, GlobalOmitEnd)
		if end < 0 {
			return nil, fmt.Errorf("unbalanced %s marker in assets/AGENTS.md", GlobalOmitBegin)
		}
		s = s[end+len(GlobalOmitEnd):]
	}
	body := strings.TrimSpace(b.String())
	// Collapse 3+ newlines to 2 (the omit-marker lines leave blank runs).
	for strings.Contains(body, "\n\n\n") {
		body = strings.ReplaceAll(body, "\n\n\n", "\n\n")
	}
	block := GlobalRulesMarkerOpen + "\n" + body + "\n" + GlobalRulesMarkerClose + "\n"
	return []byte(block), nil
}

// WireGlobalRulesFile manages one global instruction file. Existing files
// keep all content outside the markers; only the kern-managed block is
// replaced. The block is derived at runtime from the single canonical rules
// file (assets/AGENTS.md) with the global-omit sections stripped. Missing
// files are created with the canonical block. Cursor .mdc paths additionally
// get the YAML frontmatter ensured at the top of the file, outside the
// markers.
func WireGlobalRulesFile(path string) Status {
	block, err := CondenseGlobalRules()
	if err != nil {
		return Status{Agent: "global-rules", Path: path, Note: err.Error()}
	}
	content := ""
	if b, rerr := os.ReadFile(path); rerr == nil {
		content = string(b)
	}
	// Version guard (C7): a managed block stamped with a NEWER version than
	// this binary is left untouched — a stale release binary (install.sh
	// post-install, an older checkout) must never clobber newer wiring.
	if v := ManagedStamp(content); v != "" && ManagedNewerThanRunning(v) {
		return Status{Agent: "global-rules", Installed: true, Path: path, Note: "managed block is newer (kern-version " + v + ") — left untouched; remove the stamp line to force a rewrite"}
	}
	// Strip BOTH managed formats so the file converges to one kern section:
	// the marker-delimited block this writer manages, AND any unmarked
	// "# kern usage rules" blocks left by `kern setup --global` (writeGlobal
	// Claude/AGENTS) — without the cross-strip the two writers stack two
	// coexisting kern sections (F15).
	cleaned := strutil.RemoveMarkedBlock(content, GlobalRulesMarkerOpen, GlobalRulesMarkerClose)
	cleaned, err = RemoveKernSection(cleaned)
	if err != nil {
		return Status{Agent: "global-rules", Path: path, Note: err.Error()}
	}
	var final string
	if strings.TrimSpace(cleaned) == "" {
		final = string(block)
	} else {
		// Blank separator between user content and the managed block.
		final = strings.TrimRight(cleaned, "\n") + "\n\n" + strings.TrimRight(string(block), "\n") + "\n"
	}
	// Cursor .mdc rules need YAML frontmatter on line 1, outside the markers.
	if strings.HasSuffix(path, ".mdc") {
		final = EnsureMDCFrontmatter(final)
	}
	// Stamp the managed block with this binary's version (C7) — inside the
	// markers, so the stamp is replaced together with the block and never
	// leaks into user content.
	final = InsertManagedStamp(final)
	if content != "" && final == content {
		return Status{Agent: "global-rules", Installed: true, Path: path, Note: "global rules already current"}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Status{Agent: "global-rules", Path: path, Note: err.Error()}
	}
	// Backup before the rewrite (C7): path+".bak.<timestamp>" keeps the
	// pre-write state recoverable even when the marker merge goes wrong.
	if content != "" {
		if err := BackupFile(path); err != nil {
			return Status{Agent: "global-rules", Path: path, Note: "backup: " + err.Error()}
		}
	}
	// Global instruction files default to 0644 when created (like the other
	// global rule files); existing files keep their current permissions.
	if err := os.WriteFile(path, []byte(final), RuleFileMode(path)); err != nil {
		return Status{Agent: "global-rules", Path: path, Note: err.Error()}
	}
	return Status{Agent: "global-rules", Installed: true, Path: path, Note: "global rules written"}
}

// EnsureMDCFrontmatter guarantees the Cursor frontmatter sits before the
// managed marker block (line 1 in the canonical layout). If the frontmatter
// is already present ahead of the markers — even if the user put personal
// notes above it — the input is returned byte-for-byte unchanged, keeping
// re-runs idempotent and user content outside the markers verbatim. Only
// when it is missing entirely is it prepended.
func EnsureMDCFrontmatter(s string) string {
	if marker := strings.Index(s, GlobalRulesMarkerOpen); marker >= 0 &&
		strings.Index(s[:marker], CursorFrontmatter) >= 0 {
		return s
	}
	return CursorFrontmatter + "\n\n" + s
}

// GlobalRulesStatus is used by Check to report the state of every global
// rules path. A slot whose managed markers are present but whose block no
// longer matches the canonical condensed rules (hand-gutted, or written by
// a kern whose rules changed) is flagged for a `kern setup --global-rules`
// refresh — marker presence alone used to hide rules-less slots from
// doctor (deep-verify L5-8).
func GlobalRulesStatus() []Status {
	var out []Status
	for _, p := range GlobalRulesPaths() {
		st := FileStatus(p, "global rules")
		if st.Installed {
			if b, err := os.ReadFile(p); err == nil && strings.Contains(string(b), GlobalRulesMarkerOpen) &&
				!GlobalSlotCarriesCanonicalRules(p) {
				st.Note = "managed block is not canonical — run `kern setup --global-rules` to refresh (repo AGENTS.md falls back to the full variant until then)"
			}
		}
		out = append(out, st)
	}
	return out
}
