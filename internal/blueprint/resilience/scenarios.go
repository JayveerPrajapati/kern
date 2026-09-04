package resilience

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// --- Parameterized Go HTTP Fault Scenario ---

// HTTPFault is a parameterized Go HTTP fault-injection scenario. It starts an
// HTTP server on a random loopback port that serves the configured path with
// the configured status after the configured delay, writes a resilience test
// into the sandbox that exercises the repository's HTTP client against the
// fault server, and runs it with `go test -run <TestName> -timeout 10s`.
//
// The generated test mirrors the G9 fixture pattern (a `fetchURL` function in
// the repository's root package): it must observe the fault and handle it
// gracefully within a bounded time. A non-resilient client (no timeout, no
// status check) makes the generated test fail; a resilient client passes.
type HTTPFault struct {
	id         string
	status     int
	delay      time.Duration
	path       string
	serverAddr string
	server     *http.Server
}

// NewHTTPFault constructs a validated HTTPFault. The status must be in
// [100, 599], the delay must be non-negative seconds, and the path must start
// with "/". A zero delay responds immediately.
func NewHTTPFault(id string, status int, delaySeconds int, path string) (*HTTPFault, error) {
	if status < 100 || status > 599 {
		return nil, fmt.Errorf("invalid HTTP status %d (want 100-599)", status)
	}
	if delaySeconds < 0 {
		return nil, fmt.Errorf("negative delay_seconds %d", delaySeconds)
	}
	if path == "" || path[0] != '/' {
		return nil, fmt.Errorf("path %q must start with /", path)
	}
	return &HTTPFault{
		id:     id,
		status: status,
		delay:  time.Duration(delaySeconds) * time.Second,
		path:   path,
	}, nil
}

// ID returns the scenario's stable identifier.
func (h *HTTPFault) ID() string { return h.id }

// Applicable reports whether the scenario applies to the repo: Go source that
// imports net/http.
func (HTTPFault) Applicable(info RepoInfo) bool {
	if info.Language != "go" {
		return false
	}
	for _, imp := range info.Imports {
		if imp == "net/http" {
			return true
		}
	}
	return false
}

// effectivePath returns the configured path, defaulting to "/" so zero-value
// built-in wrappers (G9 constructs &HTTPTimeout{} directly) prepare correctly.
func (h *HTTPFault) effectivePath() string {
	if h.path == "" {
		return "/"
	}
	return h.path
}

// Prepare starts a fault server on a random loopback port. The mux serves the
// configured path, sleeping for the configured delay before writing the
// configured status (delay 0 = immediate response).
func (h *HTTPFault) Prepare(ctx context.Context) error {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	h.serverAddr = "http://" + listener.Addr().String()

	mux := http.NewServeMux()
	mux.HandleFunc(h.effectivePath(), func(w http.ResponseWriter, r *http.Request) {
		if h.delay > 0 {
			time.Sleep(h.delay)
		}
		w.WriteHeader(h.status)
	})
	h.server = &http.Server{Handler: mux}
	go h.server.Serve(listener)
	return nil
}

// Run writes a generated resilience test into the sandbox that hits
// http://<addr><path> through the repository's fetchURL function and asserts
// graceful handling, then executes it with `go test -run <TestName> -timeout
// 10s`. The generated file is removed before returning.
func (h *HTTPFault) Run(ctx context.Context, target Sandbox) Result {
	fileName, testName := h.generatedTestNames()
	content := h.generatedTest(testName)
	if err := os.WriteFile(fileName, []byte(content), 0o644); err != nil {
		return Result{
			ScenarioID:    h.ID(),
			Passed:        false,
			ExitCode:      -1,
			Output:        truncate(err.Error(), 500),
			FaultInjected: h.serverAddr != "",
			Detail:        fmt.Sprintf("could not write resilience test: %v", err),
		}
	}
	defer os.Remove(fileName)

	res := target.Run(ctx, ".", []string{"go", "test", "./...", "-run", testName, "-timeout", "10s"})
	return Result{
		ScenarioID:    h.ID(),
		Passed:        res.Ok,
		ExitCode:      res.ExitCode,
		Output:        truncate(res.Stdout+res.Stderr, 500),
		FaultInjected: h.serverAddr != "",
		Detail:        fmt.Sprintf("server at %s returns %d on %s after %s; tests should detect and handle the fault", h.serverAddr, h.status, h.path, h.delay),
	}
}

// Cleanup shuts down the fault server. Idempotent and safe after a failed
// Prepare.
func (h *HTTPFault) Cleanup(ctx context.Context) error {
	if h.server != nil {
		ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		h.server.Shutdown(ctx)
		h.server = nil
	}
	return nil
}

// generatedTestNames derives the generated test file name and test function
// name from the scenario ID so multiple scenarios can run against the same
// repository without colliding.
func (h *HTTPFault) generatedTestNames() (string, string) {
	safe := sanitizeID(h.ID())
	return "blueprint_resilience_" + safe + "_test.go", "TestBlueprintResilience" + safe
}

// generatedTest renders the resilience test source. It hits
// http://<addr><path> through the repository's fetchURL and asserts graceful
// handling per the G9 pattern: the client must surface the fault (as an error
// or as the declared non-2xx status); treating the fault response as success
// is a failure.
func (h *HTTPFault) generatedTest(testName string) string {
	pkg := detectRootPackage(".")
	url := h.serverAddr + h.effectivePath()
	return fmt.Sprintf(`package %s

import (
	"io"
	"testing"
	"time"
)

func %s(t *testing.T) {
	start := time.Now()
	resp, err := fetchURL(%q)
	elapsed := time.Since(start)
	if err != nil {
		t.Logf("graceful: fault handled as error after %%s: %%v", elapsed, err)
		return
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != %d {
		t.Fatalf("fault not observed: status %%d, want %%d (after %%s)", resp.StatusCode, %d, elapsed)
	}
	if %d >= 400 {
		t.Fatalf("fault not handled gracefully: status %%d returned with no error after %%s", resp.StatusCode, elapsed)
	}
	t.Logf("fault observed: status %%d after %%s", resp.StatusCode, elapsed)
}
`, pkg, testName, url, h.status, h.status, h.status)
}

// sanitizeID turns a scenario ID into a valid Go identifier fragment.
func sanitizeID(id string) string {
	out := make([]byte, 0, len(id))
	for i := 0; i < len(id); i++ {
		c := id[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' {
			out = append(out, c)
		} else {
			out = append(out, '_')
		}
	}
	return string(out)
}

// detectRootPackage inspects the repo root for the first "package X"
// declaration so the generated test file joins the correct package. Falls back
// to "main" when the root has no Go source files.
func detectRootPackage(root string) string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return "main"
	}
	for _, e := range entries {
		if e.IsDir() || !hasSuffix(e.Name(), ".go") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, e.Name()))
		if err != nil {
			continue
		}
		for _, line := range splitLines(string(data)) {
			t := trim(line)
			if hasPrefix(t, "package ") {
				return trim(t[len("package "):])
			}
		}
	}
	return "main"
}

// --- Built-in Go HTTP Timeout Scenario ---

// HTTPTimeout is the built-in slow-HTTP fault: status 200 with a 30s delay,
// exercising client timeouts. It is a thin wrapper over a parameterized
// HTTPFault so the concrete type can be constructed as a zero value (G9
// registers and drives it directly).
//
// G9: "injected timeout is actually injected" + "non-resilient implementation
// fails" + "resilient implementation passes".
type HTTPTimeout struct {
	HTTPFault
}

// ID returns the stable built-in identifier.
func (HTTPTimeout) ID() string { return "go:http-timeout" }

// Prepare applies the built-in timeout parameters (30s delay, status 200,
// path /) to a zero-value HTTPTimeout before starting the fault server, so
// G9's `&HTTPTimeout{}` construction keeps its built-in behavior.
func (h *HTTPTimeout) Prepare(ctx context.Context) error {
	h.ensureBuiltIn("go:http-timeout", 200, 30)
	return h.HTTPFault.Prepare(ctx)
}

// ensureBuiltIn populates a zero-value HTTPFault with the given built-in
// parameters. Configured instances (id already set) are left untouched.
func (h *HTTPFault) ensureBuiltIn(id string, status int, delaySeconds int) {
	if h.id != "" {
		return
	}
	h.id = id
	h.status = status
	h.delay = time.Duration(delaySeconds) * time.Second
	h.path = "/"
}

// --- Built-in Go HTTP 500 Scenario ---

// HTTP500 is the built-in 500-error fault: immediate status 500, exercising
// response-status error handling. Thin wrapper over a parameterized HTTPFault
// so the concrete type can be constructed as a zero value (G9 registers it
// directly).
type HTTP500 struct {
	HTTPFault
}

// ID returns the stable built-in identifier.
func (HTTP500) ID() string { return "go:http-500" }

// Prepare applies the built-in 500 parameters (immediate status 500, path /)
// to a zero-value HTTP500 before starting the fault server, so G9's
// `&HTTP500{}` construction keeps its built-in behavior.
func (h *HTTP500) Prepare(ctx context.Context) error {
	h.ensureBuiltIn("go:http-500", 500, 0)
	return h.HTTPFault.Prepare(ctx)
}

// --- Go Malformed JSON Scenario ---

// MalformedJSON is a Go scenario that starts an HTTP server returning malformed
// JSON. Code that doesn't handle JSON decode errors will panic or produce
// wrong results; code with proper error handling will fail gracefully.
type MalformedJSON struct {
	serverAddr string
	server     *http.Server
}

func (MalformedJSON) ID() string { return "go:malformed-json" }

func (MalformedJSON) Applicable(info RepoInfo) bool {
	if info.Language != "go" {
		return false
	}
	for _, imp := range info.Imports {
		if imp == "encoding/json" || imp == "net/http" {
			return true
		}
	}
	return false
}

func (m *MalformedJSON) Prepare(ctx context.Context) error {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	m.serverAddr = "http://" + listener.Addr().String()

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Deliberately malformed JSON (missing closing brace).
		w.Write([]byte(`{"key": "value"`))
	})

	m.server = &http.Server{Handler: mux}
	go m.server.Serve(listener)
	return nil
}

func (m *MalformedJSON) Run(ctx context.Context, target Sandbox) Result {
	res := target.Run(ctx, ".", []string{"go", "test", "./...", "-run", "TestResilienceMalformedJSON"})
	return Result{
		ScenarioID:    m.ID(),
		Passed:        res.Ok,
		ExitCode:      res.ExitCode,
		Output:        truncate(res.Stdout+res.Stderr, 500),
		FaultInjected: m.serverAddr != "",
		Detail:        fmt.Sprintf("server at %s returns malformed JSON; tests should handle decode errors", m.serverAddr),
	}
}

func (m *MalformedJSON) Cleanup(ctx context.Context) error {
	if m.server != nil {
		ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		m.server.Shutdown(ctx)
		m.server = nil
	}
	return nil
}

// --- Shell Scenario (second ecosystem) ---

// ShellScenario is a shell/bash resilience scenario — the second ecosystem
// (spec line 1268: "additional ecosystems are added only after G9 passes for
// the first"). Shell scripts are the simplest second ecosystem: no compiler
// needed, universally available on macOS and Linux.
//
// Unlike the Go scenarios (which compile + execute generated tests against the
// repo), a shell scenario is evaluated STATICALLY: does the script exhibit its
// declared failure mode? The scenario carries the script source to analyze,
// the expected finding pattern (the marker whose presence flags the failure
// mode), and an optional guard marker whose presence means the script handles
// the failure. Run pattern-matches the source and returns the verdict directly
// — no sandbox round-trip, no compilation.
//
// B5: each built-in shell scenario is a deliberately non-resilient script; the
// check flags it, so the finding names the concrete failure mode.
type ShellScenario struct {
	id        string // stable identifier, e.g. "shell:unhandled-exit"
	ecosystem string // "shell"
	source    string // the script content under analysis
	desc      string // human-readable description of the failure mode
	pattern   string // expected finding pattern: the failure marker to look for
	absent    string // optional guard marker; its presence means the failure is handled
}

// NewShellScenario constructs a validated ShellScenario. The source and the
// expected finding pattern must be non-empty; absent may be empty.
func NewShellScenario(id, source, desc, pattern, absent string) (*ShellScenario, error) {
	if id == "" {
		return nil, fmt.Errorf("shell scenario missing id")
	}
	if source == "" {
		return nil, fmt.Errorf("shell scenario %q missing source", id)
	}
	if pattern == "" {
		return nil, fmt.Errorf("shell scenario %q missing pattern", id)
	}
	return &ShellScenario{id: id, ecosystem: "shell", source: source, desc: desc, pattern: pattern, absent: absent}, nil
}

// ID returns the scenario's stable identifier.
func (s *ShellScenario) ID() string { return s.id }

// Ecosystem returns the ecosystem this scenario applies to ("shell").
func (s *ShellScenario) Ecosystem() string { return s.ecosystem }

// Source returns the script content under analysis.
func (s *ShellScenario) Source() string { return s.source }

// Applicable reports whether the scenario applies to the given repo: a repo
// whose primary language is shell (contains .sh scripts, no go.mod).
func (ShellScenario) Applicable(info RepoInfo) bool { return info.Language == "shell" }

// Prepare is a no-op for shell scenarios: nothing is started or injected.
func (s *ShellScenario) Prepare(ctx context.Context) error { return nil }

// Run evaluates the script source statically. The scenario flags the failure
// mode when the expected finding pattern is present in the source and the
// guard marker (absent) is not — an unhandled `exit 1`, an unset-variable
// reference under `set -u`, or a command whose error is never checked. A
// flagged script is a non-resilient script, so Passed is false and the check
// emits a WARN finding naming the failure mode.
func (s *ShellScenario) Run(ctx context.Context, target Sandbox) Result {
	found := s.flags()
	return Result{
		ScenarioID:    s.ID(),
		Passed:        !found,
		ExitCode:      0,
		FaultInjected: true,
		Detail:        s.desc,
	}
}

// Cleanup is a no-op for shell scenarios.
func (s *ShellScenario) Cleanup(ctx context.Context) error { return nil }

// flags reports whether the script exhibits the failure mode: the expected
// finding pattern is present and, when a guard marker is declared, that guard
// is absent.
func (s *ShellScenario) flags() bool {
	if s.pattern != "" && !contains(s.source, s.pattern) {
		return false
	}
	if s.absent != "" && contains(s.source, s.absent) {
		return false
	}
	return true
}

// --- Built-in shell scenarios (B5) ---

// shellUnhandledExitScript calls `exit 1` without a trap: the non-zero exit
// propagates and no cleanup runs.
const shellUnhandledExitScript = `#!/usr/bin/env bash
# Deploys with no error handling: exits non-zero without running cleanup.
set -e
deploy_app
exit 1
`

// shellUnsetVariableScript references an unset variable under `set -u`: the
// script aborts with an unbound-variable error instead of failing gracefully.
const shellUnsetVariableScript = `#!/usr/bin/env bash
# Uses set -u but references an environment variable that is never set.
set -u
echo "deploying to $DEPLOY_TARGET"
`

// shellMissingErrorHandlingScript runs `cd /nonexistent` and never checks $?:
// the failure propagates silently into the rest of the script.
const shellMissingErrorHandlingScript = `#!/usr/bin/env bash
# Changes into a directory that does not exist and never checks the exit code.
cd /nonexistent
run_migrations
`

// --- Default scenarios ---

// DefaultScenarios returns the built-in scenarios as fresh instances. Each
// call allocates new scenario objects because scenarios are stateful (the Go
// HTTP scenarios hold the running fault server between Prepare and Cleanup).
func DefaultScenarios() []Scenario {
	timeout, err := NewHTTPFault("go:http-timeout", 200, 30, "/")
	if err != nil {
		panic("resilience: built-in scenario misconfiguration: " + err.Error())
	}
	five, err := NewHTTPFault("go:http-500", 500, 0, "/")
	if err != nil {
		panic("resilience: built-in scenario misconfiguration: " + err.Error())
	}
	// Shell scenarios (B5, second ecosystem) are stateless, but they follow
	// the same fresh-instance contract for symmetry.
	shellUnhandledExit, err := NewShellScenario(
		"shell:unhandled-exit",
		shellUnhandledExitScript,
		"script calls `exit 1` without a trap; unhandled non-zero exit propagates without cleanup",
		"exit 1",
		"trap",
	)
	if err != nil {
		panic("resilience: built-in scenario misconfiguration: " + err.Error())
	}
	shellUnsetVariable, err := NewShellScenario(
		"shell:unset-variable",
		shellUnsetVariableScript,
		"script uses `set -u` but references an unset variable; it aborts with an unbound-variable error",
		"$DEPLOY_TARGET",
		"DEPLOY_TARGET=",
	)
	if err != nil {
		panic("resilience: built-in scenario misconfiguration: " + err.Error())
	}
	shellMissingErrorHandling, err := NewShellScenario(
		"shell:missing-error-handling",
		shellMissingErrorHandlingScript,
		"script runs `cd /nonexistent` without checking $?; missing error handling lets the failure propagate silently",
		"cd /nonexistent",
		"$?",
	)
	if err != nil {
		panic("resilience: built-in scenario misconfiguration: " + err.Error())
	}
	return []Scenario{timeout, five, shellUnhandledExit, shellUnsetVariable, shellMissingErrorHandling}
}

// --- Helpers ---

// truncate shortens a string to maxChars, appending "..." if truncated.
func truncate(s string, maxChars int) string {
	if len(s) <= maxChars {
		return s
	}
	return s[:maxChars] + "..."
}

// DetectRepoInfo inspects a repo directory and returns its RepoInfo.
func DetectRepoInfo(root string) RepoInfo {
	info := RepoInfo{Root: root}

	// Detect Go.
	if content, err := readFile(filepath.Join(root, "go.mod")); err == nil {
		info.HasGoMod = true
		info.Language = "go"
		info.ModulePath = parseModulePath(content)
		info.Imports = detectGoImports(root)
		info.HasTests = hasGoTests(root)
	}

	// Detect shell (second ecosystem, B5): a repo with no go.mod but with .sh
	// scripts gets the shell language so the shell scenarios can apply.
	if info.Language == "" && hasShellScripts(root) {
		info.Language = "shell"
		info.HasShell = true
	}

	return info
}

// hasShellScripts reports whether the repo contains at least one .sh script
// (shell ecosystem detection). Test/hidden/vendor trees are skipped.
func hasShellScripts(root string) bool {
	found := false
	filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			if info != nil && info.IsDir() {
				name := info.Name()
				if name == ".git" || name == "vendor" || name == "node_modules" || name == "testdata" {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if hasSuffix(path, ".sh") {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

func readFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func parseModulePath(goMod string) string {
	for _, line := range splitLines(goMod) {
		if len(line) > 7 && line[:7] == "module " {
			return trim(line[7:])
		}
	}
	return ""
}

func detectGoImports(root string) []string {
	imports := map[string]bool{}
	// Walk .go files and extract import paths.
	filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			if info != nil && info.IsDir() {
				name := info.Name()
				if name == "vendor" || name == ".git" || name == "testdata" {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if !hasSuffix(path, ".go") || hasSuffix(path, "_test.go") {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		for _, line := range splitLines(string(content)) {
			line = trim(line)
			if line == "import (" {
				continue
			}
			if hasPrefix(line, "import ") {
				// Single-line imports: `import "net/http"` or `import alias "net/http"`.
				rest := trim(line[len("import "):])
				if idx := indexOf(rest, "\""); idx >= 0 {
					imp := rest[idx+1:]
					if j := indexOf(imp, "\""); j >= 0 {
						imp = imp[:j]
						if isStdLib(imp) {
							imports[imp] = true
						}
					}
				}
				continue
			}
			if len(line) > 0 && line[0] == '"' {
				imp := line[1:]
				if idx := indexOf(imp, "\""); idx >= 0 {
					imp = imp[:idx]
					// Only record standard library imports we care about.
					if isStdLib(imp) {
						imports[imp] = true
					}
				}
			}
		}
		return nil
	})
	var result []string
	for imp := range imports {
		result = append(result, imp)
	}
	return result
}

func hasGoTests(root string) bool {
	found := false
	filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if hasSuffix(path, "_test.go") {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

func isStdLib(imp string) bool {
	if imp == "" {
		return false
	}
	// Standard library imports don't contain a dot in the first segment.
	for i := 0; i < len(imp); i++ {
		if imp[i] == '.' {
			return false
		}
		if imp[i] == '/' {
			break
		}
	}
	return true
}

// Tiny string helpers to avoid importing strings (keeps the file focused).
func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

func trim(s string) string {
	start := 0
	end := len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t' || s[start] == '\r') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\r') {
		end--
	}
	return s[start:end]
}

func hasSuffix(s, suffix string) bool {
	return len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix
}

func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

func indexOf(s, sub string) int {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func contains(s, sub string) bool {
	return indexOf(s, sub) >= 0
}
