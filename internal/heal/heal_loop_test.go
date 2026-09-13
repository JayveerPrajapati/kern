package heal

import (
	"context"
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
	if got := failingFiles(t.TempDir(), out); len(got) != 0 {
		t.Fatalf("expected no resolvable files, got %v", got)
	}
}

func TestFailingFilesDedupsAndResolves(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "app.go"), []byte("x"), 0o644)

	out := "app.go:3:1: syntax error\napp.go:5:1: again\nother.go:2:1: nope"
	got := failingFiles(dir, out)
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

	res := Run(context.Background(), root, "fix the build", "", 3, 60*time.Second, false)
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

	res := Run(context.Background(), root, "task", "", 3, 30*time.Second, false)
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
	res := Run(context.Background(), root, "task", "", 3, 5*time.Second, false)
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

	res := Run(context.Background(), root, "task", "", 1, 60*time.Second, false)
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
	// Pin the provider to ollama: with the auto chain, an unreachable ollama
	// would fall through to the locally-installed agent CLIs (claude/
	// opencode/...) and make this hermetic test depend on the machine's
	// agent installs and login state.
	t.Setenv("KERN_LLM_PROVIDER", "ollama")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	res := Run(context.Background(), root, "task", "", 1, 60*time.Second, false)
	if res.Err == nil {
		t.Fatal("expected error when Ollama unreachable")
	}
	if !strings.Contains(res.Err.Error(), "llm round") {
		t.Fatalf("expected llm round error, got %v", res.Err)
	}
}

// brokenHubProject builds a Go project whose build failure names hub.go,
// and Hub has 11 callers (HIGH pre-edit verdict): the undefined name keeps
// the file parseable so the hub stays indexed.
func brokenHubProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module demo\n\ngo 1.22\n")
	write("hub.go", "package main\n\nfunc Hub() int { return undefinedName }\n")
	write("app.go", "package main\n\nfunc main() { println(Hub()) }\n")
	for i := 0; i < 10; i++ {
		write("c"+itoa(i)+".go", "package main\n\nfunc Caller"+itoa(i)+"() int { return Hub() }\n")
	}
	return root
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	s := ""
	for i > 0 {
		s = string(rune('0'+i%10)) + s
		i /= 10
	}
	return s
}

// TestRunRefusesHighRiskRepair pins the P2 heal gate: when the failing file
// carries a HIGH verdict the loop refuses before spending any LLM round,
// unless forced.
func TestRunRefusesHighRiskRepair(t *testing.T) {
	root := brokenHubProject(t)
	srv := mockOllama(t, "### FILE: hub.go\npackage main\n\nfunc Hub() int { return 42 }\n")
	defer srv.Close()
	t.Setenv("OLLAMA_HOST", srv.URL)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	res := Run(context.Background(), root, "fix the build", "", 3, 60*time.Second, false)
	if res.Err == nil || !strings.Contains(res.Err.Error(), "--force") {
		t.Fatalf("expected HIGH refusal naming --force, got %+v", res)
	}
	if res.Iterations != 0 {
		t.Fatalf("refusal must precede any LLM round, iterations = %d", res.Iterations)
	}
}

// TestRunForceRepairsHighRisk pins the override: force=true heals the hub.
func TestRunForceRepairsHighRisk(t *testing.T) {
	root := brokenHubProject(t)
	srv := mockOllama(t, "### FILE: hub.go\npackage main\n\nfunc Hub() int { return 42 }\n")
	defer srv.Close()
	t.Setenv("OLLAMA_HOST", srv.URL)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	res := Run(context.Background(), root, "fix the build", "", 3, 60*time.Second, true)
	if res.Err != nil {
		t.Fatalf("forced heal must proceed, got err: %v", res.Err)
	}
	if !res.Validated {
		t.Fatalf("forced heal must validate, got %+v", res)
	}
}
