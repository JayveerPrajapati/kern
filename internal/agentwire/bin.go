package agentwire

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// mcpName returns the platform-appropriate MCP binary file name. On Windows
// the release binary is kern-mcp.exe; everywhere else it is extensionless.
func mcpName() string {
	if runtime.GOOS == "windows" {
		return "kern-mcp.exe"
	}
	return "kern-mcp"
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

// PortableMCPCommand returns the command that PROJECT configs should run to
// start kern-mcp. Prefer a PATH-resolved bare "kern-mcp" (the config may be
// committed and shared, so a machine-specific absolute path would break
// teammates); fall back to the sibling absolute path only when kern-mcp is not
// on PATH. Machine-global configs must use LaunchMCPCommand instead — GUI
// hosts do not inherit the shell PATH.
func PortableMCPCommand() string {
	if p, err := exec.LookPath(mcpName()); err == nil && p != "" {
		return mcpName()
	}
	return Bin()
}

// LaunchMCPCommand returns an ABSOLUTE kern-mcp command for machine-global
// configs. GUI-launched hosts on macOS (Claude Code, Cursor, Gemini, Qwen,
// Codex, Zed) do not inherit the shell PATH: a bare "kern-mcp" written by a
// setup run from a terminal resolves there but NOT at host launch, so the MCP
// server silently never starts and agents fall back to built-in tools. The
// install directory is stable across `kern update`, which makes the absolute
// path safe for global configs (project configs stay portable; see
// PortableMCPCommand).
func LaunchMCPCommand() string { return launchCommand(mcpName(), Bin()) }

// LaunchCLICommand is the hook-command equivalent of LaunchMCPCommand: an
// absolute `kern` path for machine-global hook settings (PreToolUse / PostToolUse
// / SessionStart), which are also executed by GUI-launched hosts with a minimal
// PATH. A bare `kern` in those hooks fails open silently, disabling kern-first
// steering.
func LaunchCLICommand() string { return launchCommand(cliName(), CLIPath()) }

// launchCommand resolves name on PATH and absolutizes it; when PATH lookup
// fails it absolutizes the sibling fallback. The result is never a bare name
// unless the fallback itself cannot be absolutized.
func launchCommand(name, fallback string) string {
	if p, err := exec.LookPath(name); err == nil && p != "" {
		if abs, err := filepath.Abs(p); err == nil {
			return abs
		}
		return p
	}
	if abs, err := filepath.Abs(fallback); err == nil {
		return abs
	}
	return fallback
}

// cliName returns the platform-appropriate kern CLI file name.
func cliName() string {
	if runtime.GOOS == "windows" {
		return "kern.exe"
	}
	return "kern"
}

// CLIPath returns the absolute path to the kern CLI that ships next to the
// running executable, falling back to the bare command name.
func CLIPath() string {
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
