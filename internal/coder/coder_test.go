package coder

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/execution"
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
	prompt := a.buildPrompt("add caching", "add Redis client", "", "/tmp/work", nil)
	if !strings.Contains(prompt, "add caching") {
		t.Error("prompt missing intent")
	}
	if !strings.Contains(prompt, "add Redis client") {
		t.Error("prompt missing plan")
	}
	if !strings.Contains(prompt, "unified diff") { // still offered as fallback
		t.Error("prompt missing diff instruction")
	}
}

func TestBuildPromptWithFailures(t *testing.T) {
	a := New(nil)
	prev := []RoundResult{
		{Round: 1, Verdict: "fail", Summary: "build error: undefined variable"},
	}
	prompt := a.buildPrompt("add caching", "add Redis client", "", "/tmp/work", prev)
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
	_, err := a.Code("test intent", "", "", nil)
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

func TestNewHonorsModelOverrideEnv(t *testing.T) {
	// With no env override and no WithModel, the model stays empty so the
	// provider's own default is used (provider-neutral).
	t.Setenv("KERN_MODEL_CODER", "")
	t.Setenv("KERN_MODEL_DEFAULT", "")
	if a := New(nil); a.model != "" {
		t.Errorf("model with no env override = %q, want empty", a.model)
	}

	// KERN_MODEL_CODER drives the default model for coder.New.
	t.Setenv("KERN_MODEL_CODER", "codellama:7b")
	if a := New(nil); a.model != "codellama:7b" {
		t.Errorf("model with KERN_MODEL_CODER = %q, want codellama:7b", a.model)
	}

	// KERN_MODEL_DEFAULT applies when the role var is unset.
	t.Setenv("KERN_MODEL_CODER", "")
	t.Setenv("KERN_MODEL_DEFAULT", "deepseek-coder")
	if a := New(nil); a.model != "deepseek-coder" {
		t.Errorf("model with KERN_MODEL_DEFAULT = %q, want deepseek-coder", a.model)
	}

	// Explicit WithModel still overrides the env (env is only the default).
	t.Setenv("KERN_MODEL_CODER", "codellama:7b")
	if a := New(nil, WithModel("llama3")); a.model != "llama3" {
		t.Errorf("WithModel must override env, got %q", a.model)
	}
}

// editFixtureWorktree creates a worktree over a tiny project with one
// editable file for the search/replace tests.
func editFixtureWorktree(t *testing.T) *execution.Worktree {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/edits\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {\n\tprintln(\"hello\")\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w, err := execution.NewWorktree(dir)
	if err != nil {
		t.Fatalf("NewWorktree: %v", err)
	}
	t.Cleanup(func() { _ = w.Cleanup() })
	return w
}

// TestExtractEdits verifies the per-file search/replace format parses,
// including multiple pairs per file and the new-file form (replace only).
func TestExtractEdits(t *testing.T) {
	resp := `Here are the edits:

<file path="main.go">
<search>
	println("hello")
</search>
<replace>
	println("goodbye")
</replace>
</file>

<file path="internal/new/util.go">
<replace>
package util
</replace>
</file>
`
	edits := extractEdits(resp)
	if len(edits) != 2 {
		t.Fatalf("got %d file edits, want 2: %+v", len(edits), edits)
	}
	if edits[0].path != "main.go" || len(edits[0].replacements) != 1 {
		t.Fatalf("first edit wrong: %+v", edits[0])
	}
	if edits[0].replacements[0].search != "\tprintln(\"hello\")" {
		t.Errorf("search text = %q; want the tab-indented line", edits[0].replacements[0].search)
	}
	if edits[1].path != "internal/new/util.go" || edits[1].replacements[0].search != "" {
		t.Fatalf("new-file edit wrong: %+v", edits[1])
	}
	if edits[1].replacements[0].replace != "package util" {
		t.Errorf("replace = %q; want %q", edits[1].replacements[0].replace, "package util")
	}
	if e := extractEdits("no edits here"); e != nil {
		t.Errorf("no file blocks should yield nil, got %+v", e)
	}
}

// TestApplyEditsRoundTrip applies parsed edits to a real worktree and checks
// the files on disk, including new-file creation in a nested directory.
func TestApplyEditsRoundTrip(t *testing.T) {
	w := editFixtureWorktree(t)
	edits := []fileEdit{
		{path: "main.go", replacements: []replacement{
			{search: `println("hello")`, replace: `println("goodbye")`},
		}},
		{path: "internal/new/util.go", replacements: []replacement{
			{search: "", replace: "package util\n\nfunc ID() int { return 1 }\n"},
		}},
	}
	if err := applyEdits(w, edits); err != nil {
		t.Fatalf("applyEdits: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(w.Dir(), "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), `println("goodbye")`) {
		t.Errorf("main.go not edited: %s", got)
	}
	got, err = os.ReadFile(filepath.Join(w.Dir(), "internal/new/util.go"))
	if err != nil {
		t.Fatalf("new file not created: %v", err)
	}
	if !strings.HasPrefix(string(got), "package util") {
		t.Errorf("new file content wrong: %s", got)
	}
}

// TestApplyEditsFailureIncludesFileHead verifies the C1 apply-failure
// feedback: when the search text is not found, the error carries both the
// offending search text and the actual head of the file so the next LLM
// round can self-correct.
func TestApplyEditsFailureIncludesFileHead(t *testing.T) {
	w := editFixtureWorktree(t)
	edits := []fileEdit{
		{path: "main.go", replacements: []replacement{
			{search: "this text does not exist", replace: "x"},
		}},
	}
	err := applyEdits(w, edits)
	if err == nil {
		t.Fatal("applyEdits should fail on unmatched search text")
	}
	msg := err.Error()
	if !strings.Contains(msg, "search text not found") {
		t.Errorf("error should name the failure: %s", msg)
	}
	if !strings.Contains(msg, "this text does not exist") {
		t.Errorf("error should show the search text: %s", msg)
	}
	if !strings.Contains(msg, `package main`) {
		t.Errorf("error should include the actual file head: %s", msg)
	}
}

// TestApplyEditsRejectsPathEscape guards the worktree confinement: paths
// that are absolute or escape with .. are rejected before any file is touched.
func TestApplyEditsRejectsPathEscape(t *testing.T) {
	w := editFixtureWorktree(t)
	for _, p := range []string{"../outside.txt", "/etc/passwd", ""} {
		err := applyEdits(w, []fileEdit{{path: p, replacements: []replacement{{search: "", replace: "x"}}}})
		if err == nil {
			t.Errorf("path %q should be rejected", p)
		}
	}
}
