package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSecSummaryLineReportsHiddenWarnings(t *testing.T) {
	counts := map[string]int{"error": 0, "warning": 1, "info": 0}
	line := secSummaryLine(0, counts, true)
	for _, want := range []string{"0 shown of 1 findings", "0 error, 1 warning, 0 info", "default lens: error", "--severity error,warning,info"} {
		if !strings.Contains(line, want) {
			t.Errorf("summary %q missing %q", line, want)
		}
	}
	if strings.Contains(line, "0 findings (0 error, 0 warning") {
		t.Errorf("summary hides the warning count: %q", line)
	}

	explicit := secSummaryLine(1, counts, false)
	if !strings.Contains(explicit, "1 findings") || strings.Contains(explicit, "default lens") {
		t.Errorf("explicit-lens summary = %q", explicit)
	}
}

// TestRunSecSingleFileTargetsFileNotDirectory pins P0-2: `kern sec <file>`
// must scan the file itself, not treat it as a root (which produced
// "read <file>/.kern/verify-suppressions.json: not a directory").
func TestRunSecSingleFileTargetsFileNotDirectory(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "server.pem")
	const pem = "-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA0auditfixturebody\n-----END RSA PRIVATE KEY-----\n"
	if err := os.WriteFile(file, []byte(pem), 0o600); err != nil {
		t.Fatal(err)
	}
	var code int
	out := captureStdout(t, func() {
		code = fixesExitCode(func() { runSec([]string{file}) })
	})
	if code != 1 {
		t.Fatalf("a private-key file must be a findings exit (1), got %d; output:\n%s", code, out)
	}
	if !strings.Contains(out, "server.pem") {
		t.Fatalf("finding must name the scanned file, got:\n%s", out)
	}
	if strings.Contains(out, "not a directory") {
		t.Fatalf("single-file mode still probed the file as a root:\n%s", out)
	}
}
