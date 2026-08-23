package setup

import (
	"runtime"
	"testing"
)

// TestPlatformBinaryNames locks the .exe naming for Windows so a Windows release
// install wires kern-mcp.exe / kern.exe (not a bare name the OS cannot resolve),
// and the extensionless names everywhere else.
func TestPlatformBinaryNames(t *testing.T) {
	if runtime.GOOS == "windows" {
		if mcpName() != "kern-mcp.exe" {
			t.Errorf("mcpName=%q want kern-mcp.exe", mcpName())
		}
		if cliName() != "kern.exe" {
			t.Errorf("cliName=%q want kern.exe", cliName())
		}
	} else {
		if mcpName() != "kern-mcp" {
			t.Errorf("mcpName=%q want kern-mcp", mcpName())
		}
		if cliName() != "kern" {
			t.Errorf("cliName=%q want kern", cliName())
		}
	}
}
