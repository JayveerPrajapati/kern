package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallHook_PrePushAndAll(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(orig) }()

	// 1. Install pre-push
	code := RunInstallHook([]string{"pre-push"})
	if code != 0 {
		t.Fatalf("expected exit code 0 for pre-push, got %d", code)
	}

	prePushPath := filepath.Join(dir, ".git", "hooks", "pre-push")
	data, err := os.ReadFile(prePushPath)
	if err != nil {
		t.Fatalf("read pre-push hook: %v", err)
	}
	// The pre-push hook keeps every BLOCKING leg (secrets, architecture,
	// approval, full two-pass duplication) but defers the deep suites
	// (tests:build-test, resilience:scenarios) to CI, which re-enforces
	// them on every PR and nightly (blueprint-nightly.yml). See
	// internal/blueprint/cli/install_hook.go for the rationale + timings.
	if !strings.Contains(string(data), "check --staged --format=terminal") {
		t.Errorf("pre-push hook missing blocking `kern check --staged` invocation: %s", string(data))
	}
	if strings.Contains(string(data), "--tests") || strings.Contains(string(data), "--resilience") {
		t.Errorf("pre-push hook must defer --tests/--resilience to CI, not run them: %s", string(data))
	}

	// 2. Install all
	code = RunInstallHook([]string{"all"})
	if code != 0 {
		t.Fatalf("expected exit code 0 for all, got %d", code)
	}

	preCommitPath := filepath.Join(dir, ".git", "hooks", "pre-commit")
	dataCommit, err := os.ReadFile(preCommitPath)
	if err != nil {
		t.Fatalf("read pre-commit hook: %v", err)
	}
	if !strings.Contains(string(dataCommit), "Blueprint pre-commit hook") {
		t.Errorf("pre-commit hook missing marker: %s", string(dataCommit))
	}

	// 3. Invalid target
	code = RunInstallHook([]string{"invalid"})
	if code != 2 {
		t.Fatalf("expected exit code 2 for invalid target, got %d", code)
	}
}
