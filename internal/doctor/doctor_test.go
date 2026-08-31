package doctor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/script"
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
