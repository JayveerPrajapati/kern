package doctor

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/guardpause"
	"sync/atomic"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/script"
	"github.com/JayveerPrajapati/kern/internal/setup"
	"github.com/JayveerPrajapati/kern/internal/version"
)

// TestCheckAuditChainSize pins the F9 doctor surface: a missing chain is ok,
// a small chain is ok with its size reported, and a chain past the 50 MB
// threshold warns with archive guidance (the chain is hash-linked, so kern
// never rotates it in place — the operator archives instead). The large
// fixture is a sparse file: truncation, not content, sets the size.
func TestCheckAuditChainSize(t *testing.T) {
	root := t.TempDir()
	if f := checkAuditChainSize(root); f.Level != "ok" {
		t.Fatalf("missing chain must be ok, got %+v", f)
	}
	auditDir := filepath.Join(root, ".kern", "audit")
	if err := os.MkdirAll(auditDir, 0o755); err != nil {
		t.Fatal(err)
	}
	chain := filepath.Join(auditDir, "chain.jsonl")
	if err := os.WriteFile(chain, []byte("{\"k\":\"a\",\"v\":{}}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if f := checkAuditChainSize(root); f.Level != "ok" || !strings.Contains(f.Detail, "MB") {
		t.Fatalf("small chain must be ok with its size, got %+v", f)
	}
	if err := os.Truncate(chain, 51<<20); err != nil {
		t.Fatal(err)
	}
	f := checkAuditChainSize(root)
	if f.Level != "warn" || !strings.Contains(f.Detail, "archive") {
		t.Fatalf("large chain must warn with archive guidance, got %+v", f)
	}
}

// repoRoot walks up from the package directory to the go.mod owner (a git
// checkout, which checkParity needs).
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above " + dir)
		}
		dir = parent
	}
}

// TestCheckParityReleaseTagAccepted pins the checkParity release-shape
// classification after the tagRe -> version.Provenance dedupe: release
// stamps — including 4-component tags like v0.9.9.1 — are accepted as "ok"
// (a tag build cannot be mapped to a HEAD hash), a matching hash stamp is
// "ok", a stale hash is "warn", and a non-release-shaped stamp (5 numeric
// components) is no longer tag-shaped and falls to the stale warn.
func TestCheckParityReleaseTagAccepted(t *testing.T) {
	orig := version.Version
	defer func() { version.Version = orig }()
	root := repoRoot(t)
	// Worktree-safety: the closed loop's verify stage runs `go test` inside
	// sandbox worktrees that exclude .git by design; checkParity cannot
	// compare a stamp without git metadata and correctly warns. Skip there.
	if shortHashAt(root) == "" {
		t.Skip("not a git checkout — checkParity needs git metadata (loop worktrees exclude .git)")
	}

	version.Version = "v0.9.9.1"
	if f := checkParity(root); f.Level != "ok" || !strings.Contains(f.Detail, "release build") {
		t.Errorf("checkParity(v0.9.9.1) = %+v, want ok release build", f)
	}
	version.Version = "v0.9.9"
	if f := checkParity(root); f.Level != "ok" {
		t.Errorf("checkParity(v0.9.9) = %+v, want ok", f)
	}
	// A stamp matching HEAD is ok (parity proven).
	version.Version = shortHashAt(root)
	if f := checkParity(root); f.Level != "ok" {
		t.Errorf("checkParity(HEAD hash) = %+v, want ok", f)
	}
	// A non-release-shaped stamp (5 components) is Unknown provenance and
	// must NOT be accepted as a release build.
	version.Version = "v1.2.3.4.5"
	if f := checkParity(root); f.Level != "warn" {
		t.Errorf("checkParity(v1.2.3.4.5) = %+v, want warn (not release-shaped)", f)
	}
}

// shortHashAt returns the repo HEAD short hash at root ("" outside a git
// checkout).
func shortHashAt(root string) string {
	out, err := exec.Command("git", "-C", root, "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// TestCheckParitySkipsForeignRepo pins the 5.2 fix: the binary/HEAD parity
// comparison may only run inside a kern checkout (go.mod module ==
// github.com/JayveerPrajapati/kern). Against a foreign repo — the psf/requests
// dogfooding report: a stamped kern binary compared against requests' HEAD
// produced "installed binary is stale; rebuild and reinstall", nudging a
// pointless reinstall — the check must skip with a "not a kern checkout" note
// instead of a stale verdict.
func TestCheckParitySkipsForeignRepo(t *testing.T) {
	orig := version.Version
	defer func() { version.Version = orig }()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module github.com/psf/requests\n\ngo 1.27\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Make it a real git checkout with a HEAD so the pre-fix code path
	// (stamp vs repo HEAD) would genuinely have produced the stale verdict.
	if err := exec.Command("git", "-C", dir, "init", "-q").Run(); err != nil {
		t.Skip("git unavailable: " + err.Error())
	}
	commit := exec.Command("git", "-C", dir, "-c", "user.email=test@example.com", "-c", "user.name=test", "commit", "-q", "--allow-empty", "-m", "init")
	if err := commit.Run(); err != nil {
		t.Skip("git commit unavailable: " + err.Error())
	}

	// A build stamp that differs from the foreign repo's HEAD would have
	// produced the stale verdict before the fix.
	version.Version = "deadbeef"
	f := checkParity(dir)
	if f.Level != "ok" {
		t.Fatalf("checkParity(foreign repo) = %+v, want ok (skip)", f)
	}
	if !strings.Contains(f.Detail, "not a kern checkout") {
		t.Errorf("detail should say the repo is not a kern checkout, got: %q", f.Detail)
	}
	if strings.Contains(f.Detail, "stale") {
		t.Errorf("foreign repo must not produce a stale-binary verdict, got: %q", f.Detail)
	}
	if isKernCheckout(dir) {
		t.Error("isKernCheckout(foreign repo) = true, want false")
	}
	if !isKernCheckout(repoRoot(t)) {
		t.Error("isKernCheckout(kern repo) = false, want true")
	}
}

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

// TestCheckPrecision_DefaultBuild verifies the pure-Go (regex) build reports
// the honest precision split: Go + Java resolved, everything else heuristic,
// with the tree-sitter upgrade hint. Skipped in the default tree-sitter build
// where the tiers are ast/resolved (covered by
// TestCheckPrecision_TreeSitterBuild).
func TestCheckPrecision_DefaultBuild(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if index.TreesitterEnabled() {
		t.Skip("pure-Go precision behavior not applicable in the default tree-sitter build")
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
	for _, want := range []string{"1 at heuristic precision", "typescript", "skipped under --precision strict", "-tags notreesitter"} {
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
	// LOW-14: KERN_READONLY always has a state row, even when unset.
	if !strings.Contains(f.Detail, "KERN_READONLY unset") {
		t.Errorf("unset KERN_READONLY missing its state row: %s", f.Detail)
	}
	t.Setenv("KERN_READONLY", "1")
	f = checkEnv()
	if !strings.Contains(f.Detail, "KERN_READONLY=1") || strings.Count(f.Detail, "KERN_READONLY") != 1 {
		t.Errorf("set KERN_READONLY must appear exactly once (state row, no duplicate echo): %s", f.Detail)
	}
}

// R2: doctor must surface the hook guard’s un-audited disable switches.
// A missing marker and KERN_DISABLED unset report ok; each active marker warns
// without implying that the exec firewall was bypassed.
func TestCheckGuardSwitches(t *testing.T) {
	root := t.TempDir()
	var f Finding
	for _, d := range guardpause.DoctorFindings(root) {
		if d.Check == "guard-switches" {
			f = Finding{Check: d.Check, Level: d.Level, Detail: d.Detail}
		}
	}
	if f.Level != "ok" || !strings.Contains(f.Detail, "no agent hook disable switches") {
		t.Fatalf("clean = %+v, want ok", f)
	}

	t.Setenv("KERN_DISABLED", "1")
	off := filepath.Join(root, ".kern", "off")
	if err := os.MkdirAll(filepath.Dir(off), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(off, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, d := range guardpause.DoctorFindings(root) {
		if d.Check == "guard-switches" {
			f = Finding{Check: d.Check, Level: d.Level, Detail: d.Detail}
		}
	}
	if f.Level != "warn" {
		t.Fatalf("disabled = %+v, want warn", f)
	}
	for _, want := range []string{"KERN_DISABLED=1", ".kern/off", "un-audited switch", "exec firewall still enforces"} {
		if !strings.Contains(f.Detail, want) {
			t.Errorf("detail missing %q: %s", want, f.Detail)
		}
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
// counts, and one with errors warns. Live sources are DESCRIBED, never
// constructed — doctor must stay offline (A3) — and a malformed configured
// URL gets its own warning instead of a silent nil.
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

// TestCheckRuntimeLiveWiringNoNetwork pins the A3 contract: a configured live
// source is reported as wiring (kind, endpoint, credential presence) WITHOUT
// contacting the endpoint — doctor constructs no poller, so the test server
// must receive zero requests.
func TestCheckRuntimeLiveWiringNoNetwork(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)

	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		_, _ = w.Write([]byte(`# unreachable path must never be polled`))
	}))
	defer srv.Close()

	t.Setenv("KERN_PROMETHEUS_URL", srv.URL)
	t.Setenv("KERN_PROMETHEUS_TOKEN", "sekrit")

	f := checkRuntime(root)
	if f.Check != "runtime" || f.Level != "ok" {
		t.Fatalf("live-wiring finding = %+v, want runtime/ok", f)
	}
	for _, want := range []string{"prometheus", srv.URL, "KERN_PROMETHEUS_TOKEN", "wiring check only"} {
		if !strings.Contains(f.Detail, want) {
			t.Errorf("live-wiring detail missing %q in:\n%s", want, f.Detail)
		}
	}
	if n := atomic.LoadInt32(&hits); n != 0 {
		t.Fatalf("doctor polled the live endpoint %d time(s) — checkRuntime must stay offline", n)
	}
}

// TestCheckRuntimeInvalidConfig pins the malformed-endpoint diagnostic: a
// configured URL that fails validation is a distinct warn naming the URL and
// the reason, never a silent "no runtime source".
func TestCheckRuntimeInvalidConfig(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)

	t.Setenv("KERN_OTEL_URL", "not-a-url")
	f := checkRuntime(root)
	if f.Check != "runtime" || f.Level != "warn" {
		t.Fatalf("invalid-config finding = %+v, want runtime/warn", f)
	}
	for _, want := range []string{"otel", "not-a-url", "invalid", "scheme must be http or https"} {
		if !strings.Contains(f.Detail, want) {
			t.Errorf("invalid-config detail missing %q in:\n%s", want, f.Detail)
		}
	}
}

func TestCheckExecDetectsSIGKILL(t *testing.T) {
	// Load guard: under full-suite parallelism a process spawn can exceed the
	// 10s default and misreport as a handshake timeout instead of the SIGKILL
	// this test asserts on.
	t.Setenv("KERN_DOCTOR_EXEC_TIMEOUT_MS", "30000")
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

// TestCheckExecMCPInitializeHandshake pins the N6 fix: binary-exec passes
// only when the binary answers a real MCP initialize handshake. A stub that
// answers initialize passes (with the server name/version in the detail); a
// stub that only prints usage fails.
func TestCheckExecMCPInitializeHandshake(t *testing.T) {
	// Load guard: see TestCheckExecDetectsSIGKILL — each subtest spawns
	// several stub processes; under full-suite parallelism the default 10s
	// probe deadline can expire mid-handshake.
	t.Setenv("KERN_DOCTOR_EXEC_TIMEOUT_MS", "30000")
	dir := t.TempDir()

	// A stub that answers the initialize handshake passes.
	good := filepath.Join(dir, "good-mcp")
	stub := "#!/bin/sh\ncat >/dev/null\nprintf '%s\\n' '{\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"protocolVersion\":\"2024-11-05\",\"capabilities\":{},\"serverInfo\":{\"name\":\"kern-mcp\",\"version\":\"0.9.9\"}}}'\n"
	if err := os.WriteFile(good, []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	f := checkExec(good)
	if f.Level != "ok" {
		t.Fatalf("initialize-answering stub = %+v, want ok", f)
	}
	if !strings.Contains(f.Detail, "handshake ok") || !strings.Contains(f.Detail, "kern-mcp/0.9.9") {
		t.Fatalf("ok detail = %q, want handshake ok + server name/version", f.Detail)
	}

	// A stub that only prints usage fails the new check.
	bad := filepath.Join(dir, "usage-only")
	if err := os.WriteFile(bad, []byte("#!/bin/sh\nprintf '%s\\n' 'Usage of kern-mcp: ...'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	f = checkExec(bad)
	if f.Level != "fail" {
		t.Fatalf("usage-only stub = %+v, want fail", f)
	}
	if !strings.Contains(f.Detail, "handshake") {
		t.Fatalf("fail detail should name the handshake, got %q", f.Detail)
	}

	// A stub that exits without answering (empty stdout) also fails.
	quiet := filepath.Join(dir, "quiet")
	if err := os.WriteFile(quiet, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if f := checkExec(quiet); f.Level != "fail" {
		t.Fatalf("quiet stub = %+v, want fail", f)
	}
}

// TestCheckPathPATHFallback pins the doctor-path fix: when the running
// binary has no kern-mcp sibling (a dev build run from /tmp), checkPath
// must not warn while a real kern-mcp resolves on PATH — agents find it
// there. Only when both the sibling and the PATH lookups miss does it warn.
func TestCheckPathPATHFallback(t *testing.T) {
	dir := t.TempDir()
	stub := filepath.Join(dir, "kern-mcp")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	f := checkPath()
	if f.Level != "ok" {
		t.Fatalf("checkPath with kern-mcp on PATH = %+v, want ok", f)
	}
	if !strings.Contains(f.Detail, "(via PATH)") {
		t.Fatalf("ok detail = %q, want \"(via PATH)\" suffix", f.Detail)
	}
}

// TestParseMCPInitializeResponse pins the response parser: a valid response
// yields the server name/version; a usage line or garbage yields not-ok.
func TestParseMCPInitializeResponse(t *testing.T) {
	server, ok := parseMCPInitializeResponse([]byte(`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2024-11-05","capabilities":{},"serverInfo":{"name":"kern","version":"dev"}}}` + "\n"))
	if !ok || server != "kern/dev" {
		t.Fatalf("valid response = (%q, %v), want (kern/dev, true)", server, ok)
	}
	// Result without serverInfo still parses (ok, no server string).
	server, ok = parseMCPInitializeResponse([]byte(`{"jsonrpc":"2.0","id":1,"result":{"capabilities":{}}}`))
	if !ok || server != "" {
		t.Fatalf("result-without-serverInfo = (%q, %v), want (\"\", true)", server, ok)
	}
	// An error response (no result) is not a pass.
	if _, ok := parseMCPInitializeResponse([]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"Method not found"}}`)); ok {
		t.Fatal("error response must not parse as a pass")
	}
	// A usage line or garbage is not a pass.
	for _, bad := range []string{"Usage of kern-mcp:\n", "not json\n", ""} {
		if _, ok := parseMCPInitializeResponse([]byte(bad)); ok {
			t.Fatalf("input %q must not parse as a pass", bad)
		}
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

// TestCheckGitExclude covers the doctor surface for index.Save's silent
// .git/info/exclude side effect: ok when the entry exists, warn when a git
// repo lacks it, ok for non-git roots.
func TestCheckGitExclude(t *testing.T) {
	// non-git root: ok
	f := checkGitExclude(t.TempDir())
	if f.Level != "ok" {
		t.Fatalf("non-git: level = %s, want ok", f.Level)
	}
	// git repo with the entry: ok
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git", "info"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "info", "exclude"), []byte(".kern/"), 0o644); err != nil {
		t.Fatal(err)
	}
	f = checkGitExclude(dir)
	if f.Level != "ok" {
		t.Fatalf("with entry: level = %s (%s), want ok", f.Level, f.Detail)
	}
	// git repo without the entry: warn
	dir2 := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir2, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	f = checkGitExclude(dir2)
	if f.Level != "warn" {
		t.Fatalf("without entry: level = %s (%s), want warn", f.Level, f.Detail)
	}
}

// TestWiringFindingsDedupesGlobalPlugin pins F13: setup.Check reports the
// opencode plugin (global) once per global plugin location; doctor must
// merge those into a single finding instead of printing the same line twice.
func TestWiringFindingsDedupesGlobalPlugin(t *testing.T) {
	sts := []setup.Status{
		{Agent: "opencode plugin (global)", Installed: true, Path: "/home/u/.config/opencode/plugins/kern.ts", Note: "kern entry present"},
		{Agent: "opencode plugin (global)", Installed: true, Path: "/home/u/.opencode/plugins/kern.ts", Note: "kern entry present"},
		{Agent: "AGENTS.md rules", Installed: true, Path: "/repo/AGENTS.md", Note: "kern entry present"},
	}
	findings := wiringFindings(sts, nil)
	count := 0
	for _, f := range findings {
		if f.Check == "opencode plugin (global)" {
			count++
			if f.Level != "ok" {
				t.Fatalf("merged global plugin level = %s, want ok", f.Level)
			}
			if !strings.Contains(f.Detail, ".config/opencode") || !strings.Contains(f.Detail, ".opencode") {
				t.Fatalf("merged detail should name both global paths, got %q", f.Detail)
			}
		}
	}
	if count != 1 {
		t.Fatalf("opencode plugin (global) reported %d times, want exactly 1 (deduped)", count)
	}
}

// TestWiringFindingsPairNotesWithPaths pins the conflicting-notes render:
// when one label (e.g. "global rules") mixes present and missing slots,
// each note must name ITS OWN paths. The old render joined the notes and
// dumped every path at the end, attributing "not present" to the first
// listed path — a file that is actually present (self-contradiction).
func TestWiringFindingsPairNotesWithPaths(t *testing.T) {
	sts := []setup.Status{
		{Agent: "global rules", Installed: true, Path: "/home/u/.claude/CLAUDE.md", Note: "kern entry present"},
		{Agent: "global rules", Installed: true, Path: "/home/u/.codex/AGENTS.md", Note: "kern entry present"},
		{Agent: "global rules", Installed: false, Path: "/home/u/.config/opencode/AGENTS.md", Note: "not present"},
	}
	findings := wiringFindings(sts, nil)
	var f *Finding
	for i := range findings {
		if findings[i].Check == "global rules" {
			f = &findings[i]
		}
	}
	if f == nil {
		t.Fatal("no merged global rules finding")
	}
	if f.Level != "warn" {
		t.Fatalf("mixed-present group level = %s, want warn", f.Level)
	}
	d := f.Detail
	if !strings.Contains(d, "kern entry present — /home/u/.claude/CLAUDE.md") {
		t.Errorf("present note must name its own paths, got %q", d)
	}
	if !strings.Contains(d, "not present — /home/u/.config/opencode/AGENTS.md") {
		t.Errorf("missing note must name exactly its own path, got %q", d)
	}
	if strings.Contains(d, "not present — /home/u/.claude/CLAUDE.md") {
		t.Errorf("missing note must not be attributed to a present path: %q", d)
	}
}

// TestWiringFindingsRegisteredOnlyWhenDetected pins F13: a "registered"
// claim must not be reported as [ok] when the agent itself is not detected.
func TestWiringFindingsRegisteredOnlyWhenDetected(t *testing.T) {
	// claude config says "registered" but claude is NOT in the detected set.
	sts := []setup.Status{
		{Agent: "claude", Installed: true, Path: "/usr/local/bin/claude", Note: "kern MCP registered (project or user scope)"},
	}
	findings := wiringFindings(sts, nil)
	for _, f := range findings {
		if f.Check == "claude" {
			if f.Level != "warn" {
				t.Fatalf("undetected-agent registration level = %s, want warn", f.Level)
			}
			if !strings.Contains(f.Detail, "not detected") {
				t.Fatalf("detail should say the agent is not detected, got %q", f.Detail)
			}
		}
	}

	// Control: with "claude (detected)" present, the registration is [ok].
	sts = []setup.Status{
		{Agent: "claude", Installed: true, Path: "/usr/local/bin/claude", Note: "kern MCP registered (project or user scope)"},
		{Agent: "claude (detected)", Installed: true, Path: "/repo/CLAUDE.md", Note: "kern-first policy present"},
	}
	for _, f := range wiringFindings(sts, nil) {
		if f.Check == "claude" && f.Level != "ok" {
			t.Fatalf("detected-agent registration level = %s, want ok", f.Level)
		}
	}
}

// TestWiringFindingsRewordsDetectedNotPresent pins F13: "[warn] claude
// (detected) not present" next to a registered line reads as "claude not
// installed"; reword it to name what is actually missing (the kern-first
// policy in the instruction file).
func TestWiringFindingsRewordsDetectedNotPresent(t *testing.T) {
	sts := []setup.Status{
		{Agent: "claude", Installed: true, Path: "/usr/local/bin/claude", Note: "kern MCP registered (project or user scope)"},
		{Agent: "claude (detected)", Installed: false, Path: "/repo/CLAUDE.md", Note: "not present"},
	}
	for _, f := range wiringFindings(sts, nil) {
		if f.Check == "claude (detected)" {
			if strings.Contains(f.Detail, "not present") && !strings.Contains(f.Detail, "policy not present") {
				t.Fatalf("reworded detail still reads as 'not present': %q", f.Detail)
			}
			if !strings.Contains(f.Detail, "kern-first policy") {
				t.Fatalf("detail should name the missing policy, got %q", f.Detail)
			}
		}
	}
}

// TestCheckIndexReportsStorePath pins F5a: doctor's index check must name
// the RESOLVED store path the root serves, not just the counts.
func TestCheckIndexReportsStorePath(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	writeGoFile(t, root, "a.go")
	ix, err := index.Build(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := ix.Save(); err != nil {
		t.Fatal(err)
	}
	f := checkIndex(root)
	if f.Level != "ok" {
		t.Fatalf("checkIndex level = %s: %+v", f.Level, f)
	}
	if !strings.Contains(f.Detail, index.StorePath(root)) {
		t.Fatalf("checkIndex detail missing resolved store path %q: %q", index.StorePath(root), f.Detail)
	}
}

// TestCheckIndexShadowNested pins F5c: serving a nested .kern while a parent
// directory also holds an index must warn.
func TestCheckIndexShadowNested(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	parent := t.TempDir()
	writeGoFile(t, parent, "a.go")
	ix, err := index.Build(parent)
	if err != nil {
		t.Fatal(err)
	}
	if err := ix.Save(); err != nil {
		t.Fatal(err)
	}
	// Parent root: its own index, no parent-of-parent index → ok.
	if f := checkIndexShadow(parent); f.Level != "ok" {
		t.Fatalf("parent root reported %s: %+v", f.Level, f)
	}
	// A nested subdir with its own index → warn (shadowing a parent index).
	sub := filepath.Join(parent, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	writeGoFile(t, sub, "b.go")
	six, err := index.Build(sub)
	if err != nil {
		t.Fatal(err)
	}
	if err := six.Save(); err != nil {
		t.Fatal(err)
	}
	f := checkIndexShadow(sub)
	if f.Level != "warn" {
		t.Fatalf("nested root reported %s, want warn: %+v", f.Level, f)
	}
	if !strings.Contains(f.Detail, parent) {
		t.Fatalf("shadow warning should name the parent dir %q: %q", parent, f.Detail)
	}
	// A dir with no nested index at all → ok.
	empty := t.TempDir()
	if f := checkIndexShadow(empty); f.Level != "ok" {
		t.Fatalf("index-less root reported %s: %+v", f.Level, f)
	}
}

// TestCheckIndexShadowParentStates pins the A2 classification: the shadow
// warning must name the parent's actual state and only advise serving the
// parent when it is fresh — a stale or unreadable parent is itself the
// problem, and the remediation must say so instead of telling the operator
// to serve a bad index.
func TestCheckIndexShadowParentStates(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	buildAndSave := func(root string) {
		ix, err := index.Build(root)
		if err != nil {
			t.Fatal(err)
		}
		if err := ix.Save(); err != nil {
			t.Fatal(err)
		}
	}
	parent := t.TempDir()
	// A file with a real symbol, so the parent index is fresh AND non-empty
	// (writeGoFile's bare "package p" yields zero symbols — that is the
	// empty-parent fixture below).
	if err := os.WriteFile(filepath.Join(parent, "a.go"), []byte("package p\n\nfunc A() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The child tree must exist BEFORE the parent build: any file added to
	// the parent's tree afterwards makes the parent index stale (that is
	// the stale fixture's mechanism, not a bug).
	sub := filepath.Join(parent, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	writeGoFile(t, sub, "b.go")
	buildAndSave(parent)
	buildAndSave(sub)

	// Fresh parent: advise serving the parent root.
	f := checkIndexShadow(sub)
	if f.Level != "warn" || !strings.Contains(f.Detail, "fresh") || !strings.Contains(f.Detail, "parent root") {
		t.Fatalf("fresh parent: got %+v", f)
	}

	// Stale parent: a source file added after the parent build makes the
	// parent index stale — this root is fine, the parent needs the fix.
	writeGoFile(t, parent, "c.go")
	f = checkIndexShadow(sub)
	if !strings.Contains(f.Detail, "stale") || !strings.Contains(f.Detail, "this root is fine") {
		t.Fatalf("stale parent: got %+v", f)
	}

	// Unreadable parent: corrupt every store the load chain tries
	// (bin snapshot → SQLite → JSON; SQLite is primary, so index.json
	// alone is NOT enough — the sqlite store would still load).
	for _, name := range []string{"index.json.snap", "index.sqlite", "index.json"} {
		if err := os.WriteFile(filepath.Join(parent, ".kern", name), []byte("not json"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	f = checkIndexShadow(sub)
	if !strings.Contains(f.Detail, "unreadable") || !strings.Contains(f.Detail, "this root is fine") {
		t.Fatalf("unreadable parent: got %+v", f)
	}

	// Empty parent: a valid parent store with no symbols (bare "package p")
	// is still a non-servable parent — same remediation as unreadable.
	emptyParent := t.TempDir()
	writeGoFile(t, emptyParent, "a.go")
	buildAndSave(emptyParent)
	emptySub := filepath.Join(emptyParent, "sub")
	if err := os.MkdirAll(emptySub, 0o755); err != nil {
		t.Fatal(err)
	}
	writeGoFile(t, emptySub, "b.go")
	buildAndSave(emptySub)
	f = checkIndexShadow(emptySub)
	if !strings.Contains(f.Detail, "empty") || !strings.Contains(f.Detail, "this root is fine") {
		t.Fatalf("empty parent: got %+v", f)
	}
}

// TestParentIndexDir walks up to the nearest ancestor holding an index.
func TestParentIndexDir(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	parent := t.TempDir()
	sub := filepath.Join(parent, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := ParentIndexDir(sub); got != "" {
		t.Fatalf("no ancestor index yet, but ParentIndexDir = %q", got)
	}
	ix, err := index.Build(parent)
	if err != nil {
		t.Fatal(err)
	}
	if err := ix.Save(); err != nil {
		t.Fatal(err)
	}
	if got := ParentIndexDir(sub); filepath.Clean(got) != filepath.Clean(parent) {
		t.Fatalf("ParentIndexDir = %q, want %q", got, parent)
	}
	if got := ParentIndexDir(parent); got != "" {
		t.Fatalf("ParentIndexDir of the indexed root itself = %q, want \"\"", got)
	}
}

// TestWiringFindingsNoCursorWarnAfterFreshSetup pins F10: after a fresh
// successful `kern setup`, doctor must not warn about a missing cursor
// policy. The live repro: bare setup wrote .cursor/rules/ (making cursor
// "detected" ever after) but — via a stale pre-run detection snapshot —
// never the kern-first policy file doctor probes, so the warning was
// permanent. Setup now writes the policy (to .cursor/rules/kern.mdc) in
// the same run that creates the marker, and accepts legacy
// .cursor/instructions/kern.mdc installs.
func TestWiringFindingsNoCursorWarnAfterFreshSetup(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("PATH", "/nonexistent")
	root := t.TempDir()

	setup.Wire(root, nil, false, false)
	for _, f := range wiringFindings(setup.Check(root), setup.DetectAgents(root)) {
		if f.Level != "warn" {
			continue
		}
		// Scope to the F10 policy checks (the "(detected)" instruction
		// statuses and the cursor rule). Home-scoped adapter/hook checks
		// legitimately warn without --global — not this defect.
		if strings.Contains(f.Check, "cursor (detected)") ||
			strings.Contains(f.Check, "copilot (detected)") ||
			f.Check == "cursor rule" {
			t.Errorf("%s: unexpected warn after fresh setup: %s", f.Check, f.Detail)
		}
	}
}

// TestCheckAuditDropped pins the `kern doctor` surface of the dropped-audit
// contract (governance.AppendDroppedAuditMarker + AuditLog.persist): absent
// or empty sidecar = ok; non-empty = warn with the count + last timestamp.
func TestCheckAuditDropped(t *testing.T) {
	t.Run("absent sidecar is ok", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		if f := checkAuditDropped(); f.Level != "ok" {
			t.Fatalf("level = %q, want ok (no sidecar): %s", f.Level, f.Detail)
		}
	})
	t.Run("empty sidecar is ok", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		dir := filepath.Join(home, ".kern", "audit")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "dropped.jsonl"), []byte("\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if f := checkAuditDropped(); f.Level != "ok" {
			t.Fatalf("level = %q, want ok (empty sidecar): %s", f.Level, f.Detail)
		}
	})
	t.Run("non-empty sidecar warns with count and last timestamp", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		dir := filepath.Join(home, ".kern", "audit")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		body := `{"ts":"2026-10-08T12:00:00Z","id":"audit-1","action":"tool_call","resource":"kern_search","cause":"disk full"}` + "\n" +
			`{"ts":"2026-10-08T12:05:00Z","id":"audit-2","action":"tool_call","resource":"kern_explore","cause":"disk full"}` + "\n"
		if err := os.WriteFile(filepath.Join(dir, "dropped.jsonl"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		f := checkAuditDropped()
		if f.Level != "warn" {
			t.Fatalf("level = %q, want warn (non-empty sidecar): %s", f.Level, f.Detail)
		}
		if !strings.Contains(f.Detail, "2 audit entries") {
			t.Fatalf("detail = %q, want the count of dropped entries", f.Detail)
		}
		if !strings.Contains(f.Detail, "12:05:00") {
			t.Fatalf("detail = %q, want the last drop's timestamp", f.Detail)
		}
	})
}

// TestWiringFindingsAdvisoryPrefixesAreWarnings pins the advisory-prefix
// mapping: setup's checkWrapperFreshness emits Installed statuses whose
// notes start with WARN:/STALE: (e.g. an installed kern older than the
// project-local binary). Doctor must render those as warnings -- "[ok]
// kern STALE: 85d7fa0 != 66a92d1" reads as a contradiction.
func TestWiringFindingsAdvisoryPrefixesAreWarnings(t *testing.T) {
	sts := []setup.Status{
		{Agent: "kern", Installed: true, Path: "/home/u/.local/bin/kern", Note: "STALE: kern v0.9.9 != kern v0.9.10 - refresh from the same release (else the pre-commit hook may run an old governance gate)"},
		{Agent: "kern-mcp", Installed: true, Path: "/home/u/.local/bin/kern-mcp", Note: "WARN: cannot read version from /home/u/.local/bin/kern-mcp; refresh it from the same release as kern"},
		{Agent: "AGENTS.md rules", Installed: true, Path: "/repo/AGENTS.md", Note: "kern entry present"},
	}
	levels := map[string]string{}
	for _, f := range wiringFindings(sts, nil) {
		levels[f.Check] = f.Level
	}
	if levels["kern"] != "warn" {
		t.Fatalf("STALE note must render warn, got %s", levels["kern"])
	}
	if levels["kern-mcp"] != "warn" {
		t.Fatalf("WARN note must render warn, got %s", levels["kern-mcp"])
	}
	if levels["AGENTS.md rules"] != "ok" {
		t.Fatalf("normal note must stay ok, got %s", levels["AGENTS.md rules"])
	}
}
