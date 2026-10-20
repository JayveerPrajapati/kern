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

// PortableMCPCommand returns the command that agent configs should run to
// start kern-mcp. Prefer a PATH-resolved bare "kern-mcp" (the agent re-resolves
// it against PATH at launch, so it works on any machine and survives relocation);
// fall back to the sibling absolute path only when kern-mcp is not on PATH.
func PortableMCPCommand() string {
	if p, err := exec.LookPath(mcpName()); err == nil && p != "" {
		return mcpName()
	}
	return Bin()
}
