package sandbox

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// cliCaptureStdout runs fn with os.Stdout redirected to a pipe and returns
// the captured output (moved from cmd/kern/json_flags_test.go).
func cliCaptureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	defer func() { os.Stdout = old }()
	fn()
	_ = w.Close()
	os.Stdout = old
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// cliJSONFixture writes a tiny Go module so index builds, validation and
// impact all have something small to work on (moved from
// cmd/kern/json_flags_test.go).
func cliJSONFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"go.mod": "module jsonclifixture\n\ngo 1.20\n",
		"main.go": `package main

func helper() string { return "h" }

func main() { _ = helper() }
`,
	}
	for rel, content := range files {
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	return dir
}

func cliAssertValidJSON(t *testing.T, out string) map[string]any {
	t.Helper()
	if !strings.HasPrefix(strings.TrimSpace(out), "{") {
		t.Fatalf("output is not a JSON object: %q", out)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	return m
}

// TestRunSandboxJSON pins the --json shape of `kern sandbox`: the sandboxed
// command runs with snapshot rollback and reports ok=true plus its output
// (moved from cmd/kern/json_flags_test.go).
func TestRunSandboxJSON(t *testing.T) {
	t.Setenv("KERN_ALLOW_EXEC", "1")
	t.Setenv("KERN_ALLOW_UNISOLATED", "1") // fail-closed gate: opt into unisolated runs on hosts without netns (darwin)
	root := cliJSONFixture(t)
	// kern flags must precede the `--` separator; everything after it is the
	// sandboxed command, verbatim (shell flags like -c included).
	out := cliCaptureStdout(t, func() { RunSandbox([]string{"--json", root, "--", "echo", "hi"}) })
	m := cliAssertValidJSON(t, out)
	if m["ok"] != true {
		t.Fatalf("expected ok=true: %v", m)
	}
	if !strings.Contains(m["output"].(string), "hi") {
		t.Fatalf("expected output to contain hi: %v", m)
	}
}

// TestRunSandboxSeparatorSplitsBeforeFlags locks the `--` separator fix: a
// shell flag after the separator must not be rejected as an unknown kern flag.
func TestRunSandboxSeparatorSplitsBeforeFlags(t *testing.T) {
	t.Setenv("KERN_ALLOW_EXEC", "1")
	t.Setenv("KERN_ALLOW_UNISOLATED", "1") // fail-closed gate: opt into unisolated runs on hosts without netns (darwin)
	root := cliJSONFixture(t)
	out := cliCaptureStdout(t, func() { RunSandbox([]string{root, "--", "sh", "-c", "exit 0"}) })
	if !strings.Contains(out, "kern: succeeded") {
		t.Fatalf("expected sandbox success with shell flags after --; got:\n%s", out)
	}
}
