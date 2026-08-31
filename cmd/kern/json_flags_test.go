package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// captureStdout runs fn with os.Stdout redirected to a pipe and returns the
// captured output, so runXxx helpers can be asserted on without polluting
// test output.
func captureStdout(t *testing.T, fn func()) string {
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

// jsonCliFixture writes a tiny Go module so index builds, validation and
// impact all have something small to work on.
func jsonCliFixture(t *testing.T) string {
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

func assertValidJSON(t *testing.T, out string) map[string]any {
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

func TestRunDoctorJSON(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := jsonCliFixture(t)
	out := captureStdout(t, func() { runDoctor([]string{root, "--json"}) })
	// doctor prints its []Finding directly as a JSON array.
	if !strings.HasPrefix(strings.TrimSpace(out), "[") {
		t.Fatalf("output is not a JSON array: %q", out)
	}
	var findings []struct {
		Check  string
		Level  string
		Detail string
	}
	if err := json.Unmarshal([]byte(out), &findings); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if len(findings) == 0 {
		t.Fatal("no findings in JSON output")
	}
	for _, f := range findings {
		if f.Check == "" || f.Level == "" {
			t.Fatalf("malformed finding: %+v", f)
		}
	}
	// The freshness and precision checks must be present in the JSON output.
	for _, want := range []string{"freshness", "precision"} {
		found := false
		for _, f := range findings {
			if f.Check == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("%s finding missing: %+v", want, findings)
		}
	}
}

// TestRunDoctor_IncludesPrecisionCheck verifies the human-readable doctor
// report carries the precision finding, which honestly states how many
// languages are resolved vs heuristic for this build.
func TestRunDoctor_IncludesPrecisionCheck(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := jsonCliFixture(t)
	runIndex([]string{root}) // build so precision has an index to report on
	out := captureStdout(t, func() { runDoctor([]string{root}) })
	if !strings.Contains(out, "[") || !strings.Contains(out, "precision") {
		t.Fatalf("doctor output missing precision check:\n%s", out)
	}
	if !strings.Contains(out, "resolved") {
		t.Fatalf("doctor precision finding should mention resolved:\n%s", out)
	}
}

func TestRunIndexStatusJSON(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := jsonCliFixture(t)
	// Before build: NOT BUILT.
	out := captureStdout(t, func() { runIndex([]string{root, "--status", "--json"}) })
	m := assertValidJSON(t, out)
	if m["built"] != false {
		t.Fatalf("expected built=false before build: %v", m)
	}
	// After build: BUILT with symbols and stale=false.
	runIndex([]string{root})
	out = captureStdout(t, func() { runIndex([]string{root, "--status", "--json"}) })
	m = assertValidJSON(t, out)
	if m["built"] != true {
		t.Fatalf("expected built=true after build: %v", m)
	}
	if s, ok := m["symbols"].(float64); !ok || s < 1 {
		t.Fatalf("expected symbols >= 1: %v", m)
	}
	if m["stale"] != false {
		t.Fatalf("expected stale=false right after build: %v", m)
	}
	// Per-language precision tiers and the build's tree-sitter capability
	// must be surfaced so consumers can see which languages are skipped
	// under --precision strict.
	pt, ok := m["precision_by_lang"].(map[string]any)
	if !ok {
		t.Fatalf("expected precision_by_lang map: %v", m)
	}
	if pt["go"] != "resolved" {
		t.Fatalf("expected precision_by_lang[go]=resolved: %v", pt)
	}
	if _, ok := m["tree_sitter_enabled"].(bool); !ok {
		t.Fatalf("expected tree_sitter_enabled bool: %v", m)
	}
	// A new source file makes the cached index stale.
	if err := os.WriteFile(filepath.Join(root, "extra.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out = captureStdout(t, func() { runIndex([]string{root, "--status", "--json"}) })
	m = assertValidJSON(t, out)
	if m["stale"] != true {
		t.Fatalf("expected stale=true after adding a file: %v", m)
	}
}

func TestRunValidateJSON(t *testing.T) {
	t.Setenv("KERN_ALLOW_EXEC", "1")
	root := jsonCliFixture(t)
	// Success path only: a failing validation exits the process, which cannot
	// be asserted in-process. The JSON shape is shared with the failure path.
	out := captureStdout(t, func() { runValidate([]string{root, "--json"}) })
	m := assertValidJSON(t, out)
	if m["ok"] != true {
		t.Fatalf("expected ok=true: %v", m)
	}
	if m["duration_ms"].(float64) < 0 {
		t.Fatalf("expected non-negative duration_ms: %v", m)
	}
}

func TestRunSandboxJSON(t *testing.T) {
	t.Setenv("KERN_ALLOW_EXEC", "1")
	root := jsonCliFixture(t)
	// kern flags must precede the `--` separator; everything after it is the
	// sandboxed command, verbatim (shell flags like -c included).
	out := captureStdout(t, func() { runSandbox([]string{"--json", root, "--", "echo", "hi"}) })
	m := assertValidJSON(t, out)
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
	root := jsonCliFixture(t)
	out := captureStdout(t, func() { runSandbox([]string{root, "--", "sh", "-c", "exit 0"}) })
	if !strings.Contains(out, "kern: succeeded") {
		t.Fatalf("expected sandbox success with shell flags after --; got:\n%s", out)
	}
}

func TestRunWhatIfJSON(t *testing.T) {
	root := jsonCliFixture(t)
	out := captureStdout(t, func() { runWhatIf("what-if", []string{"helper", "--root", root, "--json"}) })
	m := assertValidJSON(t, out)
	if _, ok := m["impact"]; !ok {
		t.Fatalf("expected impact object: %v", m)
	}
	if m["change"] != "helper" {
		t.Fatalf("expected change=helper: %v", m)
	}
}

func TestRunImpactJSON(t *testing.T) {
	root := jsonCliFixture(t)
	out := captureStdout(t, func() { runImpact([]string{"helper", "--root", root, "--json"}) })
	m := assertValidJSON(t, out)
	if _, ok := m["impact"]; !ok {
		t.Fatalf("expected impact object: %v", m)
	}
	if m["change"] != "helper" {
		t.Fatalf("expected change=helper: %v", m)
	}
	if m["task_id"] == "" {
		t.Fatalf("expected task_id: %v", m)
	}
}

func TestRunTestGapsJSON(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := jsonCliFixture(t)
	runIndex([]string{root})
	out := captureStdout(t, func() { runTestgaps([]string{root, "--json"}) })
	m := assertValidJSON(t, out)
	if _, ok := m["coverage"]; !ok {
		t.Fatalf("expected coverage object: %v", m)
	}
	if _, ok := m["gaps"]; !ok {
		t.Fatalf("expected gaps array: %v", m)
	}
}
