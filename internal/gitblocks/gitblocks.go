// Package gitblocks owns the git ignore/exclude management cluster that
// setup applies to repositories: the kern-generated .gitignore block, the
// machine-global git ignore (~/.config/git/ignore) and the repository-local
// .git/info/exclude wiring. It was extracted from internal/setup so that
// setup stays under its LOC cap. The leaf must NOT import internal/setup
// (that would cycle: setup imports the leaf), so it reports outcomes through
// its own Result shape, which setup adapts into its Status values.
package gitblocks

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/strutil"
)

// Result reports one git-ignore wiring outcome. It mirrors the shape of the
// status type the setup package reports for every other wiring step; setup
// adapts a Result into its own Status without changing observable behavior.
// Errors are folded into Note (exactly as setup.Status does) rather than
// carried on a separate field.
type Result struct {
	Agent     string
	Installed bool
	// Skipped marks a deliberate no-op (empty root, not a git repository).
	// It is not an error: setup must not exit non-zero for skips, only for
	// real failures.
	Skipped bool
	Path    string
	Note    string
}

// gitignoreMarker identifies the block of generated entries this package owns.
const gitignoreMarker = "# --- kern generated (agent wiring, machine-specific) ---"

// blueprintRuntimeEntries are the .blueprint/ runtime artifacts that must be
// git-ignored: audit trails, receipts, verdict/fingerprint caches and the
// metrics file are machine-local by nature. The .blueprint/ directory itself
// and its config files (config.yaml, suppressions.yaml, owners.yaml) are user
// config and must stay committable, so only these specific paths are ignored,
// never ".blueprint/" wholesale.
//
// Ownership: the PROJECT .gitignore block for these entries belongs to
// internal/bpcli/cli's ensureBlueprintRuntimeGitignored (a separate marked
// block appended by `kern install hook` / blueprint install, which also
// carries sec-cache.json). setup
// writes them only to the machine-global ignore (WireGlobalGitignore) —
// do NOT re-add them to gitignoreBody: check strips in-block entries as
// legacy, so duplicating them there dirties .gitignore on every
// setup→check cycle.
var blueprintRuntimeEntries = []string{
	".blueprint/audit/",
	".blueprint/receipts/",
	".blueprint/verdict-cache/",
	".blueprint/fingerprint-cache/",
	".blueprint/metrics.json",
}

// GitignoreGenerated writes the setup-generated project files to .gitignore.
// Machine-specific configs (absolute binary paths in .mcp.json, .claude/,
// .cursor/, .kiro/ hooks) must never be committed; uncommitted copies would
// leak the machine layout and stale paths. But portable configs like
// opencode.json (relative "bin/kern-mcp" command) and the .opencache plugins
// directory ARE committed — only their machine-specific subdirs (node_modules)
// are ignored.
// Idempotent replacement: an existing kern block — marked or
// unmarked, current or stale — is detected and replaced IN PLACE, so running
// setup twice always yields exactly ONE block.
func GitignoreGenerated(root string) Result {
	path := filepath.Join(root, ".gitignore")
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Result{Agent: "gitignore", Path: path, Note: err.Error()}
	}
	closeMarker := "# --- end kern generated ---"
	block := "\n" + gitignoreMarker + "\n" + gitignoreBody + closeMarker + "\n"
	content := string(data)
	// Fast idempotency: the canonical block is already present — nothing to
	// write (no duplicate append, no mtime bump).
	if strings.Contains(content, block) {
		return Result{Agent: "gitignore", Installed: true, Path: path, Note: "generated entries already current"}
	}
	// Remove any existing kern block (marked, or the legacy unmarked form
	// written by older versions) and re-append the fresh one in its place.
	cleaned := removeGitignoreBlock(content, closeMarker)
	out := strings.TrimRight(cleaned, "\n")
	if out != "" {
		out += "\n"
	}
	out += block
	if out == content {
		return Result{Agent: "gitignore", Installed: true, Path: path, Note: "generated entries already current"}
	}
	if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
		return Result{Agent: "gitignore", Path: path, Note: err.Error()}
	}
	return Result{Agent: "gitignore", Installed: true, Path: path, Note: "generated entries written to .gitignore"}
}

// gitignoreBody is the entry list of the generated .gitignore block (between
// the header marker and the close marker). Kept as a named string so the
// canonical block can be matched verbatim for idempotency. The .blueprint
// runtime entries are deliberately absent: that block is owned by
// internal/bpcli/cli's ensureBlueprintRuntimeGitignored (see
// blueprintRuntimeEntries above).
var gitignoreBody = `# Re-run ` + "`kern setup`" + ` to refresh. Unignore a line to commit shared config.
.mcp.json
.claude/
.cursor/
.vscode/
.gemini/
.kiro/
.kern/
.continue/
.windsurf/
.opencode/node_modules/
.opencode/package-lock.json
.cortexkit/
CLAUDE.md
GEMINI.md
.github/copilot-instructions.md
.agents/rules/kern.md
.agents/hooks.json
.github/hooks/
`

// removeGitignoreBlock strips any existing kern-generated .gitignore block
// from content, handling both forms kern has written over time: the marked
// form (gitignoreMarker … closeMarker) and the legacy unmarked form (the
// header marker alone, appended last by older versions). A mismatched block
// (stale entries, edited markers) is removed the same way, so re-running
// setup replaces it in place instead of appending a duplicate.
func removeGitignoreBlock(content, closeMarker string) string {
	// Marked block: header + close marker present. removeMarkedBlock strips
	// between them inclusive; a stale block (markers intact, entries edited)
	// is replaced by this path.
	if strings.Contains(content, gitignoreMarker) && strings.Contains(content, closeMarker) {
		return strutil.RemoveMarkedBlock(content, gitignoreMarker, closeMarker)
	}
	// Legacy unmarked block: header marker without the close marker. Older
	// versions appended the block last, so the kern-owned section runs from
	// the marker to the next blank-line section boundary (or EOF).
	if i := strings.Index(content, gitignoreMarker); i >= 0 {
		end := len(content)
		if j := strings.Index(content[i+len(gitignoreMarker):], "\n\n"); j >= 0 {
			end = i + len(gitignoreMarker) + j
		}
		return strings.TrimRight(content[:i], "\n") + content[end:]
	}
	// Orphaned close marker (the header line was edited away): drop the
	// trailing section containing it.
	if i := strings.Index(content, closeMarker); i >= 0 {
		start := i
		if j := strings.LastIndex(content[:i], "\n\n"); j >= 0 {
			start = j + 1
		}
		return strings.TrimRight(content[:start], "\n")
	}
	return content
}

// gitConfigIgnore resolves the global git ignore path: XDG_CONFIG_HOME/git/ignore
// when XDG_CONFIG_HOME is set, else ~/.config/git/ignore (falling back to
// ./.config/git/ignore when the home dir cannot be resolved). This is the same
// resolution setup's globalConfig helper performs for "git"/"ignore"; it is
// duplicated here (rather than shared) so the leaf stays decoupled from setup.
func gitConfigIgnore() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = "."
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "git", "ignore")
}

// backupFile copies path to path+".bak.<timestamp>" before a rewrite so a
// destructive edit never destroys the user's original content without recourse.
// Duplicated from internal/setup (where wireGlobalRulesFile and the global
// AGENTS/Claude writers also use it) so the leaf stays decoupled from setup.
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

// WireGlobalGitignore ensures that ~/.config/git/ignore (or the custom
// core.excludesfile) contains .kern/ and the blueprint runtime entries so that
// git machine-wide ignores them across ALL repositories. Only the runtime
// paths are added — .blueprint/ config (config.yaml, suppressions.yaml,
// owners.yaml) stays committable.
func WireGlobalGitignore() Result {
	ignorePath := gitConfigIgnore()
	data, err := os.ReadFile(ignorePath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Result{Agent: "global-gitignore", Path: ignorePath, Note: err.Error()}
	}
	content := string(data)
	present := map[string]bool{}
	for _, line := range strings.Split(content, "\n") {
		present[strings.TrimSpace(line)] = true
	}
	var missing []string
	for _, entry := range append([]string{".kern/"}, blueprintRuntimeEntries...) {
		if !present[entry] {
			missing = append(missing, entry)
		}
	}
	if len(missing) == 0 {
		return Result{Agent: "global-gitignore", Installed: true, Path: ignorePath, Note: "global git ignore already configured"}
	}
	if err := os.MkdirAll(filepath.Dir(ignorePath), 0o755); err != nil {
		return Result{Agent: "global-gitignore", Path: ignorePath, Note: err.Error()}
	}
	// Backup before the machine-wide write (C7): even an append-only edit to
	// ~/.config/git/ignore gets a recoverable pre-write copy.
	if len(content) > 0 {
		if berr := backupFile(ignorePath); berr != nil {
			return Result{Agent: "global-gitignore", Path: ignorePath, Note: "backup: " + berr.Error()}
		}
	}
	separator := "\n"
	if len(content) == 0 || strings.HasSuffix(content, "\n") {
		separator = ""
	}
	newContent := content + separator + "# kern global exclude (prevents accidental git commits across all repos)\n" + strings.Join(missing, "\n") + "\n"
	if err := os.WriteFile(ignorePath, []byte(newContent), 0o644); err != nil {
		return Result{Agent: "global-gitignore", Path: ignorePath, Note: err.Error()}
	}
	return Result{Agent: "global-gitignore", Installed: true, Path: ignorePath, Note: "added " + strings.Join(missing, ", ") + " to global git ignore"}
}

// WireLocalGitExclude ensures that <root>/.git/info/exclude contains .kern/ so that
// the current repository ignores .kern/ without dirtying the tracked .gitignore.
func WireLocalGitExclude(root string) Result {
	if root == "" {
		return Result{Agent: "local-gitexclude", Skipped: true, Note: "empty root"}
	}
	gitDir := filepath.Join(root, ".git")
	fi, err := os.Stat(gitDir)
	if err != nil {
		return Result{Agent: "local-gitexclude", Skipped: true, Note: "not a git repository"}
	}
	var infoDir string
	if fi.IsDir() {
		infoDir = filepath.Join(gitDir, "info")
	} else {
		b, err := os.ReadFile(gitDir)
		if err != nil {
			return Result{Agent: "local-gitexclude", Note: err.Error()}
		}
		line := strings.TrimSpace(string(b))
		if strings.HasPrefix(line, "gitdir:") {
			target := strings.TrimSpace(strings.TrimPrefix(line, "gitdir:"))
			if !filepath.IsAbs(target) {
				target = filepath.Join(root, target)
			}
			infoDir = filepath.Join(target, "info")
		} else {
			return Result{Agent: "local-gitexclude", Note: "unrecognized .git pointer"}
		}
	}
	if err := os.MkdirAll(infoDir, 0o755); err != nil {
		return Result{Agent: "local-gitexclude", Path: infoDir, Note: err.Error()}
	}
	excludePath := filepath.Join(infoDir, "exclude")
	b, _ := os.ReadFile(excludePath)
	content := string(b)
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == ".kern" || trimmed == ".kern/" {
			return Result{Agent: "local-gitexclude", Installed: true, Path: excludePath, Note: "local git exclude already configured"}
		}
	}
	separator := "\n"
	if len(content) == 0 || strings.HasSuffix(content, "\n") {
		separator = ""
	}
	newContent := content + separator + "# kern local exclude\n.kern/\n"
	if err := os.WriteFile(excludePath, []byte(newContent), 0o644); err != nil {
		return Result{Agent: "local-gitexclude", Path: excludePath, Note: err.Error()}
	}
	return Result{Agent: "local-gitexclude", Installed: true, Path: excludePath, Note: "added .kern/ to .git/info/exclude"}
}
