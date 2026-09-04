package main

import (
	"os"
	"os/exec"
	"testing"
)

func TestKernOpsBinaryBuildsAndRuns(t *testing.T) {
	// Build binary
	tmpBin := t.TempDir() + "/kernops_test_bin"
	buildCmd := exec.Command("go", "build", "-o", tmpBin, ".")
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, string(out))
	}
	defer os.Remove(tmpBin)

	// Run with --help
	cmd := exec.Command(tmpBin, "-help")
	out, _ := cmd.CombinedOutput()
	if !os.IsNotExist(nil) && len(out) == 0 {
		t.Fatalf("expected help output from kernops")
	}

	// Run triage --help
	cmdTriage := exec.Command(tmpBin, "triage", "-help")
	triageOut, _ := cmdTriage.CombinedOutput()
	if len(triageOut) == 0 {
		t.Fatalf("expected help output from kernops triage")
	}
}

