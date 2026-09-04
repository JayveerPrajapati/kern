package sandbox

import (
	"context"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/blueprint/domain"
)

// TestSandboxMatrix_DefaultGoTarget verifies that a sandbox check with no matrix
// configured falls back to the default Go build+test target and passes.
func TestSandboxMatrix_DefaultGoTarget(t *testing.T) {
	dir := g8Repo(t)
	check := NewDefaultCheck()
	result, err := check.Run(context.Background(), domain.ChangeRequest{RepositoryRoot: dir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != domain.StatusPass {
		t.Errorf("expected StatusPass, got %s (error=%s, findings=%d)", result.Status, result.Error, len(result.Findings))
	}
}

// TestSandboxMatrix_ExplicitGoTarget verifies that an explicit matrix with a Go
// target produces the same pass result.
func TestSandboxMatrix_ExplicitGoTarget(t *testing.T) {
	dir := g8Repo(t)
	check := NewDefaultCheck(WithMatrix([]MatrixTarget{
		{
			Name:  "go",
			Dir:   ".",
			Build: []string{"go", "build", "./..."},
			Test:  []string{"go", "test", "./..."},
		},
	}))
	result, err := check.Run(context.Background(), domain.ChangeRequest{RepositoryRoot: dir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != domain.StatusPass {
		t.Errorf("expected StatusPass, got %s (error=%s, findings=%d)", result.Status, result.Error, len(result.Findings))
	}
}

// TestSandboxMatrix_CommandTarget verifies the combined `command` field.
func TestSandboxMatrix_CommandTarget(t *testing.T) {
	dir := g8Repo(t)
	check := NewDefaultCheck(WithMatrix([]MatrixTarget{
		{
			Name:    "combined",
			Dir:     ".",
			Command: []string{"go", "build", "./..."},
		},
	}))
	result, err := check.Run(context.Background(), domain.ChangeRequest{RepositoryRoot: dir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != domain.StatusPass {
		t.Errorf("expected StatusPass, got %s (error=%s)", result.Status, result.Error)
	}
}

// TestSandboxMatrix_FailingTarget verifies a failing build returns StatusBlock.
func TestSandboxMatrix_FailingTarget(t *testing.T) {
	dir := g8Repo(t)
	g8WriteCommit(t, dir, "bad.go", "package main\n\nfunc broken() { this is invalid }\n")

	check := NewDefaultCheck(WithMatrix([]MatrixTarget{
		{
			Name:  "go",
			Dir:   ".",
			Build: []string{"go", "build", "./..."},
			Test:  []string{"go", "test", "./..."},
		},
	}))
	result, err := check.Run(context.Background(), domain.ChangeRequest{RepositoryRoot: dir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != domain.StatusBlock {
		t.Errorf("expected StatusBlock, got %s", result.Status)
	}
	if len(result.Findings) == 0 {
		t.Error("expected at least one finding")
	} else if result.Findings[0].RuleID != "sandbox:build-failure" {
		t.Errorf("expected sandbox:build-failure, got %s", result.Findings[0].RuleID)
	}
}

// TestSandboxMatrix_MultiTarget verifies two targets both run and a failing
// second step is caught.
func TestSandboxMatrix_MultiTarget(t *testing.T) {
	dir := g8Repo(t)
	check := NewDefaultCheck(WithMatrix([]MatrixTarget{
		{
			Name:  "go-build",
			Dir:   ".",
			Build: []string{"go", "build", "./..."},
		},
		{
			Name:    "fail",
			Dir:     ".",
			Command: []string{"false"},
		},
	}))
	result, err := check.Run(context.Background(), domain.ChangeRequest{RepositoryRoot: dir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != domain.StatusBlock {
		t.Errorf("expected StatusBlock, got %s", result.Status)
	}
}

// TestSplitCommand checks whitespace splitting.
func TestSplitCommand(t *testing.T) {
	got := SplitCommand("go build ./...")
	want := []string{"go", "build", "./..."}
	if len(got) != len(want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] expected %q, got %q", i, want[i], got[i])
		}
	}
}
