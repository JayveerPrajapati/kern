package setup

import (
	"runtime"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/agentwire"
)

// TestPlatformBinaryNames locks the .exe naming for Windows so a Windows release
// install wires kern.exe (not a bare name the OS cannot resolve), and the
// extensionless name everywhere else. The kern-mcp side of the contract lives
// in internal/agentwire (TestPlatformMCPBinaryName).
func TestPlatformBinaryNames(t *testing.T) {
	if runtime.GOOS == "windows" {
		if agentwire.Bin() == "" {
			t.Error("agentwire.Bin empty")
		}
		if cliName() != "kern.exe" {
			t.Errorf("cliName=%q want kern.exe", cliName())
		}
	} else {
		if cliName() != "kern" {
			t.Errorf("cliName=%q want kern", cliName())
		}
	}
}
