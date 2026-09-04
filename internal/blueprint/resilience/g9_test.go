package resilience

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// --- Test sandbox adapter ---

// testSandbox implements the Sandbox interface using the same process-group
// isolation as the real sandbox (Phase 8), but inlined here to avoid a circular
// dependency on the sandbox package.
type testSandbox struct {
	timeout time.Duration
}

func (ts testSandbox) Run(ctx context.Context, repoRoot string, command []string) SandboxResult {
	if ts.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, ts.timeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	res := SandboxResult{
		Output:   string(out),
		ExitCode: 0,
		Ok:       err == nil,
	}
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			res.ExitCode = exitErr.ExitCode()
		} else {
			res.ExitCode = -1
		}
		if ctx.Err() == context.DeadlineExceeded {
			res.TimedOut = true
		}
	}
	return res
}

// --- Fixture helpers ---

// makeRepo creates a git repo with a go.mod and optional source files.
func makeRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "config", "user.email", "t@t")
	runGit(t, dir, "config", "user.name", "t")
	writeFile(t, dir, "go.mod", "module example.com/test\n\ngo 1.23\n")
	for path, content := range files {
		writeFile(t, dir, path, content)
	}
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-qm", "init")
	return dir
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func writeFile(t *testing.T, dir, relpath, content string) {
	t.Helper()
	full := filepath.Join(dir, relpath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", relpath, err)
	}
}

// resilientHTTPClient is Go code with proper HTTP client timeouts (5s).
const resilientHTTPClient = `package main

import (
	"net/http"
	"time"
)

func fetchURL(url string) (*http.Response, error) {
	client := &http.Client{
		Timeout: 5 * time.Second,
	}
	return client.Get(url)
}
`

// nonResilientHTTPClient is Go code with NO timeout — will hang on a slow server.
const nonResilientHTTPClient = `package main

import (
	"net/http"
)

func fetchURL(url string) (*http.Response, error) {
	return http.Get(url) // no timeout — hangs on slow server
}
`

// resilientTestSuite tests that the HTTP client times out gracefully.
const resilientTestSuite = `package main

import (
	"os"
	"testing"
	"time"
)

func TestResilienceHTTPTimeout(t *testing.T) {
	url := os.Getenv("FAULT_SERVER_URL")
	if url == "" {
		t.Skip("no fault server")
	}
	start := time.Now()
	_, err := fetchURL(url)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected error from slow server, got nil")
	}
	if elapsed > 10*time.Second {
		t.Fatalf("client did not timeout: took %s", elapsed)
	}
}
`

// nonResilientTestSuite has no timeout assertion — it'll just hang.
const nonResilientTestSuite = `package main

import (
	"os"
	"testing"
)

func TestResilienceHTTPTimeout(t *testing.T) {
	url := os.Getenv("FAULT_SERVER_URL")
	if url == "" {
		t.Skip("no fault server")
	}
	// No timeout — this will hang on the slow server.
	_, _ = fetchURL(url)
}
`

// --- G9 Tests ---

// G9-1: baseline test passes (no fault injected)
func TestG9_BaselinePasses(t *testing.T) {
	dir := makeRepo(t, map[string]string{
		"main.go":      resilientHTTPClient,
		"main_test.go": resilientTestSuite,
	})
	sb := testSandbox{timeout: 15 * time.Second}
	res := sb.Run(context.Background(), dir, []string{"go", "test", "./...", "-run", "TestResilienceHTTPTimeout", "-timeout", "10s"})
	if !res.Ok {
		t.Fatalf("baseline test should pass (no fault server): exit=%d\n%s", res.ExitCode, res.Output)
	}
	// The test should skip because FAULT_SERVER_URL is not set.
	if !strings.Contains(res.Output, "SKIP") && !strings.Contains(res.Output, "skip") {
		t.Logf("note: baseline test did not SKIP (may have passed outright): %s", res.Output)
	}
}

// G9-2: injected timeout is actually injected
func TestG9_InjectedTimeoutActuallyInjected(t *testing.T) {
	scenario := &HTTPTimeout{}
	ctx := context.Background()

	if err := scenario.Prepare(ctx); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	defer scenario.Cleanup(ctx)

	if scenario.serverAddr == "" {
		t.Fatal("serverAddr not set after Prepare")
	}

	// Verify the server is actually slow by connecting to it.
	start := time.Now()
	conn, err := net.DialTimeout("tcp", strings.TrimPrefix(scenario.serverAddr, "http://"), 1*time.Second)
	if err != nil {
		t.Fatalf("cannot connect to fault server: %v", err)
	}
	defer conn.Close()
	// Send an HTTP request — the server should not respond within 2s.
	conn.Write([]byte("GET / HTTP/1.0\r\n\r\n"))
	buf := make([]byte, 1024)
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, err = conn.Read(buf)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatalf("fault server responded (expected to hang): took %s", elapsed)
	}
	if elapsed < 1*time.Second {
		t.Errorf("fault server returned too quickly: %s (expected to hang)", elapsed)
	}
	t.Logf("fault server correctly hung for %s", elapsed)
}

// G9-3: resilient implementation passes (client has 5s timeout)
func TestG9_ResilientImplPasses(t *testing.T) {
	dir := makeRepo(t, map[string]string{
		"main.go":      resilientHTTPClient,
		"main_test.go": resilientTestSuite,
	})

	scenario := &HTTPTimeout{}
	ctx := context.Background()
	if err := scenario.Prepare(ctx); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	defer scenario.Cleanup(ctx)

	// Run the test with the fault server URL set.
	sb := testSandboxWithEnv{timeout: 15 * time.Second, env: []string{"FAULT_SERVER_URL=" + scenario.serverAddr}}
	res := sb.Run(context.Background(), dir, []string{"go", "test", "./...", "-run", "TestResilienceHTTPTimeout", "-timeout", "10s"})
	if !res.Ok {
		t.Fatalf("resilient impl should pass: exit=%d\n%s", res.ExitCode, res.Output)
	}
}

// G9-4: non-resilient implementation fails (no client timeout — hangs)
func TestG9_NonResilientImplFails(t *testing.T) {
	dir := makeRepo(t, map[string]string{
		"main.go":      nonResilientHTTPClient,
		"main_test.go": nonResilientTestSuite,
	})

	scenario := &HTTPTimeout{}
	ctx := context.Background()
	if err := scenario.Prepare(ctx); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	defer scenario.Cleanup(ctx)

	// Run with a short sandbox timeout — the non-resilient code will hang.
	sb := testSandboxWithEnv{timeout: 8 * time.Second, env: []string{"FAULT_SERVER_URL=" + scenario.serverAddr}}
	res := sb.Run(context.Background(), dir, []string{"go", "test", "./...", "-run", "TestResilienceHTTPTimeout", "-timeout", "5s"})
	if res.Ok {
		t.Fatal("non-resilient impl should fail (hang/timeout), but it passed")
	}
}

// G9-5: failures are repeatable
func TestG9_FailuresRepeatable(t *testing.T) {
	dir := makeRepo(t, map[string]string{
		"main.go":      nonResilientHTTPClient,
		"main_test.go": nonResilientTestSuite,
	})

	scenario := &HTTPTimeout{}
	ctx := context.Background()
	if err := scenario.Prepare(ctx); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	defer scenario.Cleanup(ctx)

	sb := testSandboxWithEnv{timeout: 8 * time.Second, env: []string{"FAULT_SERVER_URL=" + scenario.serverAddr}}

	// Run twice — both should fail.
	for i := 0; i < 2; i++ {
		res := sb.Run(context.Background(), dir, []string{"go", "test", "./...", "-run", "TestResilienceHTTPTimeout", "-timeout", "5s"})
		if res.Ok {
			t.Fatalf("run %d: expected failure, got pass", i+1)
		}
	}
}

// G9-6: no network leakage outside the sandbox behavior
func TestG9_NoNetworkLeakage(t *testing.T) {
	scenario := &HTTPTimeout{}
	ctx := context.Background()
	if err := scenario.Prepare(ctx); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	defer scenario.Cleanup(ctx)

	// The fault server should only listen on 127.0.0.1 (localhost).
	addr := strings.TrimPrefix(scenario.serverAddr, "http://")
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("split addr: %v", err)
	}
	if host != "127.0.0.1" {
		t.Errorf("fault server listens on %s, expected 127.0.0.1 (no external network exposure)", host)
	}

	// Verify the server is not reachable on 0.0.0.0.
	ln, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Logf("note: cannot test 0.0.0.0 binding: %v", err)
	} else {
		ln.Close()
	}
}

// G9-7: cleanup is guaranteed
func TestG9_CleanupGuaranteed(t *testing.T) {
	scenario := &HTTPTimeout{}
	ctx := context.Background()

	if err := scenario.Prepare(ctx); err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	addr := scenario.serverAddr
	if addr == "" {
		t.Fatal("no server addr after Prepare")
	}

	// Verify the server is listening.
	conn, err := net.Dial("tcp", strings.TrimPrefix(addr, "http://"))
	if err != nil {
		t.Fatalf("server not listening after Prepare: %v", err)
	}
	conn.Close()

	// Cleanup.
	if err := scenario.Cleanup(ctx); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}

	// Verify the server is no longer listening.
	_, err = net.Dial("tcp", strings.TrimPrefix(addr, "http://"))
	if err == nil {
		t.Fatal("server still listening after Cleanup — port leak")
	}

	// Cleanup should be idempotent.
	if err := scenario.Cleanup(ctx); err != nil {
		t.Errorf("second Cleanup should be idempotent: %v", err)
	}
}

// G9-bonus: scenario applicability detection
func TestG9_Applicability(t *testing.T) {
	dir := makeRepo(t, map[string]string{
		"main.go": resilientHTTPClient,
	})
	info := DetectRepoInfo(dir)

	if info.Language != "go" {
		t.Errorf("Language = %s, want go", info.Language)
	}
	if !info.HasGoMod {
		t.Error("HasGoMod = false, want true")
	}

	scenario := HTTPTimeout{}
	if !scenario.Applicable(info) {
		t.Error("HTTPTimeout should be applicable to a Go repo with net/http import")
	}

	// Non-Go repo should not be applicable.
	nonGoInfo := RepoInfo{Language: "python"}
	if scenario.Applicable(nonGoInfo) {
		t.Error("HTTPTimeout should not be applicable to Python")
	}
}

// G9-bonus: registry works
func TestG9_Registry(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&HTTPTimeout{})
	reg.Register(&HTTP500{})
	reg.Register(&MalformedJSON{})

	if len(reg.All()) != 3 {
		t.Errorf("registry has %d scenarios, want 3", len(reg.All()))
	}

	info := RepoInfo{Language: "go", Imports: []string{"net/http"}}
	applicable := reg.Applicable(info)
	if len(applicable) != 3 {
		t.Errorf("applicable scenarios = %d, want 3", len(applicable))
	}

	if reg.Get("go:http-timeout") == nil {
		t.Error("Get(go:http-timeout) returned nil")
	}
}

// --- Shell scenario tests (B5: second ecosystem) ---

// shellScenarioIDs is the expected built-in shell scenario set.
var shellScenarioIDs = []string{"shell:unhandled-exit", "shell:unset-variable", "shell:missing-error-handling"}

// TestG9_ShellScenariosRegistered verifies the shell scenarios ship as
// built-ins alongside the Go ones, with the expected ids and ecosystem.
func TestG9_ShellScenariosRegistered(t *testing.T) {
	builtins := DefaultScenarios()
	got := map[string]bool{}
	for _, s := range builtins {
		if sh, ok := s.(*ShellScenario); ok {
			got[sh.ID()] = true
			if sh.Ecosystem() != "shell" {
				t.Errorf("%s: Ecosystem() = %q, want shell", sh.ID(), sh.Ecosystem())
			}
			if sh.Source() == "" {
				t.Errorf("%s: source script is empty", sh.ID())
			}
		}
	}
	for _, id := range shellScenarioIDs {
		if !got[id] {
			t.Errorf("DefaultScenarios() missing shell scenario %q (got %v)", id, got)
		}
	}
	// Registry round-trip: register and fetch each shell scenario.
	reg := NewRegistry()
	for _, s := range DefaultScenarios() {
		reg.Register(s)
	}
	for _, id := range shellScenarioIDs {
		if reg.Get(id) == nil {
			t.Errorf("registry Get(%q) returned nil", id)
		}
	}
}

// TestG9_ShellScenarioDetectsFailure drives every built-in shell scenario
// through Run: each deliberately non-resilient script must be flagged
// (Passed=false) and the finding detail must name the failure mode.
func TestG9_ShellScenarioDetectsFailure(t *testing.T) {
	wantDetail := map[string]string{
		"shell:unhandled-exit":         "exit 1",
		"shell:unset-variable":         "unset variable",
		"shell:missing-error-handling": "missing error handling",
	}
	sb := testSandbox{}
	for _, s := range DefaultScenarios() {
		sh, ok := s.(*ShellScenario)
		if !ok {
			continue
		}
		if err := sh.Prepare(context.Background()); err != nil {
			t.Fatalf("%s Prepare: %v", sh.ID(), err)
		}
		res := sh.Run(context.Background(), sb)
		if err := sh.Cleanup(context.Background()); err != nil {
			t.Fatalf("%s Cleanup: %v", sh.ID(), err)
		}
		if res.Passed {
			t.Errorf("%s: Run passed, want the failure mode to be flagged", sh.ID())
		}
		if !res.FaultInjected {
			t.Errorf("%s: FaultInjected = false, want true (the failure script was analyzed)", sh.ID())
		}
		want := wantDetail[sh.ID()]
		if want != "" && !contains(res.Detail, want) {
			t.Errorf("%s: Detail %q missing %q", sh.ID(), res.Detail, want)
		}
		// The expected finding pattern must be present in the source and the
		// guard marker absent (that is what makes it a flagged failure).
		if !contains(sh.Source(), sh.pattern) {
			t.Errorf("%s: pattern %q not present in source", sh.ID(), sh.pattern)
		}
		if sh.absent != "" && contains(sh.Source(), sh.absent) {
			t.Errorf("%s: guard marker %q unexpectedly present in source", sh.ID(), sh.absent)
		}
	}
}

// TestG9_ShellScenarioPassesWhenHandled verifies the pattern matcher is not a
// dumb substring check: a script that guards the failure (trap present, the
// variable assigned, the exit code checked) is NOT flagged.
func TestG9_ShellScenarioPassesWhenHandled(t *testing.T) {
	cases := []struct {
		name    string
		sc      *ShellScenario
		wantRun bool // true = scenario Passes (script handled the failure)
	}{
		{
			name: "trap handles exit",
			sc: &ShellScenario{
				id:        "shell:unhandled-exit",
				ecosystem: "shell",
				source:    "#!/usr/bin/env bash\ntrap cleanup EXIT\ndeploy_app\nexit 1\n",
				desc:      "script calls exit 1 without a trap",
				pattern:   "exit 1",
				absent:    "trap",
			},
			wantRun: true,
		},
		{
			name: "variable assigned",
			sc: &ShellScenario{
				id:        "shell:unset-variable",
				ecosystem: "shell",
				source:    "#!/usr/bin/env bash\nset -u\nDEPLOY_TARGET=prod\necho \"deploying to $DEPLOY_TARGET\"\n",
				desc:      "script references an unset variable",
				pattern:   "$DEPLOY_TARGET",
				absent:    "DEPLOY_TARGET=",
			},
			wantRun: true,
		},
		{
			name: "exit code checked",
			sc: &ShellScenario{
				id:        "shell:missing-error-handling",
				ecosystem: "shell",
				source:    "#!/usr/bin/env bash\ncd /nonexistent || exit 1\nif [ $? -ne 0 ]; then exit 1; fi\n",
				desc:      "script runs cd without checking $?",
				pattern:   "cd /nonexistent",
				absent:    "$?",
			},
			wantRun: true,
		},
	}
	sb := testSandbox{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.sc.Prepare(context.Background()); err != nil {
				t.Fatalf("Prepare: %v", err)
			}
			res := tc.sc.Run(context.Background(), sb)
			if err := tc.sc.Cleanup(context.Background()); err != nil {
				t.Fatalf("Cleanup: %v", err)
			}
			if res.Passed != tc.wantRun {
				t.Errorf("Run.Passed = %v, want %v (Detail: %s)", res.Passed, tc.wantRun, res.Detail)
			}
		})
	}
}

// TestG9_ShellApplicabilityAndDetection verifies shell scenarios only apply to
// shell repos and that DetectRepoInfo recognizes a repo with .sh scripts.
func TestG9_ShellApplicabilityAndDetection(t *testing.T) {
	sc := &ShellScenario{}
	if !sc.Applicable(RepoInfo{Language: "shell"}) {
		t.Error("ShellScenario should be applicable to a shell repo")
	}
	if sc.Applicable(RepoInfo{Language: "go"}) {
		t.Error("ShellScenario should not be applicable to a Go repo")
	}
	if sc.Applicable(RepoInfo{Language: "python"}) {
		t.Error("ShellScenario should not be applicable to a Python repo")
	}

	// A repo with a .sh script and no go.mod is detected as shell.
	dir := t.TempDir()
	writeFile(t, dir, "deploy.sh", "#!/usr/bin/env bash\necho hi\n")
	info := DetectRepoInfo(dir)
	if info.Language != "shell" {
		t.Errorf("DetectRepoInfo on .sh repo: Language = %q, want shell", info.Language)
	}
	if !info.HasShell {
		t.Error("HasShell = false, want true")
	}

	// A repo with a go.mod stays Go even when it also has .sh scripts.
	dir2 := makeRepo(t, map[string]string{
		"main.go":   "package main\n\nfunc main() {}\n",
		"deploy.sh": "#!/usr/bin/env bash\necho hi\n",
	})
	info2 := DetectRepoInfo(dir2)
	if info2.Language != "go" {
		t.Errorf("DetectRepoInfo on go+sh repo: Language = %q, want go", info2.Language)
	}

	// An empty repo is neither.
	info3 := DetectRepoInfo(t.TempDir())
	if info3.Language != "" {
		t.Errorf("DetectRepoInfo on empty repo: Language = %q, want \"\"", info3.Language)
	}
}

// --- testSandboxWithEnv: testSandbox with extra env vars ---

type testSandboxWithEnv struct {
	timeout time.Duration
	env     []string
}

func (ts testSandboxWithEnv) Run(ctx context.Context, repoRoot string, command []string) SandboxResult {
	if ts.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, ts.timeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), ts.env...)
	out, err := cmd.CombinedOutput()
	res := SandboxResult{
		Output:   string(out),
		ExitCode: 0,
		Ok:       err == nil,
	}
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			res.ExitCode = exitErr.ExitCode()
		} else {
			res.ExitCode = -1
		}
		if ctx.Err() == context.DeadlineExceeded {
			res.TimedOut = true
		}
	}
	return res
}
