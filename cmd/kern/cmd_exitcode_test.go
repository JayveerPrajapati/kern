package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestRunDoctorExitsNonzeroOnFail pins the e2e round-2 fix: `kern doctor`
// printed "verdict: failures" but exited 0. Any [fail]-level finding must
// map to exit 1 (warn-only runs stay 0).
func TestRunDoctorExitsNonzeroOnFail(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	// Force the binary-exec check to fail: setup.Bin() falls back to a
	// bare "kern-mcp" lookup that cannot resolve without PATH.
	t.Setenv("PATH", t.TempDir())
	root := jsonCliFixture(t)

	var code int
	out := captureStdout(t, func() {
		code = runDoctor([]string{root, "--json"})
	})
	if code != 1 {
		t.Fatalf("expected exit code 1 with fail findings, got %d (out: %.200s)", code, out)
	}
	if !strings.Contains(out, `"Level": "fail"`) {
		t.Fatalf("expected a fail-level finding in JSON, got: %.400s", out)
	}
}

// TestRunDoctorExitCodeConsistency asserts the invariant both ways: the
// exit code is 1 exactly when a fail-level finding is present.
func TestRunDoctorExitCodeConsistency(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := jsonCliFixture(t)
	var code int
	out := captureStdout(t, func() {
		code = runDoctor([]string{root, "--json"})
	})
	hasFail := strings.Contains(out, `"Level": "fail"`)
	if hasFail && code != 1 {
		t.Fatalf("fail findings present but exit code %d", code)
	}
	if !hasFail && code != 0 {
		t.Fatalf("no fail findings but exit code %d (out: %.300s)", code, out)
	}
}

// TestRunProjectMissingRootFails pins the e2e round-2 fix: `kern project
// map` rendered "Project: map (0 files)" with exit 0 for a nonexistent
// root. A missing root must fail loud (exit 1).
func TestRunProjectMissingRootFails(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope")
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected exitError panic for missing root")
		}
		e, ok := r.(exitError)
		if !ok {
			panic(r)
		}
		if e.code != 1 {
			t.Fatalf("expected exit code 1, got %d", e.code)
		}
	}()
	runProject([]string{missing})
}
