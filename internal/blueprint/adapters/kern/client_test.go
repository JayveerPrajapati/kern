package kern

import (
	"context"
	"errors"
	"testing"
)

// --- P2-4 (G25): KernClient.Version() provenance probe ---

// TestClientVersionStripsPrefixAndCaches verifies Version() strips the leading
// "kern " prefix and caches the result: two calls spawn exactly one
// subprocess invocation.
func TestClientVersionStripsPrefixAndCaches(t *testing.T) {
	calls := 0
	runner := func(ctx context.Context, name string, args []string, workdir string) (string, string, int, error) {
		calls++
		if len(args) != 1 || args[0] != "version" {
			t.Errorf("args = %v, want [version]", args)
		}
		return "kern dev\n", "", 0, nil
	}
	c := &KernClient{binaryPath: "kern", runner: runner}

	v, err := c.Version()
	if err != nil {
		t.Fatalf("Version returned error: %v", err)
	}
	if v != "dev" {
		t.Errorf("Version = %q, want %q (leading 'kern ' stripped)", v, "dev")
	}

	// Second call: served from the cache, no new subprocess.
	v2, err2 := c.Version()
	if err2 != nil {
		t.Fatalf("second Version returned error: %v", err2)
	}
	if v2 != "dev" {
		t.Errorf("cached Version = %q, want %q", v2, "dev")
	}
	if calls != 1 {
		t.Errorf("runner invocations = %d, want 1 (cached)", calls)
	}
}

// TestClientVersionNonZeroExit verifies a non-zero exit surfaces as an error
// so callers can treat the probe as best-effort.
func TestClientVersionNonZeroExit(t *testing.T) {
	runner := func(ctx context.Context, name string, args []string, workdir string) (string, string, int, error) {
		return "", "version: boom", 1, nil
	}
	c := &KernClient{binaryPath: "kern", runner: runner}
	if _, err := c.Version(); err == nil {
		t.Fatal("Version returned no error, want error for non-zero exit")
	}
}

// TestClientVersionLaunchFailure verifies a launch failure surfaces as an
// error and is cached too (probe once per client lifetime).
func TestClientVersionLaunchFailure(t *testing.T) {
	calls := 0
	runner := func(ctx context.Context, name string, args []string, workdir string) (string, string, int, error) {
		calls++
		return "", "", -1, errors.New("launch failed")
	}
	c := &KernClient{binaryPath: "kern", runner: runner}
	if _, err := c.Version(); err == nil {
		t.Fatal("Version returned no error, want error for launch failure")
	}
	if _, err := c.Version(); err == nil {
		t.Fatal("cached Version returned no error, want the cached launch failure")
	}
	if calls != 1 {
		t.Errorf("runner invocations = %d, want 1 (error cached too)", calls)
	}
}

// TestClientVersionEmptyOutput verifies empty output is an error: provenance
// stamping needs a non-empty version string.
func TestClientVersionEmptyOutput(t *testing.T) {
	runner := func(ctx context.Context, name string, args []string, workdir string) (string, string, int, error) {
		return "", "", 0, nil
	}
	c := &KernClient{binaryPath: "kern", runner: runner}
	if _, err := c.Version(); err == nil {
		t.Fatal("Version returned no error, want error for empty output")
	}
}
