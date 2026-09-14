package doctor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/script"
	"github.com/JayveerPrajapati/kern/internal/setup"
)

func TestRunReturnsFindings(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	findings := Run(t.TempDir())
	if len(findings) == 0 {
		t.Fatal("no findings produced")
	}
	seen := map[string]bool{}
	for _, f := range findings {
		if f.Check == "" || f.Level == "" {
			t.Fatalf("malformed finding: %+v", f)
		}
		seen[f.Check] = true
	}
	for _, want := range []string{"binary", "capabilities", "index", "stats"} {
		if !seen[want] {
			t.Fatalf("missing check %q in %v", want, seen)
		}
	}
}

func TestCheckCapabilities(t *testing.T) {
	f := checkCapabilities()
	if f.Level != "ok" {
		t.Fatalf("capabilities should always be ok, got %s", f.Level)
	}
	if !strings.Contains(f.Detail, "sqlite: ") || !strings.Contains(f.Detail, "treesitter: ") {
		t.Fatalf("capabilities detail missing both tags: %q", f.Detail)
	}
}

// TestCheckNetworkIsolation verifies the doctor reports the platform's
// isolation capability honestly: available with a netns (Linux unshare), or
// unavailable with the fail-closed override hint when not (macOS/Windows).
func TestCheckNetworkIsolation(t *testing.T) {
	f := checkNetworkIsolation()
	if f.Check != "network-isolation" || f.Level == "" {
		t.Fatalf("bad finding: %+v", f)
	}
	if script.NetworkIsolationAvailable() {
		if f.Level != "ok" || !strings.Contains(f.Detail, "available") {
			t.Fatalf("expected ok/available, got %+v", f)
		}
	} else {
		if f.Level != "warn" || !strings.Contains(f.Detail, "fail closed") {
			t.Fatalf("expected warn with fail-closed hint, got %+v", f)
		}
		if !strings.Contains(f.Detail, "KERN_ALLOW_UNISOLATED") {
			t.Fatalf("detail missing override hint: %q", f.Detail)
		}
	}
}

func TestCheckFreshnessReportsStale(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	// Build an index for an empty-ish tree, then touch a source file so the
	// cached index becomes stale relative to the tree.
	writeGoFile(t, root, "a.go")
	ix, err := index.Build(root)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if err := ix.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if f := checkIndexFreshness(root); f.Level != "ok" {
		t.Fatalf("fresh index reported %s: %+v", f.Level, f)
	}
	writeGoFile(t, root, "b.go") // new file after build → stale
	if f := checkIndexFreshness(root); f.Level != "warn" || !strings.Contains(f.Detail, "STALE") {
		t.Fatalf("stale index reported %s: %+v", f.Level, f)
	}
	// No cached index at all → ok, not a double-fail.
	other := t.TempDir()
	writeGoFile(t, other, "a.go")
	if f := checkIndexFreshness(other); f.Level != "ok" {
		t.Fatalf("no-index reported %s: %+v", f.Level, f)
	}
}

func writeGoFile(t *testing.T, root, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte("package p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRender(t *testing.T) {
	findings := []Finding{
		{Check: "binary", Level: "ok", Detail: "/x/kern"},
		{Check: "ollama", Level: "warn", Detail: "not reachable"},
		{Check: "index", Level: "fail", Detail: "no source files"},
	}
	out := Render("/tmp", findings)
	for _, want := range []string{"# kern doctor", "[ok]", "[warn]", "[fail]", "verdict: failures"} {
		if !strings.Contains(out, want) {
			t.Fatalf("render missing %q:\n%s", want, out)
		}
	}
}

func TestCheckPrecision_NoIndex(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	f := checkPrecision(t.TempDir())
	if f.Check != "precision" || f.Level != "warn" {
		t.Fatalf("no-index precision = %+v, want warn", f)
	}
	if !strings.Contains(f.Detail, "no index found") {
		t.Fatalf("no-index detail = %q, want 'no index found'", f.Detail)
	}
}

// TestCheckPrecision_DefaultBuild verifies the regex build reports the honest
// precision split: Go + Java resolved, everything else heuristic, with the
// tree-sitter upgrade hint. Skipped under -tags treesitter where the tiers
// are ast/resolved (covered by TestCheckPrecision_TreeSitterBuild).
func TestCheckPrecision_DefaultBuild(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if index.TreesitterEnabled() {
		t.Skip("default-build behavior not applicable under -tags treesitter")
	}
	root := t.TempDir()
	writeGoFile(t, root, "a.go")
	writeFixtureFile(t, root, "app.ts", "export function handle(): void { helper(); }\nfunction helper(): void {}\n")
	writeFixtureFile(t, root, "App.java", "public class App { public void run() { util(); } public void util() {} }\n")
	ix, err := index.Build(root)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if err := ix.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	f := checkPrecision(root)
	if f.Level != "warn" {
		t.Fatalf("default-build precision = %s, want warn: %+v", f.Level, f)
	}
	for _, want := range []string{"1 at heuristic precision", "typescript", "skipped under --precision strict", "-tags treesitter"} {
		if !strings.Contains(f.Detail, want) {
			t.Fatalf("detail missing %q: %s", want, f.Detail)
		}
	}
}

// TestCheckPrecision_AllResolvedBuild covers the ok branch in the default
// build: an index whose languages are all resolved (Go + Java only) reports
// ok, not the heuristic warning.
func TestCheckPrecision_AllResolvedBuild(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	writeGoFile(t, root, "a.go")
	writeFixtureFile(t, root, "App.java", "public class App { public void run() { util(); } public void util() {} }\n")
	ix, err := index.Build(root)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if err := ix.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	f := checkPrecision(root)
	if f.Level != "ok" {
		t.Fatalf("all-resolved precision = %s, want ok: %+v", f.Level, f)
	}
	if !strings.Contains(f.Detail, "languages at") {
		t.Fatalf("detail missing precision claim: %q", f.Detail)
	}
}

func writeFixtureFile(t *testing.T, root, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestCheckPluginSyncDrift verifies the D1 doctor check: an installed plugin
// copy that differs from the embedded asset is reported as a warning, and a
// matching copy reports ok. It writes to a temp HOME/XDG so the machine's real
// plugin files are not touched.
func TestCheckPluginSyncDrift(t *testing.T) {
	if _, err := setup.PluginAsset(); err != nil {
		t.Skipf("embedded plugin asset unavailable: %v", err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	// No copies installed: no findings (installation is the wiring check's job).
	if got := checkPluginSync(); len(got) != 0 {
		t.Errorf("no installed copies should yield no findings, got %+v", got)
	}

	// Matching copy → ok.
	p := filepath.Join(home, ".config", "opencode", "plugins", "kern.ts")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	src, _ := setup.PluginAsset()
	if err := os.WriteFile(p, src, 0o644); err != nil {
		t.Fatal(err)
	}
	got := checkPluginSync()
	if len(got) != 1 || got[0].Level != "ok" {
		t.Errorf("matching copy should be ok, got %+v", got)
	}

	// Stale copy → warn with the fix hint.
	if err := os.WriteFile(p, []byte("// stale copy\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got = checkPluginSync()
	if len(got) != 1 || got[0].Level != "warn" {
		t.Fatalf("stale copy should warn, got %+v", got)
	}
	if !strings.Contains(got[0].Detail, "kern setup --global") {
		t.Errorf("warning should include the fix hint: %s", got[0].Detail)
	}
}

// D5: .kern/config.json validity — malformed is a fail (it silently degrades
// every config lookup to defaults), valid is ok, and known verify.* keys with
// non-string values warn.
func TestCheckConfig(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	root := t.TempDir()
	f := checkConfig(root)
	if f.Level != "ok" || !strings.Contains(f.Detail, "no .kern/config.json") {
		t.Errorf("missing config = %+v, want ok/no-file", f)
	}

	dir := filepath.Join(root, ".kern")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"verify":{"test": 42}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	f = checkConfig(root)
	if f.Level != "warn" || !strings.Contains(f.Detail, "verify.test must be a string") {
		t.Errorf("wrong-type key = %+v, want warn", f)
	}

	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"verify":{"build":"npm run build"},"verify":{"test":"go test ./..."}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	f = checkConfig(root)
	if f.Level != "ok" {
		t.Errorf("valid config = %+v, want ok", f)
	}

	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"verify": {`), 0o644); err != nil {
		t.Fatal(err)
	}
	f = checkConfig(root)
	if f.Level != "fail" {
		t.Errorf("malformed config = %+v, want fail", f)
	}
}

// D5: env check echoes every KERN_* var and validates the known ones.
func TestCheckEnvEchoesAndValidates(t *testing.T) {
	t.Setenv("KERN_LLM_PROVIDER", "bogus")
	t.Setenv("KERN_MCP_WATCH_INTERVAL", "not-a-number")
	t.Setenv("KERN_ALLOW_EXEC", "1")
	t.Setenv("KERN_MODEL", "qwen2.5-coder")
	f := checkEnv()
	if f.Level != "warn" {
		t.Fatalf("invalid env = %+v, want warn", f)
	}
	for _, want := range []string{"KERN_LLM_PROVIDER=bogus (unknown provider)", "KERN_MCP_WATCH_INTERVAL=not-a-number (want a positive integer)", "KERN_MODEL=qwen2.5-coder"} {
		if !strings.Contains(f.Detail, want) {
			t.Errorf("env detail missing %q in:\n%s", want, f.Detail)
		}
	}
	// KERN_ALLOW_EXEC=1 must NOT be flagged.
	if strings.Contains(f.Detail, "KERN_ALLOW_EXEC=1 (fail-closed") {
		t.Errorf("valid toggle flagged: %s", f.Detail)
	}

	t.Setenv("KERN_LLM_PROVIDER", "ollama")
	t.Setenv("KERN_MCP_WATCH_INTERVAL", "5")
	f = checkEnv()
	if f.Level != "ok" {
		t.Errorf("valid env = %+v, want ok", f)
	}
}

// D5: cache scan flags zero-byte JSON files as corruption markers.
func TestCheckCacheDetectsZeroByteJSON(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", dir)
	cachePath := filepath.Join(dir, "kern") // cache.Dir derives from XDG_CACHE_HOME
	if err := os.MkdirAll(cachePath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cachePath, "a.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cachePath, "truncated.json"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	f := checkCache()
	if f.Level != "fail" || !strings.Contains(f.Detail, "zero-byte") {
		t.Errorf("zero-byte json = %+v, want fail", f)
	}
	_ = os.Remove(filepath.Join(cachePath, "truncated.json"))
	f = checkCache()
	if f.Level != "ok" {
		t.Errorf("clean cache = %+v, want ok", f)
	}
}

// D5: version reports the stamp; an unstamped dev build warns.
func TestCheckVersion(t *testing.T) {
	f := checkVersion()
	if f.Check != "version" || f.Detail == "" {
		t.Fatalf("version = %+v", f)
	}
	if !strings.Contains(f.Detail, "kern ") || !strings.Contains(f.Detail, "go") {
		t.Errorf("version detail = %q, want kern <v> · go<ver> · os/arch", f.Detail)
	}
	if f.Level != "ok" && f.Level != "warn" {
		t.Errorf("version level = %q, want ok|warn", f.Level)
	}
}

// TestCheckRuntime pins the production-intelligence diagnostic: no source
// warns with the enable hint; a wired local snapshot reports its telemetry
// counts, and one with errors warns.
func TestCheckRuntime(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root) // isolate from any cwd-level env/config

	f := checkRuntime(root)
	if f.Check != "runtime" || f.Level != "warn" {
		t.Fatalf("no-source finding = %+v, want runtime/warn", f)
	}
	if !strings.Contains(f.Detail, "KERN_PROMETHEUS_URL") {
		t.Fatalf("no-source detail missing enable hint: %q", f.Detail)
	}

	if err := os.MkdirAll(filepath.Join(root, ".kern"), 0o755); err != nil {
		t.Fatal(err)
	}
	clean := `{"events":[{"id":"e1","type":"metric","service":"app","severity":"info","message":"rps=1"}]}`
	if err := os.WriteFile(filepath.Join(root, ".kern", "runtime.json"), []byte(clean), 0o644); err != nil {
		t.Fatal(err)
	}
	f = checkRuntime(root)
	if f.Level != "ok" {
		t.Fatalf("clean-source finding = %+v, want ok", f)
	}
	for _, want := range []string{"local", "1 events", "0 errors", "1 service"} {
		if !strings.Contains(f.Detail, want) {
			t.Errorf("clean detail missing %q in:\n%s", want, f.Detail)
		}
	}

	withErrors := `{"events":[
		{"id":"e1","type":"metric","service":"app","severity":"info","message":"rps=1"},
		{"id":"e2","type":"error","service":"app","severity":"error","message":"boom"}
	]}`
	if err := os.WriteFile(filepath.Join(root, ".kern", "runtime.json"), []byte(withErrors), 0o644); err != nil {
		t.Fatal(err)
	}
	f = checkRuntime(root)
	if f.Level != "warn" {
		t.Fatalf("error-source finding = %+v, want warn", f)
	}
	if !strings.Contains(f.Detail, "production errors present") {
		t.Fatalf("error detail missing marker: %q", f.Detail)
	}
}

func TestCheckExecDetectsSIGKILL(t *testing.T) {
	// Regression (e2e 2026-09-13): a binary killed by SIGKILL at exec
	// (macOS Gatekeeper) reported the generic "failed to run: signal:
	// killed" instead of the actionable re-sign guidance, because the
	// ExitCode()==137 check never fires for a direct signal death
	// (ExitCode is -1; the 137 form only appears shell-wrapped).
	dir := t.TempDir()
	bin := filepath.Join(dir, "selfkill")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nkill -9 $$\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	f := checkExec(bin)
	if f.Level != "fail" {
		t.Fatalf("expected fail level for SIGKILLed binary, got %+v", f)
	}
	if !strings.Contains(f.Detail, "SIGKILL") || !strings.Contains(f.Detail, "re-sign") {
		t.Fatalf("expected Gatekeeper re-sign guidance in detail, got %q", f.Detail)
	}
}

func TestCheckMultiRepoFreshness(t *testing.T) {
	parent := t.TempDir()

	subA := filepath.Join(parent, "repo-a")
	subB := filepath.Join(parent, "repo-b")
	_ = os.MkdirAll(filepath.Join(subA, ".git"), 0o755)
	_ = os.MkdirAll(filepath.Join(subB, ".git"), 0o755)

	writeGoFile(t, subA, "a.go")
	writeGoFile(t, subB, "b.go")

	ixA, err := index.Build(subA)
	if err != nil {
		t.Fatal(err)
	}
	if err := ixA.Save(); err != nil {
		t.Fatal(err)
	}

	ixB, err := index.Build(subB)
	if err != nil {
		t.Fatal(err)
	}
	if err := ixB.Save(); err != nil {
		t.Fatal(err)
	}

	// In parent workspace with no root index, child indices must aggregate as fresh
	f := checkIndexFreshness(parent)
	if f.Level != "ok" || !strings.Contains(f.Detail, "multi-repo index is fresh") {
		t.Fatalf("expected ok multi-repo freshness, got %+v", f)
	}

	fi := checkIndex(parent)
	if fi.Level != "ok" || !strings.Contains(fi.Detail, "multi-repo index") {
		t.Fatalf("expected ok multi-repo index, got %+v", fi)
	}
}
