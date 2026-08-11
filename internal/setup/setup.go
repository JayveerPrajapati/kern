// Package setup wires kern into code agents with a single command.
//
// It writes the standard project-level .mcp.json (auto-discovered by Claude
// Code, Cursor, Windsurf and most MCP-compatible agents), the opencode
// project/global MCP config, the opencode plugin, and the AGENTS.md usage
// rules. All operations are idempotent: existing entries are left untouched.
package setup

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

//go:embed assets/plugin/kern.ts
var pluginFS embed.FS

//go:embed assets/AGENTS.md
var rulesFS embed.FS

// Status reports one agent's wiring state.
type Status struct {
	Agent     string
	Installed bool
	Path      string
	Note      string
}

// Bin returns the absolute path to the kern-mcp binary that ships next to the
// running executable, falling back to the bare "kern-mcp" command.
func Bin() string {
	exe, err := os.Executable()
	if err != nil {
		return "kern-mcp"
	}
	abs := filepath.Join(filepath.Dir(exe), "kern-mcp")
	if _, err := os.Stat(abs); err == nil {
		return abs
	}
	return "kern-mcp"
}

// CLIBin returns the absolute path to the kern CLI binary (the sibling of
// kern-mcp), used by agent hook commands. Falls back to the bare "kern"
// command when the sibling is not found.
func CLIBin() string {
	exe, err := os.Executable()
	if err != nil {
		return "kern"
	}
	abs := filepath.Join(filepath.Dir(exe), "kern")
	if _, err := os.Stat(abs); err == nil {
		return abs
	}
	return "kern"
}

// adapter describes a JSON-config agent: where its config lives, which key
// holds servers, and the entry shape.
type adapter struct {
	name  string
	path  func(root string) string
	key   string
	entry func(bin string) map[string]any
}

// adapters is the registry of JSON-config agents supported by setup.
var adapters = []adapter{
	{name: "continue", path: globalConfig("continue", "config.json"), key: "mcpServers", entry: stdioEntry},
	{name: "windsurf", path: globalConfig(".codeium", "windsurf", "mcp_config.json"), key: "mcpServers", entry: stdioEntry},
	{name: "zed", path: globalConfig("zed", "settings.json"), key: "context_servers", entry: cmdEntry},
	{name: "vscode", path: projectConfig(".vscode", "mcp.json"), key: "servers", entry: stdioEntry},
	{name: "cursor", path: projectConfig(".cursor", "mcp.json"), key: "mcpServers", entry: stdioEntry},
	{name: "gemini", path: projectConfig(".gemini", "settings.json"), key: "mcpServers", entry: stdioEntry},
	{name: "antigravity", path: globalConfig(".gemini", "antigravity", "mcp_config.json"), key: "mcpServers", entry: stdioEntry},
	{name: "qwen", path: homeConfig(".qwen", "settings.json"), key: "mcpServers", entry: stdioEntry},
	{name: "qoder", path: homeConfig(".qoder", "mcp.json"), key: "mcpServers", entry: stdioEntry},
	{name: "kiro", path: projectConfig(".kiro", "settings", "mcp.json"), key: "mcpServers", entry: stdioEntry},
	{name: "copilot", path: projectConfig(".vscode", "mcp.json"), key: "servers", entry: stdioEntry},
	{name: "copilot-cli", path: globalConfig(".copilot", "mcp-config.json"), key: "mcpServers", entry: stdioEntry},
}

func stdioEntry(bin string) map[string]any {
	return map[string]any{"type": "stdio", "command": bin, "args": []string{}}
}

func cmdEntry(bin string) map[string]any {
	return map[string]any{"command": bin, "args": []string{}}
}

// globalConfig resolves a path under the user config dir.
func globalConfig(sub ...string) func(string) string {
	return func(_ string) string {
		base := os.Getenv("XDG_CONFIG_HOME")
		if base == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				home = "."
			}
			base = filepath.Join(home, ".config")
		}
		return filepath.Join(append([]string{base}, sub...)...)
	}
}

// projectConfig resolves a path inside the project root.
func projectConfig(sub ...string) func(string) string {
	return func(root string) string {
		return filepath.Join(append([]string{root}, sub...)...)
	}
}

// homeConfig resolves a path directly under the user home dir (used by agents
// that keep their config at ~/.name rather than under ~/.config).
func homeConfig(sub ...string) func(string) string {
	return func(_ string) string {
		home, err := os.UserHomeDir()
		if err != nil {
			home = "."
		}
		return filepath.Join(append([]string{home}, sub...)...)
	}
}

// Check reports the current wiring state without changing anything.
func Check(root string) []Status {
	out := []Status{
		fileStatus(filepath.Join(root, ".mcp.json"), "mcp (project .mcp.json)"),
		fileStatus(filepath.Join(root, "opencode.json"), "opencode (project)"),
		fileStatus(filepath.Join(root, ".opencode", "plugins", "kern.ts"), "opencode plugin"),
		fileStatus(filepath.Join(root, "AGENTS.md"), "AGENTS.md rules"),
		fileStatus(globalOpencodePath(), "opencode (global config)"),
	}
	for _, a := range adapters {
		out = append(out, fileStatus(a.path(root), a.name))
	}
	out = append(out, claudeStatus())
	out = append(out, codexStatus())
	out = append(out, fileStatus(filepath.Join(root, ".claude", "settings.json"), "claude hooks"))
	out = append(out, fileStatus(filepath.Join(root, ".gemini", "settings.json"), "gemini hooks"))
	out = append(out, fileStatus(filepath.Join(root, ".cursor", "rules", "kern-hooks.mdc"), "cursor rule"))

	// Report detected agents and their instruction file status
	detected := DetectAgents(root)
	for _, agent := range detected {
		file, ok := instructionFiles[agent]
		if ok {
			path := filepath.Join(root, file)
			installed := false
			if b, err := os.ReadFile(path); err == nil && (strings.Contains(string(b), "kern usage rules") || strings.Contains(string(b), "kern-instruction:")) {
				installed = true
			}
			mark := "not present"
			if installed {
				mark = "kern-first policy present"
			}
			out = append(out, Status{Agent: agent + " (detected)", Installed: installed, Path: path, Note: mark})
		}
	}

	out = append(out, fileStatus(filepath.Join(root, ".gitignore"), "gitignore (generated block)"))
	return out
}

// Wire configures the requested agents. An empty agents list means all;
// when detect is true, only agents found by DetectAgents are wired.
// Instruction files with the kern-first policy are always written for
// every detected agent that has an instruction file, regardless of the
// agents list — this ensures kern-first enforcement across all present
// platforms without per-agent configuration.
func Wire(root string, agents []string, detect bool) []Status {
	detected := DetectAgents(root)
	if detect && len(agents) == 0 {
		if len(detected) == 0 {
			return []Status{{Agent: "detect", Note: "no agents detected — nothing to wire"}}
		}
		agents = detected
	}
	bin := Bin()
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
	if enabled("mcp") {
		out = append(out, wireMCPJSON(root, bin))
	}
	if enabled("opencode") {
		out = append(out, wireOpencode(root, bin))
		out = append(out, wirePlugin(root))
		out = append(out, wireAgentRules(root))
		out = append(out, wireGlobal(bin))
	}
	if enabled("claude") {
		out = append(out, wireClaude(bin))
		out = append(out, wireClaudeHooks(root, CLIBin()))
	}
	if enabled("codex") {
		out = append(out, wireCodex(bin))
	}
	for _, a := range adapters {
		if enabled(a.name) {
			out = append(out, wireAdapter(a, root, bin))
		}
	}
	if enabled("gemini") {
		out = append(out, wireGeminiHooks(root, CLIBin()))
	}
	if enabled("cursor") {
		out = append(out, wireCursorRules(root))
	}
	out = append(out, gitignoreGenerated(root))

	// Wire kern-first instruction files for every detected platform that
	// has an instruction file. This is independent of the explicit agents
	// list — it ensures the kern-first policy reaches all present agents.
	// detected is computed before any wiring so it reflects what existed
	// before this run, not the configs this run just created.
	out = append(out, wireInstructions(root, detected)...)

	return out
}

func wireAdapter(a adapter, root, bin string) Status {
	path := a.path(root)
	err := mergeJSON(path, a.key, a.entry(bin))
	if err != nil {
		return Status{Agent: a.name, Path: path, Note: err.Error()}
	}
	return Status{Agent: a.name, Installed: true, Path: path, Note: a.name + " config updated"}
}

func wireMCPJSON(root, bin string) Status {
	path := filepath.Join(root, ".mcp.json")
	err := mergeJSON(path, "mcpServers", map[string]any{
		"command": bin,
		"args":    []string{},
	})
	if err != nil {
		return Status{Agent: "mcp", Path: path, Note: err.Error()}
	}
	return Status{Agent: "mcp", Installed: true, Path: path, Note: "project .mcp.json written (auto-discovered by Claude Code, Cursor, Windsurf, …)"}
}

func wireOpencode(root, bin string) Status {
	path := filepath.Join(root, "opencode.json")
	// For the project-level config, prefer the portable relative path "bin/kern-mcp"
	// so it works on any machine after a build. Only fall back to the absolute
	// bin when the relative binary is not present next to the project root.
	cmd := "bin/kern-mcp"
	if _, err := os.Stat(filepath.Join(root, cmd)); err != nil {
		cmd = bin
	}
	entry := map[string]any{
		"type":    "local",
		"command": []string{cmd},
		"enabled": true,
	}
	if cmd == "bin/kern-mcp" {
		entry["cwd"] = "."
	}
	err := mergeJSON(path, "mcp", entry)
	if err != nil {
		return Status{Agent: "opencode", Path: path, Note: err.Error()}
	}
	return Status{Agent: "opencode", Installed: true, Path: path, Note: "opencode.json kern MCP entry present"}
}

func wireGlobal(bin string) Status {
	path := globalOpencodePath()
	err := mergeJSON(path, "mcp", map[string]any{
		"type":    "local",
		"command": []string{bin},
		"enabled": true,
	})
	if err != nil {
		return Status{Agent: "opencode-global", Path: path, Note: err.Error()}
	}
	return Status{Agent: "opencode-global", Installed: true, Path: path, Note: "global opencode MCP entry present"}
}

func wirePlugin(root string) Status {
	dir := filepath.Join(root, ".opencode", "plugins")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Status{Agent: "opencode-plugin", Note: err.Error()}
	}
	path := filepath.Join(dir, "kern.ts")
	src, err := pluginFS.ReadFile("assets/plugin/kern.ts")
	if err != nil {
		return Status{Agent: "opencode-plugin", Path: path, Note: err.Error()}
	}
	// Compare-and-write so re-running setup never torches user edits.
	if cur, rerr := os.ReadFile(path); rerr == nil {
		if bytes.Equal(cur, src) {
			return Status{Agent: "opencode-plugin", Installed: true, Path: path, Note: "plugin already current"}
		}
		return Status{Agent: "opencode-plugin", Path: path, Note: "plugin is customized — left untouched"}
	}
	if err := os.WriteFile(path, src, 0o644); err != nil {
		return Status{Agent: "opencode-plugin", Path: path, Note: err.Error()}
	}
	return Status{Agent: "opencode-plugin", Installed: true, Path: path, Note: "plugin installed"}
}

// hostRuleFiles are the per-agent rule files that peer agents read directly
// (Claude Code: CLAUDE.md, Gemini/CodeBuddy: GEMINI.md). AGENTS.md is the
// universal source; the same single-source block is instantiated into these
// host files so every agent sees the rules, but only when the file already
// exists — setup never creates new rule files unprompted.
var hostRuleFiles = []string{"CLAUDE.md", "GEMINI.md"}

// detectedAgent maps each agent identifier to a detection function.
// An agent is considered "present" when any of its indicators exist.
type agentDetector struct {
	agent  string
	paths  []string // project-relative or home-relative paths to check
	binary string    // optional: binary name to look for on PATH
}

// agentDetectors is the registry of all supported agents for auto-detection.
var agentDetectors = []agentDetector{
	{agent: "opencode", paths: []string{"opencode.json"}, binary: "opencode"},
	{agent: "claude", paths: []string{"CLAUDE.md", ".claude/settings.json"}, binary: "claude"},
	{agent: "cursor", paths: []string{".cursor/mcp.json", ".cursor/rules"}, binary: "cursor"},
	{agent: "copilot", paths: []string{".vscode/mcp.json", ".github/copilot-instructions.md"}, binary: ""},
	{agent: "gemini", paths: []string{"GEMINI.md", ".gemini/settings.json"}, binary: ""},
	{agent: "codex", paths: []string{".codex/config.toml"}, binary: "codex"},
	{agent: "continue", paths: []string{".continuerc.json"}, binary: ""},
	{agent: "windsurf", paths: []string{".windsurfrc", ".codeium/windsurf/mcp_config.json"}, binary: ""},
	{agent: "zed", paths: []string{"~/.config/zed/settings.json"}, binary: ""},
	{agent: "qwen", paths: []string{"~/.qwen/settings.json"}, binary: ""},
	{agent: "qoder", paths: []string{"~/.qoder/mcp.json"}, binary: ""},
	{agent: "kiro", paths: []string{".kiro/settings/mcp.json"}, binary: ""},
}

// instructionFiles maps each agent to its project-level instruction file
// that receives the kern-first policy. These files are git-ignored when
// generated by setup (they carry machine-specific paths or are redundant
// with the universal AGENTS.md).
var instructionFiles = map[string]string{
	"claude":  "CLAUDE.md",
	"gemini":  "GEMINI.md",
	"copilot": ".github/copilot-instructions.md",
	"cursor":  ".cursor/instructions/kern.mdc",
}

// DetectAgents scans the project root and system PATH for evidence that each
// known agent is in use. Returns the list of detected agent names.
func DetectAgents(root string) []string {
	var detected []string
	for _, d := range agentDetectors {
		if isAgentPresent(root, d) {
			detected = append(detected, d.agent)
		}
	}
	return detected
}

func isAgentPresent(root string, d agentDetector) bool {
	for _, p := range d.paths {
		path := p
		if strings.HasPrefix(p, "~") {
			path = homeJoin(strings.TrimPrefix(p, "~"))
		} else {
			path = filepath.Join(root, p)
		}
		if _, err := os.Stat(path); err == nil {
			return true
		}
	}
	if d.binary != "" {
		if _, err := exec.LookPath(d.binary); err == nil {
			return true
		}
	}
	return false
}

func homeJoin(rel string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", rel)
	}
	return filepath.Join(home, rel)
}

// wireInstructions writes the kern-first policy block into each detected
// agent's instruction file, creating the file if missing. Each written
// file receives an idempotency marker so re-running setup doesn't duplicate.
func wireInstructions(root string, agents []string) []Status {
	rules, err := rulesFS.ReadFile("assets/AGENTS.md")
	if err != nil {
		return []Status{{Agent: "instructions", Note: err.Error()}}
	}
	rulesText := string(rules)
	var out []Status
	for _, agent := range agents {
		file, ok := instructionFiles[agent]
		if !ok {
			continue
		}
		path := filepath.Join(root, file)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			out = append(out, Status{Agent: agent + "-instruction", Note: err.Error()})
			continue
		}
		status := writeInstructionFile(path, rulesText)
		status.Agent = agent + "-instruction"
		out = append(out, status)
	}
	return out
}

// writeInstructionFile writes the kern-first policy block to a single
// instruction file, idempotently (never duplicates the block).
func writeInstructionFile(path, rulesText string) Status {
	marker := "<!-- kern-instruction: managed by kern setup; remove marker to stop managing -->"
	content, _ := os.ReadFile(path)
	cur := string(content)
	if strings.Contains(cur, "kern-instruction:") || strings.Contains(cur, "kern usage rules") {
		return Status{Agent: "", Installed: true, Path: path, Note: "kern-first policy already present"}
	}
	var joined string
	if strings.TrimSpace(cur) == "" {
		joined = marker + "\n\n" + rulesText
	} else {
		joined = strings.TrimRight(cur, "\n") + "\n\n" + marker + "\n\n" + rulesText
	}
	if err := os.WriteFile(path, []byte(joined), 0o644); err != nil {
		return Status{Agent: "", Path: path, Note: err.Error()}
	}
	return Status{Agent: "", Installed: true, Path: path, Note: "kern-first policy written"}
}

func wireAgentRules(root string) Status {
	status := wireRulesFile(root, "AGENTS.md")
	// Same content, per host. Errors here are informational: the universal
	// AGENTS.md is the primary delivery mechanism.
	for _, name := range hostRuleFiles {
		path := filepath.Join(root, name)
		if _, err := os.Stat(path); err != nil {
			continue
		}
		wireRulesFile(root, name)
	}
	return status
}

// wireRulesFile appends the embedded kern rules block to a single rule file,
// idempotently (never duplicates) and never overwriting existing content.
func wireRulesFile(root, name string) Status {
	path := filepath.Join(root, name)
	content := ""
	if b, err := os.ReadFile(path); err == nil {
		content = string(b)
	}
	if strings.Contains(content, "kern usage rules") {
		return Status{Agent: name, Installed: true, Path: path, Note: "rules already present"}
	}
	rules, err := rulesFS.ReadFile("assets/AGENTS.md")
	if err != nil {
		return Status{Agent: name, Path: path, Note: err.Error()}
	}
	var joined string
	if content == "" {
		joined = string(rules)
	} else {
		joined = strings.TrimRight(content, "\n") + "\n\n" + string(rules)
	}
	if err := os.WriteFile(path, []byte(joined), 0o644); err != nil {
		return Status{Agent: name, Path: path, Note: err.Error()}
	}
	return Status{Agent: name, Installed: true, Path: path, Note: "rules appended"}
}

func wireClaude(bin string) Status {
	path, err := exec.LookPath("claude")
	if err != nil {
		return Status{Agent: "claude", Note: "claude not on PATH — project .mcp.json still covers Claude Code"}
	}
	cmd := exec.Command(path, "mcp", "add", "kern", "--", bin)
	if out, err := cmd.CombinedOutput(); err != nil {
		return Status{Agent: "claude", Path: path, Note: fmt.Sprintf("claude mcp add failed: %s", strings.TrimSpace(string(out)))}
	}
	return Status{Agent: "claude", Installed: true, Path: path, Note: "claude mcp add ok"}
}

// wireCodex writes the Codex MCP server entry into ~/.codex/config.toml.
// Codex uses TOML rather than JSON, so it gets a small dedicated writer
// instead of the JSON adapters.
func wireCodex(bin string) Status {
	home, err := os.UserHomeDir()
	if err != nil {
		return Status{Agent: "codex", Note: err.Error()}
	}
	path := filepath.Join(home, ".codex", "config.toml")
	needle := "[mcp_servers.kern]"
	if b, err := os.ReadFile(path); err == nil && strings.Contains(string(b), needle) {
		return Status{Agent: "codex", Installed: true, Path: path, Note: "codex config already registers kern"}
	}
	entry := "\n[mcp_servers.kern]\ncommand = \"" + strings.ReplaceAll(bin, `\`, `\\`) + "\"\n"
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Status{Agent: "codex", Path: path, Note: err.Error()}
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return Status{Agent: "codex", Path: path, Note: err.Error()}
	}
	defer f.Close()
	if _, err := f.WriteString(entry); err != nil {
		return Status{Agent: "codex", Path: path, Note: err.Error()}
	}
	return Status{Agent: "codex", Installed: true, Path: path, Note: "codex config updated"}
}

func codexStatus() Status {
	home, err := os.UserHomeDir()
	if err != nil {
		return Status{Agent: "codex", Note: err.Error()}
	}
	path := filepath.Join(home, ".codex", "config.toml")
	if b, err := os.ReadFile(path); err == nil && strings.Contains(string(b), "[mcp_servers.kern]") {
		return Status{Agent: "codex", Installed: true, Path: path, Note: "kern MCP registered"}
	}
	return Status{Agent: "codex", Path: path, Note: "codex config.toml has no kern MCP"}
}

// hookCommand builds the shell command for a kern hook event. Agent hook
// shells expand the per-agent project-dir env var ($CLAUDE_PROJECT_DIR /
// $GEMINI_PROJECT_DIR), keeping the command free of machine-specific paths.
func hookCommand(bin, sub string) string {
	return bin + ` hook ` + sub + ` "$CLAUDE_PROJECT_DIR"`
}

// wireClaudeHooks registers kern's PostToolUse and UserPromptSubmit hooks in
// .claude/settings.json. The PostToolUse hook compresses oversized Bash/Read/
// Grep results and records edits + failures into project memory; the
// UserPromptSubmit hook captures substantive user prompts. Existing hooks are
// preserved; the kern group is merged in only when absent.
func wireClaudeHooks(root, bin string) Status {
	path := filepath.Join(root, ".claude", "settings.json")
	groups := map[string]any{
		"PostToolUse": []any{
			map[string]any{
				"matcher": "Edit|Write|Bash|Read|Grep",
				"hooks": []any{
					map[string]any{"type": "command", "command": hookCommand(bin, "claude-post")},
				},
			},
		},
		"UserPromptSubmit": []any{
			map[string]any{
				"hooks": []any{
					map[string]any{"type": "command", "command": hookCommand(bin, "claude-prompt")},
				},
			},
		},
	}
	if err := mergeHookGroups(path, groups); err != nil {
		return Status{Agent: "claude-hooks", Path: path, Note: err.Error()}
	}
	return Status{Agent: "claude-hooks", Installed: true, Path: path, Note: "claude PostToolUse/UserPromptSubmit hooks registered"}
}

// wireGeminiHooks registers kern's AfterTool and BeforeAgent hooks in
// .gemini/settings.json (Gemini names AfterTool, not PostToolUse; BeforeAgent,
// not UserPromptSubmit). The AfterTool hook compresses oversized shell/read/
// grep results (via exit-2 stderr substitution) and records edits + failures;
// BeforeAgent captures substantive user prompts. Existing hooks and the
// mcpServers key are preserved.
func wireGeminiHooks(root, bin string) Status {
	path := filepath.Join(root, ".gemini", "settings.json")
	groups := map[string]any{
		"AfterTool": []any{
			map[string]any{
				"matcher": "_shell_|read_file|grep|glob",
				"hooks": []any{
					map[string]any{
						"type":        "command",
						"command":     bin + ` hook gemini-after "$GEMINI_PROJECT_DIR"`,
						"name":        "kern-after-tool",
						"description": "kern: compress oversized tool output, record edits and failures",
					},
				},
			},
		},
		"BeforeAgent": []any{
			map[string]any{
				"hooks": []any{
					map[string]any{
						"type":        "command",
						"command":     bin + ` hook gemini-prompt "$GEMINI_PROJECT_DIR"`,
						"name":        "kern-prompt-capture",
						"description": "kern: capture substantive user prompts into project memory",
					},
				},
			},
		},
	}
	if err := mergeHookGroups(path, groups); err != nil {
		return Status{Agent: "gemini-hooks", Path: path, Note: err.Error()}
	}
	return Status{Agent: "gemini-hooks", Installed: true, Path: path, Note: "gemini AfterTool/BeforeAgent hooks registered"}
}

// mergeHookGroups merges hook event groups into a settings JSON file (Claude
// Code and Gemini CLI share the same shape: settings.hooks.<event>[]), creating
// the file when absent and always preserving unrelated keys (mcpServers, other
// hooks). A group already containing a "kern hook" command is left untouched,
// so re-running setup never duplicates hooks.
func mergeHookGroups(path string, groups map[string]any) error {
	var m map[string]any
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := json.Unmarshal(data, &m); err != nil {
			return fmt.Errorf("%s is not valid JSON: %w", path, err)
		}
	case errors.Is(err, os.ErrNotExist):
		m = map[string]any{}
	default:
		return err
	}
	if m == nil {
		m = map[string]any{}
	}
	hooks, _ := m["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	changed := false
	for event, group := range groups {
		existing, _ := hooks[event].([]any)
		if hasKernHook(existing) {
			continue
		}
		// groups values are the matcher-group arrays themselves; append each
		// group so the JSON shape stays hooks.<event>[] — not doubly nested.
		for _, g := range group.([]any) {
			existing = append(existing, g)
		}
		hooks[event] = existing
		changed = true
	}
	if !changed {
		return nil
	}
	m["hooks"] = hooks
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err = json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// hasKernHook reports whether any group in an event's hook groups already
// contains a kern hook command.
func hasKernHook(groups []any) bool {
	for _, g := range groups {
		gm, _ := g.(map[string]any)
		hs, _ := gm["hooks"].([]any)
		for _, h := range hs {
			hm, _ := h.(map[string]any)
			if cmd, _ := hm["command"].(string); strings.Contains(cmd, " hook ") {
				return true
			}
		}
	}
	return false
}

// wireCursorRules writes an instruction rule for Cursor. Cursor's rule events
// cannot execute commands, so the rule tells the model to use kern's MCP tools
// (compression + memory) — the only interception Cursor's platform supports.
func wireCursorRules(root string) Status {
	path := filepath.Join(root, ".cursor", "rules", "kern-hooks.mdc")
	if b, err := os.ReadFile(path); err == nil && strings.Contains(string(b), "kern-hooks: generated by kern setup") {
		return Status{Agent: "cursor-rules", Installed: true, Path: path, Note: "cursor rule already present"}
	}
	content := `---
description: Use kern to compress large outputs and keep session memory
globs:
alwaysApply: false
---
<!-- kern-hooks: generated by kern setup; remove this marker to stop managing the file -->

- When a tool output is very large (roughly more than 4000 characters), do not
  paste it raw into context. Run ` + "`kern_optimize_log`" + ` (or ` + "`kern_context_budget`" + `) on it and
  work from the compressed summary.
- After editing files, record what changed with ` + "`kern_memory_add`" + ` (e.g. "Edited <path>").
- When a command fails, record the failure line with ` + "`kern_memory_add`" + ` so a
  compacted or fresh session can recover the state.
`
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Status{Agent: "cursor-rules", Path: path, Note: err.Error()}
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return Status{Agent: "cursor-rules", Path: path, Note: err.Error()}
	}
	return Status{Agent: "cursor-rules", Installed: true, Path: path, Note: "cursor kern rule written"}
}

// gitignoreMarker identifies the block of generated entries this package owns.
const gitignoreMarker = "# --- kern generated (agent wiring, machine-specific) ---"

// gitignoreGenerated appends the setup-generated project files to .gitignore.
// Machine-specific configs (absolute binary paths in .mcp.json, .claude/,
// .cursor/, .kiro/ hooks) must never be committed; uncommitted copies would
// leak the machine layout and stale paths. But portable configs like
// opencode.json (relative "bin/kern-mcp" command) and the .opencache plugins
// directory ARE committed — only their machine-specific subdirs (node_modules)
// are ignored.
// Idempotent: the block is added once, and only when absent.
func gitignoreGenerated(root string) Status {
	path := filepath.Join(root, ".gitignore")
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Status{Agent: "gitignore", Path: path, Note: err.Error()}
	}
	if strings.Contains(string(data), gitignoreMarker) {
		return Status{Agent: "gitignore", Installed: true, Path: path, Note: "generated entries already gitignored"}
	}
	block := "\n" + gitignoreMarker + `
# Re-run ` + "`kern setup`" + ` to refresh. Unignore a line to commit shared config.
.mcp.json
.claude/
.cursor/
.gemini/
.kiro/
.kern/
.opencode/node_modules/
.opencode/package-lock.json
CLAUDE.md
GEMINI.md
.github/copilot-instructions.md
`
	out := strings.TrimRight(string(data), "\n")
	if out != "" {
		out += "\n"
	}
	out += block
	if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
		return Status{Agent: "gitignore", Path: path, Note: err.Error()}
	}
	return Status{Agent: "gitignore", Installed: true, Path: path, Note: "generated entries added to .gitignore"}
}

// mergeJSON reads a JSON (or JSONC) file, inserts entry under key, and writes
// it back. JSONC comments (// line and /* block */) are stripped before
// parsing so machine-generated config like opencode.jsonc that users may
// annotate with comments still loads. A pre-existing "kern" entry is repaired
// (replaced) when it differs from the current entry — e.g. the binary path
// changed — instead of being left stale, and the file is not rewritten when
// nothing changed.
func mergeJSON(path, key string, entry map[string]any) error {
	var m map[string]any
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		cleaned := stripJSONC(data)
		if err := json.Unmarshal(cleaned, &m); err != nil {
			return fmt.Errorf("%s is not valid JSON: %w", path, err)
		}
	case errors.Is(err, os.ErrNotExist):
		m = map[string]any{}
	default:
		return err
	}
	if m == nil {
		m = map[string]any{}
	}
	existing, _ := m[key].(map[string]any)
	if existing == nil {
		existing = map[string]any{}
	}
	cur, _ := existing["kern"].(map[string]any)
	if mapsEqual(cur, entry) {
		return nil
	}
	existing["kern"] = entry
	m[key] = existing
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err = json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// mapsEqual reports whether two JSON-encodable maps are semantically equal
// (marshal sorts keys, so byte equality is order-independent).
func mapsEqual(a, b map[string]any) bool {
	aj, aerr := json.Marshal(a)
	bj, berr := json.Marshal(b)
	return aerr == nil && berr == nil && string(aj) == string(bj)
}

// stripJSONC removes // line comments and /* block */ comments from JSON data,
// while preserving strings that contain those sequences. This is a minimal
// stripper — it does not validate JSON, just removes comment tokens.
func stripJSONC(data []byte) []byte {
	var out []byte
	inString := false
	escape := false
	i := 0
	for i < len(data) {
		c := data[i]
		if inString {
			out = append(out, c)
			if escape {
				escape = false
			} else if c == '\\' {
				escape = true
			} else if c == '"' {
				inString = false
			}
			i++
			continue
		}
		// Not inside a string.
		if c == '"' {
			inString = true
			out = append(out, c)
			i++
			continue
		}
		// Block comment: /* ... */
		if i+1 < len(data) && c == '/' && data[i+1] == '*' {
			i += 2
			for i+1 < len(data) && !(data[i] == '*' && data[i+1] == '/') {
				i++
			}
			i += 2 // skip closing */
			continue
		}
		// Line comment: // ... until end of line
		if c == '/' && i+1 < len(data) && data[i+1] == '/' {
			for i < len(data) && data[i] != '\n' {
				i++
			}
			continue
		}
		out = append(out, c)
		i++
	}
	return out
}

func fileStatus(path, label string) Status {
	b, err := os.ReadFile(path)
	if err != nil {
		return Status{Agent: label, Path: path, Note: "not present"}
	}
	installed := strings.Contains(string(b), "kern")
	note := "kern entry present"
	if !installed {
		note = "file exists but no kern entry"
	}
	return Status{Agent: label, Installed: installed, Path: path, Note: note}
}

func claudeStatus() Status {
	path, err := exec.LookPath("claude")
	if err != nil {
		return Status{Agent: "claude", Note: "not installed"}
	}
	if b, err := os.ReadFile(claudeConfigPath()); err == nil && strings.Contains(string(b), `"kern"`) {
		return Status{Agent: "claude", Installed: true, Path: path, Note: "kern MCP registered (project or user scope)"}
	}
	return Status{Agent: "claude", Path: path, Note: "claude available — run kern setup to add kern MCP"}
}

func claudeConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude.json")
}

func globalOpencodePath() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "opencode", "opencode.jsonc")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "opencode", "opencode.jsonc")
}
