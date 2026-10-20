package graphcli

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// captureStdout runs fn with os.Stdout redirected to a pipe and returns the
// captured output, so runXxx helpers can be asserted on without polluting
// test output. (Copy of the cmd/kern test helper, moved with the tests.)
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
// impact all have something small to work on. (Copy of the cmd/kern test
// helper, moved with the tests.)
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

func TestRunIndexJavaCompilerUpgradesFreshIndex(t *testing.T) {
	output, err := exec.Command("java", "-version").CombinedOutput()
	version := strings.SplitN(string(output), "\"", 3)
	if err != nil || len(version) < 2 {
		t.Skip("local JDK 17+ required")
	}
	major, _ := strconv.Atoi(strings.SplitN(version[1], ".", 2)[0])
	if major < 17 {
		t.Skip("local JDK 17+ required")
	}
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "Api.java"), []byte("class Api { int choose(int value) { return value; } int choose(String value) { return 1; } void call() { choose(1); } }"), 0o644); err != nil {
		t.Fatal(err)
	}
	captureStdout(t, func() { runIndex([]string{root, "--json"}) })
	result := assertValidJSON(t, captureStdout(t, func() { runIndex([]string{root, "--java-compiler", "--java-classpath", root, "--json"}) }))
	if result["java_compiler"] != true || result["action"] != "rebuilt" {
		t.Fatalf("Java compiler upgrade skipped: %v", result)
	}
	loaded, err := index.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.JavaCompiler || loaded.JavaClasspath != root || len(loaded.Calls["Api.call"]) != 1 || loaded.Calls["Api.call"][0].Binding != "javac" || loaded.Calls["Api.call"][0].ToID == "" {
		t.Fatalf("CLI lost Java compiler binding: %+v", loaded.Calls["Api.call"])
	}
}

func TestRunIndexGoTypesUpgradesFreshIndex(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	for name, source := range map[string]string{
		"go.mod":  "module demo\n\ngo 1.22\n",
		"main.go": "package demo\nfunc Use() { Work() }\nfunc Work() {}\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	captureStdout(t, func() { runIndex([]string{root, "--json"}) })
	out := captureStdout(t, func() { runIndex([]string{root, "--go-types", "--json"}) })
	result := assertValidJSON(t, out)
	if result["go_types"] != true || result["action"] != "rebuilt" {
		t.Fatalf("compiler upgrade skipped on a fresh index: %v", result)
	}
	loaded, err := index.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.GoTypes || loaded.Calls["Use"][0].Binding != "go/types" || loaded.Calls["Use"][0].ToID == "" {
		t.Fatalf("CLI did not persist compiler bindings: %+v", loaded.Calls["Use"])
	}
}

func TestRunIndexJSONEmitsSummary(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := jsonCliFixture(t)

	out := captureStdout(t, func() { runIndex([]string{root, "--json"}) })
	m := assertValidJSON(t, out)
	if m["freshness"] != "rebuilt" {
		t.Fatalf("cold index freshness = %v, want rebuilt", m["freshness"])
	}
	if m["action"] != "rebuilt" {
		t.Fatalf("cold index action = %v, want rebuilt", m["action"])
	}
	if m["built"] != true {
		t.Fatalf("index built = %v, want true", m["built"])
	}
	if s, ok := m["symbols"].(float64); !ok || s < 1 {
		t.Fatalf("symbols = %v, want >= 1", m["symbols"])
	}
	if f, ok := m["files"].(float64); !ok || f < 1 {
		t.Fatalf("files = %v, want >= 1", m["files"])
	}
	if _, ok := m["store"].(string); !ok {
		t.Fatalf("store missing from summary: %v", m)
	}
	if _, ok := m["languages"].([]any); !ok {
		t.Fatalf("languages missing from summary: %v", m)
	}
	if _, ok := m["root"].(string); !ok {
		t.Fatalf("root missing from summary: %v", m)
	}
}

func TestRunIndexSkipsRebuildWhenFresh(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := jsonCliFixture(t)

	// Cold: first run builds.
	out := captureStdout(t, func() { runIndex([]string{root, "--json"}) })
	m := assertValidJSON(t, out)
	if m["freshness"] != "rebuilt" || m["action"] != "rebuilt" {
		t.Fatalf("cold run = %v, want freshness=rebuilt/action=rebuilt", m)
	}
	coldSymbols, _ := m["symbols"].(float64)

	// Warm: same tree, second run must NOT rebuild.
	out = captureStdout(t, func() { runIndex([]string{root, "--json"}) })
	m = assertValidJSON(t, out)
	if m["freshness"] != "fresh" || m["action"] != "skipped" {
		t.Fatalf("warm run = %v, want freshness=fresh/action=skipped (no rebuild)", m)
	}
	if m["symbols"] != coldSymbols {
		t.Fatalf("warm symbols = %v, want %v (unchanged)", m["symbols"], coldSymbols)
	}
	if m["stale"] != false {
		t.Fatalf("warm stale = %v, want false", m["stale"])
	}

	// The text output on the warm path reports the fresh state and tells the
	// user how to force a rebuild.
	out = captureStdout(t, func() { runIndex([]string{root}) })
	if !strings.Contains(out, "FRESH") || !strings.Contains(out, "--force") {
		t.Fatalf("warm text output = %q, want FRESH + `--force` hint", out)
	}

	// --force rebuilds unconditionally.
	out = captureStdout(t, func() { runIndex([]string{root, "--json", "--force"}) })
	m = assertValidJSON(t, out)
	if m["freshness"] != "rebuilt" {
		t.Fatalf("forced run = %v, want freshness=rebuilt", m)
	}
}

// TestRunIndexRebuildsWhenStale pins warm-path invalidation: a content edit
// makes the persisted index stale, so the next `kern index` rebuilds and the
// JSON summary reports the new counts.
func TestRunIndexRebuildsWhenStale(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := jsonCliFixture(t)

	runIndex([]string{root, "--json"})

	// Adding a file changes the content root: the index is now stale.
	if err := os.WriteFile(filepath.Join(root, "extra.go"), []byte("package main\n\nfunc extra() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() { runIndex([]string{root, "--json"}) })
	m := assertValidJSON(t, out)
	if m["freshness"] != "rebuilt" {
		t.Fatalf("stale run = %v, want freshness=rebuilt", m)
	}
	if s, ok := m["symbols"].(float64); !ok || s < 2 {
		t.Fatalf("symbols = %v, want >= 2 after adding extra.go", m["symbols"])
	}
}

// TestRunIndexUpdateJSONEmitsSummary pins the update-path JSON fix: `kern
// index --update --json` must emit the same valid JSON summary the plain
// `--json` path emits (with the update-specific `reused` count) instead of
// always printing the human "index updated: ..." text line.
func TestRunIndexUpdateJSONEmitsSummary(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := jsonCliFixture(t)

	// Cold build first so --update has a previous index to update against.
	out := captureStdout(t, func() { runIndex([]string{root, "--json"}) })
	m := assertValidJSON(t, out)
	if m["action"] != "rebuilt" {
		t.Fatalf("cold run = %v, want action=rebuilt", m)
	}

	// --update --json must emit JSON, not the human text line.
	out = captureStdout(t, func() { runIndex([]string{root, "--update", "--json"}) })
	m = assertValidJSON(t, out)
	if m["action"] != "updated" {
		t.Fatalf("update run action = %v, want updated", m["action"])
	}
	if m["built"] != true {
		t.Fatalf("update run built = %v, want true", m["built"])
	}
	if m["stale"] != false {
		t.Fatalf("update run stale = %v, want false", m["stale"])
	}
	if s, ok := m["symbols"].(float64); !ok || s < 1 {
		t.Fatalf("symbols = %v, want >= 1", m["symbols"])
	}
	if f, ok := m["files"].(float64); !ok || f < 1 {
		t.Fatalf("files = %v, want >= 1", m["files"])
	}
	if _, ok := m["reused"].(float64); !ok {
		t.Fatalf("reused missing from update summary: %v", m)
	}
	if _, ok := m["store"].(string); !ok {
		t.Fatalf("store missing from update summary: %v", m)
	}
}

// TestRunIndexWhere pins F5b: `kern index where` reports the RESOLVED store
// path, the freshness verdict and the symbol count — the quick "which index
// am I serving" answer — and warns when a nested .kern shadows a parent.
func TestRunIndexWhere(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := jsonCliFixture(t)
	runIndex([]string{root}) // build the index

	out := captureStdout(t, func() { runIndex([]string{"where", root}) })
	// `kern index where` names the store the root actually serves: the
	// SQLite-primary store in the default build, the JSON cache under
	// -tags nosqlite.
	want := index.StorePath(root)
	if index.SQLiteEnabled() {
		want = index.SQLitePath(root)
	}
	if !strings.Contains(out, want) {
		t.Fatalf("where output missing store path %q: %q", want, out)
	}
	if !strings.Contains(out, "fresh") {
		t.Fatalf("where output missing freshness verdict: %q", out)
	}
	if !strings.Contains(out, "symbols:") {
		t.Fatalf("where output missing symbol count: %q", out)
	}
	if strings.Contains(out, "warning: nested index") {
		t.Fatalf("unexpected shadow warning at the root: %q", out)
	}

	// JSON form carries the same fields.
	jout := captureStdout(t, func() { runIndex([]string{"where", root, "--json"}) })
	m := assertValidJSON(t, jout)
	if m["store"] != want {
		t.Fatalf("json store = %v, want %q", m["store"], want)
	}
	if m["freshness"] != "fresh" {
		t.Fatalf("json freshness = %v, want fresh", m["freshness"])
	}
	if s, ok := m["symbols"].(float64); !ok || s < 1 {
		t.Fatalf("json symbols = %v, want >= 1", m["symbols"])
	}

	// A nested .kern under the same root must warn about the parent index.
	sub := filepath.Join(root, "pkg", "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "s.go"), []byte("package sub\n\n// S is a stub.\nfunc S() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runIndex([]string{sub}) // build the nested subdir index
	out = captureStdout(t, func() { runIndex([]string{"where", sub}) })
	if !strings.Contains(out, "warning: nested index shadows a parent index") {
		t.Fatalf("nested where output missing shadow warning: %q", out)
	}
}
