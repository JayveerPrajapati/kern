package heal

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTruncate(t *testing.T) {
	if got := truncate("short", 100); got != "short" {
		t.Fatalf("truncate short: %q", got)
	}
	got := truncate(strings.Repeat("x", 50), 10)
	if !strings.HasPrefix(got, strings.Repeat("x", 10)) || !strings.HasSuffix(got, "... (truncated)") {
		t.Fatalf("truncate long wrong: %q", got)
	}
}

func TestSplitLines(t *testing.T) {
	if got := splitLines("a\nb\n"); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("splitLines trailing newline wrong: %v", got)
	}
	if got := splitLines("single"); len(got) != 1 || got[0] != "single" {
		t.Fatalf("splitLines single wrong: %v", got)
	}
}

func TestFailingFilesNoMatch(t *testing.T) {
	// Paths that don't exist on disk are dropped (os.Stat guard).
	out := "app.go:3:1: syntax error\npkg/foo.go:10:5: undefined\nweird line no refs here"
	if got := failingFiles(out); len(got) != 0 {
		t.Fatalf("expected no resolvable files, got %v", got)
	}
}

func TestFailingFilesDedupsAndResolves(t *testing.T) {
	// failingFiles stats against CWD, so run from a temp dir containing the file.
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "app.go"), []byte("x"), 0o644)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(cwd)

	out := "app.go:3:1: syntax error\napp.go:5:1: again\nother.go:2:1: nope"
	got := failingFiles(out)
	if len(got) != 1 || got[0] != "app.go" {
		t.Fatalf("expected deduped [app.go], got %v (other.go dropped by stat)", got)
	}
}

// mockOllama is a fake Ollama server that returns a corrected file in
// /api/generate and 200 on /api/tags (for the Available() probe).
func mockOllama(t *testing.T, generateBody string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tags" {
			w.Write([]byte(`{"models":[]}`))
			return
		}
		if r.URL.Path != "/api/generate" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		resp := map[string]any{"response": generateBody}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

func newBrokenGoProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "go.mod"), []byte("module demo\n\ngo 1.22\n"), 0o644)
	_ = os.WriteFile(filepath.Join(root, "app.go"), []byte("package main\n\nfunc broken(\n"), 0o644)
	return root
}

func TestRunHealLoopEndToEnd(t *testing.T) {
	root := newBrokenGoProject(t)

	// Mock Ollama returns a corrected, valid app.go.
	corrected := "### FILE: app.go\npackage main\n\nfunc main() {}\n"
	srv := mockOllama(t, corrected)
	defer srv.Close()
	t.Setenv("OLLAMA_HOST", srv.URL)
	// Avoid touching the real XDG cache.
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	res := Run(root, "fix the build", "", 3, 60*time.Second)
	if res.Err != nil {
		t.Fatalf("expected successful heal, got err: %v", res.Err)
	}
	if res.Iterations != 1 {
		t.Fatalf("expected 1 iteration, got %d", res.Iterations)
	}
	if res.Command == nil || res.Command.Name != "go build" {
		t.Fatalf("expected go build command, got %+v", res.Command)
	}
	if res.Diff == "" {
		t.Fatal("expected non-empty diff against the live broken file")
	}
}

func TestRunAlreadyHealthy(t *testing.T) {
	// A valid project passes baseline validation immediately: no LLM round.
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "go.mod"), []byte("module demo\n\ngo 1.22\n"), 0o644)
	_ = os.WriteFile(filepath.Join(root, "app.go"), []byte("package main\n\nfunc main() {}\n"), 0o644)

	res := Run(root, "task", "", 3, 30*time.Second)
	if res.Err != nil {
		t.Fatalf("expected no error, got %v", res.Err)
	}
	if !res.Validated {
		t.Fatal("expected Validated=true for healthy project")
	}
	if res.Iterations != 0 {
		t.Fatalf("expected 0 iterations, got %d", res.Iterations)
	}
}

func TestRunDetectFails(t *testing.T) {
	// No go.mod / no supported files -> Detect returns error.
	root := t.TempDir()
	res := Run(root, "task", "", 3, 5*time.Second)
	if res.Err == nil {
		t.Fatal("expected error when no toolchain detected")
	}
}

func TestRunLLMNoFileBlocks(t *testing.T) {
	root := newBrokenGoProject(t)
	// Mock Ollama returns junk without FILE blocks -> loop bails with error.
	srv := mockOllama(t, "no file blocks here")
	defer srv.Close()
	t.Setenv("OLLAMA_HOST", srv.URL)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	res := Run(root, "task", "", 1, 60*time.Second)
	if res.Err == nil {
		t.Fatal("expected error from LLM reply without FILE blocks")
	}
	if res.Iterations != 1 {
		t.Fatalf("expected 1 iteration, got %d", res.Iterations)
	}
}

func TestRunLLMUnreachable(t *testing.T) {
	root := newBrokenGoProject(t)
	t.Setenv("OLLAMA_HOST", "http://127.0.0.1:1")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	res := Run(root, "task", "", 1, 60*time.Second)
	if res.Err == nil {
		t.Fatal("expected error when Ollama unreachable")
	}
	if !strings.Contains(res.Err.Error(), "llm round") {
		t.Fatalf("expected llm round error, got %v", res.Err)
	}
}
