package cockpit

import (
	"bytes"
	"strings"
	"testing"
)

// TestOpsCLIEntryPointAlive verifies the ops CLI entry point behind `kern ops`
// (and the `kern kernops` alias). It replaced the old standalone-binary build
// smoke test; the in-process RunOpsCLI is exercised directly: --help must exit
// 0 and print cockpit-specific usage text.
func TestOpsCLIEntryPointAlive(t *testing.T) {
	var out, errBuf bytes.Buffer

	if code := RunOpsCLI([]string{"--help"}, &out, &errBuf); code != 0 {
		t.Fatalf("RunOpsCLI --help exit code = %d, want 0", code)
	}
	helpOut := out.String() + errBuf.String()
	if helpOut == "" {
		t.Fatalf("expected help output from RunOpsCLI --help")
	}
	if !strings.Contains(helpOut, "Autonomy level") {
		t.Fatalf("expected cockpit-specific flag help, got: %q", helpOut)
	}

	// The triage subcommand help is reachable through the same entry point.
	out.Reset()
	errBuf.Reset()
	if code := RunOpsCLI([]string{"triage", "--help"}, &out, &errBuf); code != 0 {
		t.Fatalf("RunOpsCLI triage --help exit code = %d, want 0", code)
	}
	triageOut := out.String() + errBuf.String()
	if triageOut == "" {
		t.Fatalf("expected help output from RunOpsCLI triage --help")
	}
	if !strings.Contains(triageOut, "triage") {
		t.Fatalf("expected triage help text, got: %q", triageOut)
	}
}
