package main

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
	code := runInstallHook([]string{"pre-push"})
	if code != 0 {
		t.Fatalf("expected exit code 0 for pre-push, got %d", code)
	}

	prePushPath := filepath.Join(dir, ".git", "hooks", "pre-push")
	data, err := os.ReadFile(prePushPath)
	if err != nil {
		t.Fatalf("read pre-push hook: %v", err)
	}
	if !strings.Contains(string(data), "--tests") || !strings.Contains(string(data), "--resilience") {
		t.Errorf("pre-push hook missing --tests or --resilience: %s", string(data))
	}

	// 2. Install all
	code = runInstallHook([]string{"all"})
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
	code = runInstallHook([]string{"invalid"})
	if code != 2 {
		t.Fatalf("expected exit code 2 for invalid target, got %d", code)
	}
}
