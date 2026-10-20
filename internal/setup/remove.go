package setup

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/agentwire"
	"github.com/JayveerPrajapati/kern/internal/configstrip"
	"github.com/JayveerPrajapati/kern/internal/gitblocks"
	"github.com/JayveerPrajapati/kern/internal/skills"
	"github.com/JayveerPrajapati/kern/internal/strutil"
)

// RemoveOptions selects what RemoveGlobal undoes.
type RemoveOptions struct {
	// Agents limits removal to the named agents' own config (MCP entry,
	// hooks, rules slot, plugin, skills). Empty means everything machine-wide,
	// including the agent-agnostic pieces (~/AGENTS.md, global git hooks,
	// global git ignore, the shared kern-guard script).
	Agents []string
	// DryRun reports what would be removed and writes nothing.
	DryRun bool
}

// RemoveGlobal is the inverse of WireWith(..., GlobalOnly) plus --global-rules.
// It removes ONLY kern-owned entries and blocks from user-level config:
// everything else in each file is preserved (markdown byte-for-byte apart from
// surrounding blank lines; JSON/TOML semantically, since the installer already
// re-serialized them). A pre-existing file is backed up to <file>.bak.<ts>
// before it is rewritten; a file left empty (kern created it) is deleted;
// an unbalanced or unparseable file is left untouched with a note. Nothing
// under ~/.kern (index, audit, memory) is touched. Idempotent: when nothing
// is found it returns no statuses. Project-scope files written by a plain
// `kern setup` inside a repo are not handled here.
//
// Status meaning: Installed=true is a removal (or, on a dry run, a planned
// one); Skipped=true is something deliberately left in place; neither is a
// failure.
func RemoveGlobal(root string, opts RemoveOptions) []Status {
	r := &remover{dry: opts.DryRun, agents: opts.Agents}
	if bad := r.unknownAgents(root); len(bad) > 0 {
		return bad
	}
	r.claudeMCP()
	r.adapters(root)
	r.opencodeGlobalConfig()
	r.codexTOML()
	r.hookFiles()
	r.globalRules()
	r.plugins()
	r.skills()
	if r.all() {
		r.gitHooks()
		if res := gitblocks.UnwireGlobalGitignore(opts.DryRun); !res.Skipped {
			r.out = append(r.out, Status{Agent: res.Agent, Installed: res.Installed, Path: res.Path, Note: res.Note})
		}
		r.guardScript()
	}
	return r.out
}

type remover struct {
	dry    bool
	agents []string
	out    []Status
}

func (r *remover) all() bool { return len(r.agents) == 0 }

func (r *remover) want(name string) bool {
	if r.all() {
		return true
	}
	for _, a := range r.agents {
		if a == name {
			return true
		}
	}
	return false
}

func (r *remover) add(agent, path string, removed bool, note string) {
	r.out = append(r.out, Status{Agent: agent, Installed: removed, Skipped: !removed, Path: path, Note: note})
}

func (r *remover) fail(agent, path string, err error) {
	r.out = append(r.out, Status{Agent: agent, Path: path, Note: err.Error()})
}

func (r *remover) unknownAgents(root string) []Status {
	known := map[string]bool{}
	for _, d := range agentDetectors {
		known[d.agent] = true
	}
	all, _ := effectiveAdapters(root)
	for _, a := range all {
		known[a.name] = true
	}
	var bad []Status
	for _, a := range r.agents {
		if !known[a] {
			bad = append(bad, Status{Agent: a, Note: "unknown agent " + fmt.Sprintf("%q", a) + " — nothing removed"})
		}
	}
	return bad
}

// editText applies fn to the file at path. fn returns the new text and
// whether anything changed. An fn error leaves the file untouched.
func (r *remover) editText(agent, path string, fn func(string) (string, bool, error)) {
	b, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			r.fail(agent, path, err)
		}
		return
	}
	next, changed, err := fn(string(b))
	if err != nil {
		r.add(agent, path, false, "left untouched: "+err.Error())
		return
	}
	if !changed {
		return
	}
	if strings.TrimSpace(next) == "" {
		if r.dry {
			r.add(agent, path, true, "[dry-run] would delete "+path+" (held only kern content)")
			return
		}
		if err := os.Remove(path); err != nil {
			r.fail(agent, path, err)
			return
		}
		r.add(agent, path, true, "deleted "+path+" (held only kern content)")
		return
	}
	if r.dry {
		r.add(agent, path, true, "[dry-run] would remove the kern entries from "+path)
		return
	}
	if err := backupFile(path); err != nil {
		r.fail(agent, path, fmt.Errorf("backup: %w", err))
		return
	}
	mode := os.FileMode(0o600)
	if info, serr := os.Stat(path); serr == nil {
		mode = info.Mode().Perm()
	}
	if err := os.WriteFile(path, []byte(next), mode); err != nil {
		r.fail(agent, path, err)
		return
	}
	r.add(agent, path, true, "removed the kern entries from "+path+" (backup: "+filepath.Base(path)+".bak.<timestamp>)")
}

// deleteFile removes a wholly kern-owned file.
func (r *remover) deleteFile(agent, path, why string) {
	if _, err := os.Stat(path); err != nil {
		return
	}
	if r.dry {
		r.add(agent, path, true, "[dry-run] would delete "+path+" ("+why+")")
		return
	}
	if err := os.Remove(path); err != nil {
		r.fail(agent, path, err)
		return
	}
	_ = os.Remove(filepath.Dir(path)) // only succeeds when empty
	r.add(agent, path, true, "deleted "+path+" ("+why+")")
}

// ---- per-surface removers ----

// claudeMCP undoes `claude mcp add kern`, which lives in ~/.claude.json (a
// file Claude Code rewrites constantly), so it goes through the claude CLI.
func (r *remover) claudeMCP() {
	if !r.want("claude") || !claudeHasKern() {
		return
	}
	path, err := exec.LookPath("claude")
	if err != nil {
		r.add("claude-mcp", claudeConfigPath(), false, "claude is not on PATH — run `claude mcp remove kern` yourself")
		return
	}
	if r.dry {
		r.add("claude-mcp", path, true, "[dry-run] would run `claude mcp remove kern` (this directory's scope and user scope)")
		return
	}
	removed := false
	for _, args := range [][]string{{"mcp", "remove", "kern"}, {"mcp", "remove", "kern", "-s", "user"}} {
		if exec.Command(path, args...).Run() == nil {
			removed = true
		}
	}
	if removed {
		r.add("claude-mcp", path, true, "ran `claude mcp remove kern`")
		return
	}
	r.add("claude-mcp", path, false, "no kern MCP entry in this directory's claude scope — if you added it from another project, run `claude mcp remove kern` there")
}

func (r *remover) adapters(root string) {
	all, _ := effectiveAdapters(root)
	seen := map[string]bool{}
	for _, a := range all {
		if a.scope != "global" || !r.want(a.name) {
			continue
		}
		p := a.path(root)
		if k := p + "\x00" + a.key; seen[k] {
			continue
		} else {
			seen[k] = true
		}
		r.editText(a.name, p, configstrip.ServerKey(a.key))
	}
}

func (r *remover) opencodeGlobalConfig() {
	if r.want("opencode") {
		r.editText("opencode-global", globalOpencodePath(), configstrip.ServerKey("mcp"))
	}
}

// codexTOML removes the [mcp_servers.kern*] tables via configstrip.CodexTOML.
// The `[features] codex_hooks = true` flag is left alone: the installer only
// adds it when absent, and a user may rely on it.
func (r *remover) codexTOML() {
	if r.want("codex") {
		r.editText("codex", filepath.Join(globalHomeDir(), ".codex", "config.toml"), configstrip.CodexTOML)
	}
}

func (r *remover) hookFiles() {
	home := globalHomeDir()
	for _, h := range []struct {
		agent, path string
		fn          func(string) (string, bool, error)
	}{
		{"claude", filepath.Join(home, ".claude", "settings.json"), configstrip.Hooks},
		{"gemini", filepath.Join(home, ".gemini", "settings.json"), configstrip.Hooks},
		{"antigravity", filepath.Join(home, ".gemini", "config", "hooks.json"), configstrip.AntigravityHook},
		{"cursor", filepath.Join(home, ".cursor", "hooks.json"), configstrip.Hooks},
		{"copilot", filepath.Join(home, ".copilot", "hooks", "kern-pretooluse.json"), configstrip.Hooks},
		{"codex", filepath.Join(home, ".codex", "hooks.json"), configstrip.Hooks},
		{"qwen", filepath.Join(home, ".qwen", "settings.json"), configstrip.Hooks},
		{"qoder", filepath.Join(home, ".qoder", "settings.json"), configstrip.Hooks},
	} {
		if r.want(h.agent) {
			r.editText(h.agent+"-hooks", h.path, h.fn)
		}
	}
	if r.want("continue") {
		// continueSettingsPath() depends on whether config.json exists, which
		// the adapter pass may just have deleted, so check every candidate.
		seen := map[string]bool{}
		for _, p := range []string{
			continueSettingsPath(),
			filepath.Join(home, ".config", "continue", "settings.json"),
			filepath.Join(home, ".continue", "settings.json"),
		} {
			if !seen[p] {
				seen[p] = true
				r.editText("continue-hooks", p, configstrip.Hooks)
			}
		}
	}
	if r.want("kiro") {
		// Kern-owned file, written wholesale; only delete it if it is ours.
		if b, err := os.ReadFile(kiroHooksPath()); err == nil && bytes.Contains(b, []byte("kern-guard")) {
			r.deleteFile("kiro-hooks", kiroHooksPath(), "kern-owned hooks file")
		}
	}
}

// stripRulesText excises every kern-managed rules block (marker-delimited and
// unmarked), normalizing the blank lines around the cut. An unbalanced block
// is an error so the file is left untouched.
func stripRulesText(cur string) (string, bool, error) {
	cleaned := strutil.RemoveMarkedBlock(cur, globalRulesMarkerOpen, globalRulesMarkerClose)
	cleaned, err := removeKernSection(cleaned)
	if err != nil {
		return "", false, err
	}
	if cleaned == cur {
		return cur, false, nil
	}
	cleaned = strings.Trim(cleaned, "\r\n")
	if cleaned == strings.TrimSpace(cursorFrontmatter) {
		cleaned = "" // only the frontmatter kern added for its own .mdc rule
	}
	if cleaned != "" {
		cleaned += "\n"
	}
	return cleaned, true, nil
}

func (r *remover) globalRules() {
	agentOf := []string{"claude", "codex", "opencode", "cursor", "gemini", "windsurf", "copilot"}
	paths := GlobalRulesPaths()
	seen := map[string]bool{}
	for i, p := range paths {
		seen[p] = true
		if r.want(agentOf[i]) {
			r.editText("global-rules-"+agentOf[i], p, stripRulesText)
		}
	}
	// Writers behind `kern setup --global` also touch these two.
	if r.want("claude") && !seen[globalClaudePath()] {
		r.editText("global-rules-claude", globalClaudePath(), stripRulesText)
	}
	if r.all() {
		r.editText("global-AGENTS.md", globalAGENTSPath(), stripRulesText)
	}
}

func (r *remover) plugins() {
	if !r.want("opencode") {
		return
	}
	src, err := pluginFS.ReadFile("assets/plugin/kern.ts")
	if err != nil {
		return
	}
	for _, p := range GlobalPluginPaths() {
		cur, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if bytes.Equal(cur, src) || isShippedPluginVersion(cur) {
			r.deleteFile("opencode-plugin-global", p, "kern's plugin")
			continue
		}
		r.add("opencode-plugin-global", p, false, "plugin is customized — left in place")
	}
}

// skillTargetAgent maps a global skills location to the agent that owns it
// ("" for the shared ~/.agents dir, removed only on a full remove).
func skillTargetAgent(homeDirCheck string) string {
	switch filepath.ToSlash(homeDirCheck) {
	case ".gemini":
		return "antigravity"
	case ".claude":
		return "claude"
	case ".cursor":
		return "cursor"
	case ".config/opencode", ".opencode":
		return "opencode"
	case ".codex":
		return "codex"
	case ".qwen":
		return "qwen"
	case ".qoder":
		return "qoder"
	case ".copilot":
		return "copilot"
	}
	return ""
}

func (r *remover) skills() {
	home := globalHomeDir()
	for _, t := range agentwire.GlobalSkillTargets {
		if agent := skillTargetAgent(t.HomeDirCheck); agent != "" {
			if !r.want(agent) {
				continue
			}
		} else if !r.all() {
			continue
		}
		dir := filepath.Join(home, t.SkillsDir)
		var del, kept []string
		if t.IsCursor {
			del = cursorSkillFiles(dir)
		} else {
			del, kept = kernSkillFiles(dir)
		}
		if len(del) == 0 && len(kept) == 0 {
			continue
		}
		if !r.dry {
			for _, p := range del {
				if err := os.Remove(p); err != nil {
					r.fail(t.Agent, p, err)
					return
				}
			}
			pruneEmptySkillDirs(dir)
		}
		note := fmt.Sprintf("removed %d kern skill file(s) from %s", len(del), dir)
		if r.dry {
			note = fmt.Sprintf("[dry-run] would remove %d kern skill file(s) from %s", len(del), dir)
		}
		if len(kept) > 0 {
			note += fmt.Sprintf("; %d modified file(s) left in place", len(kept))
		}
		r.add(t.Agent, dir, len(del) > 0, note)
	}
}

// kernSkillFiles lists installed kern skill files under dir that are
// byte-identical to the embedded ones (to delete) and ones that differ (kept:
// user-modified or from another kern version).
func kernSkillFiles(dir string) (del, kept []string) {
	check := func(path string, want []byte) {
		got, err := os.ReadFile(path)
		if err != nil {
			return
		}
		if bytes.Equal(got, want) {
			del = append(del, path)
		} else {
			kept = append(kept, path)
		}
	}
	for _, name := range skills.SkillNames {
		if data, err := skills.ReadSkill(name); err == nil {
			check(filepath.Join(dir, name, "SKILL.md"), data)
		}
		scripts, err := skills.ListSkillScripts(name)
		if err != nil {
			continue
		}
		for _, s := range scripts {
			if data, err := skills.ReadSkillScript(name, s); err == nil {
				check(filepath.Join(dir, name, "scripts", s), data)
			}
		}
	}
	return del, kept
}

// cursorSkillFiles lists the .mdc rules kern generated (carrying its marker).
func cursorSkillFiles(dir string) []string {
	var del []string
	for _, name := range skills.SkillNames {
		p := filepath.Join(dir, name+".mdc")
		if b, err := os.ReadFile(p); err == nil && bytes.Contains(b, []byte("<!-- kern-skill: generated by kern setup -->")) {
			del = append(del, p)
		}
	}
	return del
}

// pruneEmptySkillDirs removes directories the skills install created, but
// only when they are empty (os.Remove refuses otherwise).
func pruneEmptySkillDirs(dir string) {
	for _, name := range skills.SkillNames {
		_ = os.Remove(filepath.Join(dir, name, "scripts"))
		_ = os.Remove(filepath.Join(dir, name))
	}
	_ = os.Remove(dir)
}

func (r *remover) gitHooks() {
	res, err := RemoveGlobalGitHooks(r.dry)
	dir, _ := GlobalGitHooksDir()
	if err != nil {
		r.fail("global-git-hooks", dir, err)
		return
	}
	verb := ""
	if r.dry {
		verb = "[dry-run] would "
	}
	if res.UnsetHooksPath != "" {
		r.add("global-git-hooks", dir, true, verb+"unset git core.hooksPath (was "+res.UnsetHooksPath+")")
	}
	if res.Foreign != "" {
		r.add("global-git-hooks", res.Foreign, false, "git core.hooksPath points to "+res.Foreign+", not kern's — left untouched")
	}
	if len(res.Removed) > 0 {
		r.add("global-git-hooks", dir, true, fmt.Sprintf("%sremove %d kern git hook file(s) from %s", verb, len(res.Removed), dir))
	}
	if len(res.Kept) > 0 {
		r.add("global-git-hooks", dir, false, fmt.Sprintf("%d non-kern file(s) in %s left in place", len(res.Kept), dir))
	}
}

func (r *remover) guardScript() {
	p := filepath.Join(globalHomeDir(), ".kern", "hooks", "kern-guard.sh")
	r.deleteFile("kern-guard", p, "shared hook script; no agent hook references it any more")
}
