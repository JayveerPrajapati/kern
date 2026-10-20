package agentwire

import (
	"runtime"
	"testing"
)

// TestPlatformMCPBinaryName locks the .exe naming for Windows so a Windows
// release install wires kern-mcp.exe (not a bare name the OS cannot resolve),
// and the extensionless name everywhere else.
func TestPlatformMCPBinaryName(t *testing.T) {
	want := "kern-mcp"
	if runtime.GOOS == "windows" {
		want = "kern-mcp.exe"
	}
	if mcpName() != want {
		t.Errorf("mcpName=%q want %q", mcpName(), want)
	}
}
