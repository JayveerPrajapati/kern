package main

import (
	"strings"
	"testing"
)

// TestHelpDescribesBareVerify pins the help text against the shipped
// behaviour: bare `kern verify` runs the default build check (commit
// 5f0dd038) — it is not a usage error.
func TestHelpDescribesBareVerify(t *testing.T) {
	out := captureStderr(t, usage)
	if !strings.Contains(out, "bare 'kern verify' runs the default build check") {
		t.Fatalf("help does not describe bare verify:\n%s", out)
	}
	if strings.Contains(out, "usage error") {
		t.Fatalf("help still claims bare verify is a usage error:\n%s", out)
	}
}
