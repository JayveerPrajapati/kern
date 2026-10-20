package app

import "testing"

func TestRun(t *testing.T) {
	if got := Run(); got != "build" {
		t.Fatalf("Run() = %q, want build", got)
	}
}
