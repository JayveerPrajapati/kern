package main

import (
	"io"
	"os"
	"testing"
)

// Shared test helpers used across cmd/kern command tests. These were
// originally defined next to the approval-family tests (cmd_approve_test.go,
// cmd_audit_test.go); when the family moved to internal/approvalcli the
// helpers stayed here because non-family tests (deploy, artifacts,
// agent-control, incident, ...) depend on them. The approvalcli package
// carries its own copies for its own tests.

// newRoot returns an isolated project root for command tests: XDG_CACHE_HOME
// is redirected so stores keyed by project root stay private to this test.
func newRoot(t *testing.T) string {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	return t.TempDir()
}

// expectExit runs fn and asserts it panicked with the exitError sentinel (the
// house pattern for testing fatal/fatalUsage paths without killing the test
// binary).
func expectExit(t *testing.T, code int, fn func()) {
	t.Helper()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatalf("expected exitError{%d}, got no panic", code)
		}
		if e, ok := r.(exitError); !ok || e.code != code {
			t.Fatalf("expected exitError{%d}, got %v", code, r)
		}
	}()
	fn()
}

// captureStderr runs fn with os.Stderr redirected to a pipe and returns the
// captured output (stdlib log writes there by default).
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	defer func() { os.Stderr = old }()
	fn()
	_ = w.Close()
	os.Stderr = old
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// captureSetupOut runs fn with os.Stdout redirected to a pipe and returns the
// captured output. Originally defined next to the setup-family render tests;
// when the setup family moved to internal/setup (workstream 3.6-A) the helper
// stayed here because non-family tests (try, ...) depend on it. The internal
// setup package carries its own copy for its own tests.
func captureSetupOut(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	done := make(chan string)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	fn()
	_ = w.Close()
	os.Stdout = old
	return <-done
}
