// Package setup wires kern into code agents with a single command.
// It writes the standard project-level .mcp.json (auto-discovered by Claude
// Code, Cursor, Windsurf and most MCP-compatible agents), the opencode
// project/global MCP config, the opencode plugin, and the AGENTS.md usage
// rules. All operations are idempotent: existing entries are left untouched.
package setup

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/agentwire"
	"github.com/JayveerPrajapati/kern/internal/gitblocks"
)

// cliName returns the platform-appropriate CLI binary file name. On Windows
// the release binary is kern.exe; everywhere else it is extensionless.
func cliName() string {
	if runtime.GOOS == "windows" {
		return "kern.exe"
	}
	return "kern"
}

// Bin returns the absolute path to the kern-mcp binary that ships next to the
// running executable, falling back to the bare command name (with the platform
// extension, e.g. kern-mcp.exe on Windows). Implemented in internal/agentwire.
func Bin() string {
	return agentwire.Bin()
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

// PortableMCPCommand returns the command that agent configs should run to
// start kern-mcp. Prefer a PATH-resolved bare "kern-mcp" (the agent re-resolves
// it against PATH at launch, so it works on any machine and survives relocation);
// fall back to the sibling absolute path only when kern-mcp is not on PATH.
// Implemented in internal/agentwire.
func PortableMCPCommand() string {
	return agentwire.PortableMCPCommand()
}

// GlobalMCPCommand returns the command that GLOBAL agent configs should run to
// start kern-mcp. Global configs are machine-specific (never committed), and
// GUI-launched hosts (Claude Code, Cursor, Gemini, Qwen, Codex, Zed on macOS)
// do NOT inherit the shell PATH — a bare "kern-mcp" written by a setup run from
// a terminal is unresolvable at host launch, so the MCP server silently never
// starts and agents fall back to built-in tools. Use an absolute command for
// global configs; the install directory is stable across kernel updates.
// Project configs keep the portable bare command (see PortableMCPCommand).
func GlobalMCPCommand() string { return agentwire.LaunchMCPCommand() }

// LaunchCLICommand returns the absolute `kern` command for machine-global hook
// settings. Hooks run in GUI-launched hosts with a minimal PATH too, so a bare
// `kern` there fails open silently and kern-first steering is lost.
func LaunchCLICommand() string { return agentwire.LaunchCLICommand() }

// PortableCLICommand returns the portable kern CLI command for agent hooks.
// Prefer a PATH-resolved bare "kern"; fall back to the sibling absolute path
// only when kern is not on PATH.
func PortableCLICommand() string {
	if p, err := exec.LookPath(cliName()); err == nil && p != "" {
		return cliName()
	}
	return CLIBin()
}

// adapter describes a JSON-config agent: where its config lives, which key
// holds servers, and the entry shape.
type adapter struct {
	name  string
	path  func(root string) string
	key   string
	entry func(bin string) map[string]any
	// scope marks where the config lives: "global" (home/global config dir,
	// safe to pre-wire for ALL agents regardless of detection — an agent
	// installed later is already wired) or "repo" (project-root file, only
	// wired for detected/present agents).
	scope string
}

// adapters is the registry of JSON-config agents supported by setup.
var adapters = []adapter{
	{name: "continue", path: globalConfig("continue", "config.json"), key: "mcpServers", entry: stdioEntry, scope: "global"},
	{name: "windsurf", path: globalConfig(".codeium", "windsurf", "mcp_config.json"), key: "mcpServers", entry: stdioEntry, scope: "global"},
	{name: "zed", path: globalConfig("zed", "settings.json"), key: "context_servers", entry: cmdEntry, scope: "global"},
	{name: "vscode", path: projectConfig(".vscode", "mcp.json"), key: "servers", entry: stdioEntry, scope: "repo"},
	{name: "cursor", path: homeConfig(".cursor", "mcp.json"), key: "mcpServers", entry: stdioEntry, scope: "global"},
	{name: "gemini", path: homeConfig(".gemini", "settings.json"), key: "mcpServers", entry: stdioEntry, scope: "global"},
	{name: "antigravity", path: homeConfig(".gemini", "config", "mcp_config.json"), key: "mcpServers", entry: stdioEntry, scope: "global"},
	{name: "qwen", path: homeConfig(".qwen", "settings.json"), key: "mcpServers", entry: stdioEntry, scope: "global"},
	{name: "qoder", path: homeConfig(".qoder", "settings.json"), key: "mcpServers", entry: stdioEntry, scope: "global"},
	{name: "kiro", path: homeConfig(".kiro", "settings", "mcp.json"), key: "mcpServers", entry: stdioEntry, scope: "global"},
	{name: "copilot", path: projectConfig(".vscode", "mcp.json"), key: "servers", entry: stdioEntry, scope: "repo"},
	{name: "copilot", path: homeConfig(".copilot", "mcp-config.json"), key: "mcpServers", entry: stdioEntry, scope: "global"},
}

func stdioEntry(bin string) map[string]any {
	return map[string]any{
		"type":    "stdio",
		"command": bin,
		"args":    []string{},
		// No KERN_ALLOW_EXEC default (C1): exec is opt-in per host. Writing it
		// here silently enabled ungoverned host command execution for every
		// agent that opened the repo — the exact opposite of the documented
		// fail-closed contract. MergeJSON replaces the kern entry wholesale, so
		// re-running setup also strips a stale KERN_ALLOW_EXEC from prior runs.
		"env": map[string]string{},
	}
}

func cmdEntry(bin string) map[string]any {
	return map[string]any{
		"command": bin,
		"args":    []string{},
		"env":     map[string]string{},
	}
}

// pluginGuardStatus reports whether the opencode plugin carries the shadow
// routing + post-compression enforcement (the tool.execute.after hook) — the
// opencode counterpart of the shell PreToolUse guard wired for Claude Code,
// Gemini, Codex and friends.
func pluginGuardStatus(root string) Status {
	p := filepath.Join(root, ".opencode", "plugins", "kern.ts")
	b, err := os.ReadFile(p)
	if err != nil {
		return Status{Agent: "opencode guard", Path: p, Note: "plugin not found — run kern setup"}
	}
	if strings.Contains(string(b), `"tool.execute.after"`) {
		return Status{Agent: "opencode guard", Installed: true, Path: p, Note: "shadow routing + post-compression (tool.execute.after)"}
	}
	return Status{Agent: "opencode guard", Path: p, Note: "legacy plugin — re-run kern setup"}
}

// globalPluginStatus reports one GLOBAL opencode plugin copy with an
// actionable remediation hint: a missing copy must name `kern setup --global` (the only
// writer of global copies — plain `kern setup` never touches them), instead
// of the generic "not present" note that reads as benign next to the project
// copy's "plugin already current" line. Present copies use the same policy
// vocabulary as copyGlobalPlugin: current, customized, or an older shipped
// version.
func globalPluginStatus(path string) Status {
	if _, err := os.Stat(filepath.Dir(filepath.Dir(path))); err != nil {
		return Status{Agent: "opencode plugin (global)", Skipped: true, Path: path, Note: "opencode not installed — skipped"}
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return Status{Agent: "opencode plugin (global)", Path: path, Note: "global copy missing — run kern setup --global to install it"}
	}
	if asset, aerr := PluginAsset(); aerr == nil && bytes.Equal(b, asset) {
		return Status{Agent: "opencode plugin (global)", Installed: true, Path: path, Note: "global plugin already current"}
	}
	if !isShippedPluginVersion(b) {
		return Status{Agent: "opencode plugin (global)", Installed: true, Path: path, Note: "global plugin is customized — the next `kern setup --global` backs it up and refreshes it"}
	}
	return Status{Agent: "opencode plugin (global)", Installed: true, Path: path, Note: "global plugin is an older kern version — run kern setup --global to refresh"}
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
	out = append(out, agentwire.GlobalRulesStatus()...)
	for _, p := range GlobalPluginPaths() {
		out = append(out, globalPluginStatus(p))
	}
	out = append(out, pluginGuardStatus(root))
	allAdapters, customErrs := effectiveAdapters(root)
	for _, err := range customErrs {
		out = append(out, Status{Agent: "custom adapters", Note: err.Error()})
	}
	for _, a := range allAdapters {
		out = append(out, fileStatus(a.path(root), a.name))
	}
	out = append(out, claudeStatus())
	out = append(out, codexStatus())
	out = append(out, fileStatusAny("claude hooks", filepath.Join(root, ".claude", "settings.json"), filepath.Join(globalHomeDir(), ".claude", "settings.json")))
	out = append(out, fileStatusAny("gemini hooks", filepath.Join(root, ".gemini", "settings.json"), filepath.Join(globalHomeDir(), ".gemini", "settings.json")))
	out = append(out, fileStatus(filepath.Join(root, ".cursor", "rules", "kern-hooks.mdc"), "cursor rule"))
	out = append(out, fileStatusAny("cursor hooks", filepath.Join(root, ".cursor", "hooks.json"), filepath.Join(globalHomeDir(), ".cursor", "hooks.json")))
	out = append(out, fileStatusAny("copilot hooks", filepath.Join(root, ".github", "hooks", "kern-pretooluse.json"), filepath.Join(globalHomeDir(), ".copilot", "hooks", "kern-pretooluse.json")))
	out = append(out, fileStatus(filepath.Join(homeConfig(".codex", "hooks.json")("")), "codex hooks"))
	out = append(out, fileStatus(filepath.Join(homeConfig(".qwen", "settings.json")("")), "qwen hooks"))
	out = append(out, fileStatus(filepath.Join(homeConfig(".qoder", "settings.json")("")), "qoder hooks"))
	out = append(out, fileStatus(continueSettingsPath(), "continue hooks"))
	out = append(out, fileStatus(kiroHooksPath(), "kiro hooks"))
	out = append(out, fileStatusAny("antigravity hooks", filepath.Join(root, ".agents", "hooks.json"), filepath.Join(globalHomeDir(), ".gemini", "config", "hooks.json")))
	out = append(out, checkGlobalGitHooks())
	out = append(out, checkHookMatcherCompleteness(root)...)

	// Report detected agents and their instruction file status
	detected := DetectAgents(root)
	for _, agent := range detected {
		file, ok := instructionFiles[agent]
		if !ok {
			continue
		}
		path := filepath.Join(root, file)
		installed := instructionPolicyPresent(path)
		if !installed {
			// F10: older kern releases wrote other paths (Cursor's
			// .cursor/instructions/kern.mdc). A legacy install still
			// carries the kern-first policy — accept it instead of
			// warning about a file this setup never wrote.
			if legacy, ok := legacyInstructionFiles[agent]; ok && instructionPolicyPresent(filepath.Join(root, legacy)) {
				path = filepath.Join(root, legacy)
				installed = true
			}
		}
		mark := "not present"
		if installed {
			mark = "kern-first policy present"
		}
		out = append(out, Status{Agent: agent + " (detected)", Installed: installed, Path: path, Note: mark})
	}

	out = append(out, agentwire.CheckSkills(root)...)
	out = append(out, fileStatus(filepath.Join(root, ".gitignore"), "gitignore (generated block)"))
	out = append(out, fileStatus(filepath.Join(root, ".git", "info", "exclude"), "git local exclude"))
	out = append(out, fileStatus(globalConfig("git", "ignore")(""), "git global ignore"))
	out = append(out, checkWrapperFreshness()...)
	return out
}

// Wire configures the requested agents with default options. An empty agents
// list means all; when detect is true, only agents found by DetectAgents are
// wired. Instruction files with the kern-first policy are always written for
// every detected agent that has an instruction file, regardless of the
// agents list — this ensures kern-first enforcement across all present
// platforms without per-agent configuration.
// global gates every USER-GLOBAL write (home-scoped hook files, home/global
// MCP adapters, global opencode config/plugin, global git ignore, editor
// exclusions, global skills): project wiring (`kern setup` without
// --global) must never touch user-global config. With global=true
// the GLOBAL-scoped configs are pre-wired for EVERY agent regardless of
// detection: an agent installed later (or used from a different machine) is
// already wired with no re-run. Detection only gates per-repo files
// (.mcp.json, opencode.json, cursor rules, vscode/copilot adapters,
// instruction files).
func Wire(root string, agents []string, detect bool, global bool) []Status {
	return WireWith(root, agents, detect, global, WireOptions{})
}

// WireOptions carries per-run knobs for WireWith. Zero value matches Wire's
// default behavior exactly.
type WireOptions struct {
	// AgentsMD selects the repo AGENTS.md variant: "thin" writes the thin
	// wiring-only file (full kern rules live in the host's global
	// instructions, managed by `kern setup --global-rules`), "full" writes
	// the complete rules, "" uses the persisted .kern/config.json value. The
	// absent-key default is resolved PER FILE by wireAgentRules: thin when
	// the corresponding host's global slot carries the kern-managed block,
	// full otherwise (an unmanaged global slot means the repo file is the
	// only delivery). An explicit non-empty value is persisted so subsequent
	// runs remember it.
	AgentsMD string
	// DryRun suppresses every write and returns preview statuses whose
	// notes describe what a real run would do ("[dry-run] would …"), so
	// `kern setup --dry-run` never mutates the project. No status may
	// claim a write happened.
	DryRun bool
	// GlobalOnly (requires global=true) wires ONLY user-level state — home
	// MCP adapters, hooks, global rules/git ignore/skills — and writes no file
	// under the project root. Repos then need no setup: the globally
	// registered MCP server indexes a repo on first use.
	GlobalOnly bool
}

// WireWith configures the requested agents like Wire, with per-run options.
func WireWith(root string, agents []string, detect bool, global bool, opts WireOptions) []Status {
	detected := DetectAgents(root)
	explicit := agents // snapshot: explicit --agents list, if any
	bin := PortableMCPCommand()
	// repoEnabled gates project-scope files: explicit list wins; else
	// detected agents (when detect); else all.
	repoEnabled := func(name string) bool {
		if len(explicit) > 0 {
			for _, a := range explicit {
				if a == name {
					return true
				}
			}
			return false
		}
		if detect {
			for _, a := range detected {
				if a == name {
					return true
				}
			}
			return false
		}
		return true
	}
	// globalEnabled gates user-scope configs: explicit list wins; else ALL
	// agents — never detection-gated, so an agent installed later is already
	// wired globally. It only matters when global is true.
	globalEnabled := func(name string) bool {
		if len(explicit) > 0 {
			for _, a := range explicit {
				if a == name {
					return true
				}
			}
			return false
		}
		return true
	}
	var out []Status
	// AGENTS.md is the universal instruction file — every detected agent
	// reads it natively (Claude, Codex, Gemini, Continue, Windsurf, Zed,
	// Qwen, Qoder, Kiro, opencode). Write it unconditionally so the
	// kern-first policy reaches all agents regardless of which are wired.
	// The variant is thin by default (low startup tokens: wiring-only facts,
	// full rules live in the host's global instructions); an explicit
	// --agents-md choice is persisted in .kern/config.json so subsequent
	// runs remember it.
	mode := opts.AgentsMD
	explicitThin := opts.AgentsMD == "thin"
	if mode == "" {
		mode = agentsMDMode(root)
		// A persisted agents_md choice is a user decision; the absent-key
		// default is not, and must not downgrade a full rules file.
		explicitThin = agentsMDPersisted(root) && mode == "thin"
	}
	wired := "all"
	if len(explicit) > 0 {
		wired = strings.Join(explicit, ", ")
	} else if detect {
		if len(detected) == 0 {
			wired = "none"
		} else {
			wired = strings.Join(detected, ", ")
		}
	}

	if opts.DryRun && opts.GlobalOnly {
		return []Status{{Agent: "global", Installed: true, Note: "[dry-run] would wire user-level MCP + hooks for every supported agent, plus global git ignore/hooks and skills — no repo files, no home files changed"}}
	}
	if opts.DryRun {
		// F10: --dry-run must not write anything. Mirror the wiring
		// sequence below — same gates, same target paths — and report
		// each step as a would-write preview. Keep this step list in
		// sync with the real sequence below.
		would := func(agent, path, action string) Status {
			note := "[dry-run] would " + action
			if _, err := os.Stat(path); err == nil {
				note += " (already present — would refresh if stale)"
			}
			return Status{Agent: agent, Installed: true, Path: path, Note: note}
		}
		seenPath := map[string]bool{}
		wouldOnce := func(agent, path, action string) {
			if seenPath[path] {
				return
			}
			seenPath[path] = true
			out = append(out, would(agent, path, action))
		}
		out = append(out, would("mcp", filepath.Join(root, ".mcp.json"), "write .mcp.json (universal MCP config)"))
		// Mirror WireRuleVariant's actual outcome, not just the resolved
		// variant: an existing FULL rules file with no explicit thin choice
		// is KEPT, so the preview must not announce a thin write it would
		// not perform.
		agentsMDPath := filepath.Join(root, "AGENTS.md")
		keepFull := false
		if agentwire.RulesVariant("AGENTS.md", mode, explicitThin) == "thin" && !explicitThin {
			if b, err := os.ReadFile(agentsMDPath); err == nil {
				keepFull = strings.Contains(string(b), agentwire.FullRulesHeader)
			}
		}
		if keepFull {
			out = append(out, Status{Agent: "AGENTS.md", Installed: true, Path: agentsMDPath, Note: "[dry-run] would keep existing full rules (pass --agents-md thin to switch)"})
		} else {
			out = append(out, would("AGENTS.md", agentsMDPath, "write AGENTS.md ("+agentwire.RulesVariant("AGENTS.md", mode, explicitThin)+" rules)"))
		}
		if repoEnabled("opencode") {
			out = append(out, would("opencode", filepath.Join(root, "opencode.json"), "merge the kern MCP entry into opencode.json"))
			out = append(out, would("opencode-plugin", filepath.Join(root, ".opencode", "plugins", "kern.ts"), "install/refresh the opencode plugin"))
		}
		if global {
			// User-global targets (home MCP adapters, hooks, global git
			// ignore/skills) are enumerated by a dozen writer functions
			// with internal paths; rather than duplicating them here
			// (drift risk), report one honest summary line.
			out = append(out, Status{Agent: "global", Installed: true, Note: "[dry-run] would wire user-global configs (home MCP adapters, hooks, global git ignore + skills) — no home files changed"})
		} else {
			previewAdapters, _ := effectiveAdapters(root)
			for _, a := range previewAdapters {
				if a.scope != "repo" || !repoEnabled(a.name) {
					continue
				}
				wouldOnce(a.name, a.path(root), "merge the kern "+a.key+" entry into the "+a.name+" config")
			}
			for _, a := range previewAdapters {
				if a.scope != "repo" || !repoEnabled(a.name) {
					continue
				}
				switch a.name {
				case "continue":
					out = append(out, would("continue-rules", filepath.Join(root, ".continue", "rules", "kern.md"), "write the continue kern-first rule"))
				case "windsurf":
					out = append(out, would("windsurf-rules", filepath.Join(root, ".windsurf", "rules", "kern-first.md"), "write the windsurf kern-first rule"))
				case "kiro":
					out = append(out, would("kiro-steering", filepath.Join(root, ".kiro", "steering", "kern-first.md"), "write the kiro kern-first steering"))
				}
			}
			if repoEnabled("cursor") {
				out = append(out, would("cursor-rules", filepath.Join(root, ".cursor", "rules", "kern-hooks.mdc"), "write the cursor rule"))
			}
			if repoEnabled("antigravity") {
				out = append(out, would("antigravity-project-hooks", filepath.Join(root, ".agents", "hooks.json"), "write the antigravity project hooks"))
			}
		}
		out = append(out, would("gitignore", filepath.Join(root, ".gitignore"), "write/refresh the kern-generated .gitignore block"))
		out = append(out, would("local-gitexclude", filepath.Join(root, ".git", "info", "exclude"), "add .kern/ to the local git exclude"))
		out = append(out, would("skills (project universal)", filepath.Join(root, ".agents", "skills"), "install/refresh kern skills in .agents/skills"))
		if _, err := os.Stat(filepath.Join(root, ".cursor")); err == nil {
			out = append(out, would("skills-cursor (project)", filepath.Join(root, ".cursor", "rules"), "install/refresh cursor skill rules in .cursor/rules"))
		}
		out = append(out, would("kern config", filepath.Join(root, ".kern", "profiles.json"), "scaffold .kern/profiles.json if missing"))
		for _, agent := range detected {
			if file, ok := instructionFiles[agent]; ok {
				out = append(out, would(agent+"-instruction", filepath.Join(root, file), "write the "+agent+" kern-first policy"))
			}
		}
		return out
	}

	projectScope := !(opts.GlobalOnly && global)
	// .mcp.json is the universal auto-discovered MCP file — always written
	// (except in GlobalOnly mode, which touches no repo file).
	if projectScope {
		out = append(out, agentwire.WireMCPJSON(root, bin))
		out = append(out, agentwire.WireAgentRules(root, mode, wired, explicitThin))
	}
	if projectScope && opts.AgentsMD != "" {
		if err := setAgentsMD(root, opts.AgentsMD); err != nil {
			out = append(out, Status{Agent: "kern config", Path: agentsMDConfigPath(root), Note: "persist agents_md: " + err.Error()})
		}
	}
	if projectScope && repoEnabled("opencode") {
		out = append(out, agentwire.WireOpencode(root))
		out = append(out, agentwire.WirePlugin(root))
	}
	// User-global wiring: hooks, home/global MCP adapters, global
	// opencode config/plugin, global git ignore and editor exclusions are
	// written ONLY with --global. Project wiring must not touch them.
	if global {
		if globalEnabled("opencode") {
			out = append(out, agentwire.WireGlobal(GlobalMCPCommand()))
			out = append(out, agentwire.WireGlobalPlugin())
		}
		if globalEnabled("claude") {
			out = append(out, wireClaude(GlobalMCPCommand()))
			out = append(out, wireClaudeHooks(LaunchCLICommand()))
		}
		if globalEnabled("codex") {
			out = append(out, wireCodex(GlobalMCPCommand()))
			out = append(out, wireCodexHooks(root))
		}
	}
	allAdapters, customErrs := effectiveAdapters(root)
	for _, err := range customErrs {
		out = append(out, Status{Agent: "custom adapters", Note: err.Error()})
	}
	for _, a := range allAdapters {
		if a.scope == "global" && !global {
			continue
		}
		if !projectScope && a.scope != "global" {
			continue
		}
		gate := repoEnabled
		if a.scope == "global" {
			gate = globalEnabled
		}
		if gate(a.name) {
			// Machine-global configs must carry the absolute command (GUI hosts
			// do not inherit the shell PATH); project configs stay portable.
			cmd := bin
			if a.scope == "global" {
				cmd = GlobalMCPCommand()
			}
			out = append(out, wireAdapter(a, root, cmd))
		}
		// Instruction files are per-repo; only wire for present/detected
		// agents, independent of the adapter's config scope.
		if projectScope && repoEnabled(a.name) {
			switch a.name {
			case "continue":
				out = append(out, wireContinueInstructions(root))
			case "windsurf":
				out = append(out, wireWindsurfInstructions(root))
			case "kiro":
				out = append(out, wireKiroInstructions(root))
			}
		}
	}
	if projectScope && repoEnabled("cursor") {
		out = append(out, wireCursorRules(root))
	}
	if projectScope && repoEnabled("antigravity") {
		out = append(out, wireProjectAntigravityHooks(root))
	}
	if global {
		if globalEnabled("gemini") {
			out = append(out, wireGeminiHooks(LaunchCLICommand()))
		}
		if globalEnabled("antigravity") {
			out = append(out, wireAntigravityHooks())
		}
		if globalEnabled("cursor") {
			out = append(out, wireCursorHooks())
		}
		if globalEnabled("copilot") {
			out = append(out, wireCopilotHooks())
		}
		if globalEnabled("qwen") {
			out = append(out, wireQwenHooks(root))
		}
		if globalEnabled("qoder") {
			out = append(out, wireQoderHooks(root))
		}
		if globalEnabled("continue") {
			out = append(out, wireContinueHooks(LaunchCLICommand()))
		}
		if globalEnabled("kiro") {
			out = append(out, wireKiroHooks())
		}
	}
	if projectScope {
		out = append(out, gitStatus(gitblocks.GitignoreGenerated(root)))
		out = append(out, gitStatus(gitblocks.WireLocalGitExclude(root)))
	}
	if global {
		out = append(out, wireGlobalGitHooks())
		out = append(out, gitStatus(gitblocks.WireGlobalGitignore()))
		out = append(out, wireEditorExclusions()...)
		out = append(out, agentwire.WireGlobalSkills()...)
	}

	if !projectScope {
		return out
	}
	out = append(out, agentwire.WireProjectSkills(root)...)
	out = append(out, ensureKernConfig(root))

	// Wire kern-first instruction files for every present platform that
	// has an instruction file. This is independent of the explicit agents
	// list — it ensures the kern-first policy reaches all present agents.
	// F10: wiring itself can create the very marker DetectAgents keys on
	// (bare setup writes .cursor/rules/ and .vscode/mcp.json; the universal
	// skills pass creates .agents/), and `detected` above is a pre-run
	// snapshot. doctor detects post-setup state, so an agent whose marker
	// this run created would end up half-wired — config present, policy
	// missing — a permanent doctor warning caused by setup itself. Extend
	// (never shrink) the snapshot with post-wiring detection so the policy
	// files land in the SAME run that creates the marker.
	seenDetected := make(map[string]bool, len(detected))
	for _, a := range detected {
		seenDetected[a] = true
	}
	for _, a := range DetectAgents(root) {
		if !seenDetected[a] {
			seenDetected[a] = true
			detected = append(detected, a)
		}
	}
	out = append(out, wireInstructions(root, detected, wired, explicitThin)...)

	return out
}

// ensureKernConfig scaffolds the project's own .kern config directory:
// .kern/skills (where user skills live) and .kern/profiles.json (user output
// profiles). Existing artifacts are never overwritten — setup only fills in
// what is missing. .kern/ is covered by the local git exclude, so this never
// dirties the tracked tree.
func ensureKernConfig(root string) Status {
	dir := filepath.Join(root, ".kern", "skills")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Status{Agent: "kern config", Path: filepath.Join(root, ".kern"), Note: err.Error()}
	}
	profilesPath := filepath.Join(root, ".kern", "profiles.json")
	if _, err := os.Stat(profilesPath); err == nil {
		return Status{Agent: "kern config", Installed: true, Path: profilesPath, Note: ".kern/profiles.json already present"}
	}
	if err := os.WriteFile(profilesPath, []byte("[]"), 0o600); err != nil {
		return Status{Agent: "kern config", Path: profilesPath, Note: err.Error()}
	}
	return Status{Agent: "kern config", Installed: true, Path: profilesPath, Note: "scaffolded .kern/profiles.json"}
}

func wireAdapter(a adapter, root, bin string) Status {
	path := a.path(root)
	err := mergeJSON(path, a.key, a.entry(bin))
	if err != nil {
		return Status{Agent: a.name, Path: path, Note: err.Error()}
	}
	return Status{Agent: a.name, Installed: true, Path: path, Note: a.name + " config updated"}
}

// gitStatus adapts a gitblocks result into the Status shape every other
// wiring step reports, preserving the agent label, flags and note so the
// observable setup output is unchanged by the gitblocks extraction (the leaf
// cannot return setup.Status directly — that would cycle the import).
func gitStatus(r gitblocks.Result) Status {
	return Status{Agent: r.Agent, Installed: r.Installed, Skipped: r.Skipped, Path: r.Path, Note: r.Note}
}
