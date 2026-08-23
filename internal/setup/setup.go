// Package setup wires kern into code agents with a single command.
//
// It writes the standard project-level .mcp.json (auto-discovered by Claude
// Code, Cursor, Windsurf and most MCP-compatible agents), the opencode
// project/global MCP config, the opencode plugin, and the AGENTS.md usage
// rules. All operations are idempotent: existing entries are left untouched.
package setup

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// mcpName / cliName return the platform-appropriate binary file names. On
// Windows the release binaries are kern-mcp.exe / kern.exe; everywhere else
// they are extensionless. setup resolves sibling and PATH paths with these so a
// Windows install wires the correct .exe into agent configs instead of a bare
// name that the OS cannot resolve.
func mcpName() string {
	if runtime.GOOS == "windows" {
		return "kern-mcp.exe"
	}
	return "kern-mcp"
}

func cliName() string {
	if runtime.GOOS == "windows" {
		return "kern.exe"
	}
	return "kern"
}

// Bin returns the absolute path to the kern-mcp binary that ships next to the
// running executable, falling back to the bare command name (with the platform
// extension, e.g. kern-mcp.exe on Windows).
func Bin() string {
	exe, err := os.Executable()
	if err != nil {
		return mcpName()
	}
	abs := filepath.Join(filepath.Dir(exe), mcpName())
	if _, err := os.Stat(abs); err == nil {
		return abs
	}
	return mcpName()
}

// CLIBin returns the absolute path to the kern CLI binary (the sibling of
// kern-mcp), used by agent hook commands. Falls back to the bare command name
// (with the platform extension, e.g. kern.exe on Windows) when the sibling is
// not found.
func CLIBin() string {
	exe, err := os.Executable()
	if err != nil {
		return cliName()
	}
	abs := filepath.Join(filepath.Dir(exe), cliName())
	if _, err := os.Stat(abs); err == nil {
		return abs
	}
	return cliName()
}

// GlobalMCPCommand returns the command that GLOBAL agent configs should run to
// start kern-mcp. Global configs must survive an upgrade or a change of install
// location, so an absolute os.Executable()-derived path is fragile: if the user
// later installs a new release elsewhere (or the binary that ran setup lives in
// a temp/versioned dir like a brew Cellar), the stale absolute path breaks the
// agent's MCP server. Prefer a PATH-resolved bare "kern-mcp" command — the agent
// resolves it against PATH at launch time, so it always points at whatever kern
// is currently installed. Only fall back to the sibling absolute path when
// kern-mcp is not on PATH at all.
func GlobalMCPCommand() string {
	if p, err := exec.LookPath(mcpName()); err == nil && p != "" {
		return mcpName()
	}
	return Bin()
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
	// AGENTS.md is the universal instruction file — every detected agent
	// reads it natively (Claude, Codex, Gemini, Continue, Windsurf, Zed,
	// Qwen, Qoder, Kiro, opencode). Write it unconditionally so the
	// kern-first policy reaches all agents regardless of which are wired.
	out = append(out, wireAgentRules(root))
	if enabled("opencode") {
		out = append(out, wireOpencode(root, bin))
		out = append(out, wirePlugin(root))
		out = append(out, wireGlobal(GlobalMCPCommand()))
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
