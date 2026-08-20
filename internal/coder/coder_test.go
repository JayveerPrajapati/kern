package coder

import (
	"strings"
	"testing"
)

func TestExtractPatchFromDiffBlock(t *testing.T) {
	response := "Here's the patch:\n```diff\n--- a/main.go\n+++ b/main.go\n@@ -1,3 +1,4 @@\n package main\n+\n func main() {}\n```\n"
	patch := extractPatch(response)
	if patch == "" {
		t.Fatal("extractPatch returned empty")
	}
	if !strings.Contains(patch, "--- a/main.go") {
		t.Error("patch missing diff header")
	}
}

func TestExtractPatchFromPatchBlock(t *testing.T) {
	response := "```patch\n--- a/file.go\n+++ b/file.go\n```\n"
	patch := extractPatch(response)
	if patch == "" {
		t.Fatal("extractPatch returned empty for patch block")
	}
}

func TestExtractPatchBareDiff(t *testing.T) {
	response := "--- a/file.go\n+++ b/file.go\n@@ -1 +1 @@\n-old\n+new\n"
	patch := extractPatch(response)
	if patch == "" {
		t.Fatal("extractPatch returned empty for bare diff")
	}
}

func TestExtractPatchEmpty(t *testing.T) {
	if patch := extractPatch("no patch here"); patch != "" {
		t.Errorf("expected empty, got %q", patch)
	}
}

func TestBuildPromptFirstRound(t *testing.T) {
	a := New(nil) // nil provider is fine for prompt building
	prompt := a.buildPrompt("add caching", "add Redis client", "/tmp/work", nil)
	if !strings.Contains(prompt, "add caching") {
		t.Error("prompt missing intent")
	}
	if !strings.Contains(prompt, "add Redis client") {
		t.Error("prompt missing plan")
	}
	if !strings.Contains(prompt, "unified diff") {
		t.Error("prompt missing diff instruction")
	}
}

func TestBuildPromptWithFailures(t *testing.T) {
	a := New(nil)
	prev := []RoundResult{
		{Round: 1, Verdict: "fail", Summary: "build error: undefined variable"},
	}
	prompt := a.buildPrompt("add caching", "add Redis client", "/tmp/work", prev)
	if !strings.Contains(prompt, "Previous attempts failed") {
		t.Error("prompt missing failure context")
	}
	if !strings.Contains(prompt, "build error") {
		t.Error("prompt missing error details")
	}
}

func TestCodeNoProvider(t *testing.T) {
	a := New(nil)
	// Code with nil provider should return ErrNoProvider. The no-provider
	// check runs before any worktree/LLM use, so nil worktree is fine here.
	_, err := a.Code("test intent", "", nil)
	if err != ErrNoProvider {
		t.Errorf("Code with nil provider = %v, want ErrNoProvider", err)
	}
}

func TestNewWithOptions(t *testing.T) {
	a := New(nil,
		WithModel("llama3"),
		WithMaxRounds(5),
		WithMaxTokens(4096),
		WithVerifyTypes([]string{"build", "test"}),
	)
	if a.model != "llama3" {
		t.Error("model not set")
	}
	if a.maxRounds != 5 {
		t.Error("maxRounds not set")
	}
	if a.maxTokens != 4096 {
		t.Error("maxTokens not set")
	}
	if len(a.verifyTypes) != 2 {
		t.Error("verifyTypes not set")
	}
}
