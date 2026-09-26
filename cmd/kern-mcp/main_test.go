package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// safeBuffer is a mutex-guarded bytes.Buffer for capturing child-process
// output: os/exec copies into it from its own goroutine while the test polls
// it, so a plain bytes.Buffer is a data race under -race.
type safeBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *safeBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *safeBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func (s *safeBuffer) Bytes() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Bytes()
}

// binPath is the real kern-mcp binary, built once in TestMain. The test
// binary's own main() is the testing harness, so the production main (flag
// parsing, stdio/HTTP serving, signal handling) can only be exercised by
// exec'ing the built binary — same pattern as cmd/blueprint's e2e tests.
var binPath string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "kern-mcp-bin-*")
	if err != nil {
		panic(err)
	}
	binPath = filepath.Join(dir, "kern-mcp")
	if out, err := exec.Command("go", "build", "-o", binPath, ".").CombinedOutput(); err != nil {
		panic(fmt.Sprintf("build kern-mcp: %v\n%s", err, out))
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// TestVersionFlags asserts the version contract shared with doctor's
// version-parity probe: -v, -version and the bare "version" arg all print a
// "kern-mcp <version>" banner and exit 0 without starting the stdio server.
func TestVersionFlags(t *testing.T) {
	for _, args := range [][]string{{"-v"}, {"-version"}, {"version"}} {
		out, err := exec.Command(binPath, args...).CombinedOutput()
		if err != nil {
			t.Fatalf("kern-mcp %v: %v", args, err)
		}
		if !strings.Contains(string(out), "kern-mcp") {
			t.Errorf("kern-mcp %v: expected version banner, got %q", args, string(out))
		}
	}
}

// TestStdioInitializeRoundTrip drives the real stdio transport: one JSON-RPC
// initialize message in, a serverInfo result out, then a SIGTERM drains
// cleanly (exit 0 — the graceful-shutdown contract).
func TestStdioInitializeRoundTrip(t *testing.T) {
	cmd := exec.Command(binPath)
	cmd.Env = append(os.Environ(),
		"KERN_PRELOAD=0",
		"KERN_MCP_WATCH=0",
		"XDG_CACHE_HOME="+t.TempDir(),
	)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr safeBuffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	req := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{}}}` + "\n"
	if _, err := io.WriteString(stdin, req); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(stdout.String(), `"serverInfo"`) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(stdout.String(), `"serverInfo"`) {
		t.Fatalf("no initialize result within 10s; stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	// The response must be parseable JSON-RPC.
	var resp struct {
		Result struct {
			ServerInfo struct {
				Name string `json:"name"`
			} `json:"serverInfo"`
		} `json:"result"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &resp); err != nil {
		t.Fatalf("initialize response is not JSON: %v (out=%q)", err, stdout.String())
	}
	if resp.Result.ServerInfo.Name != "kern" {
		t.Errorf("expected serverInfo.name kern, got %q", resp.Result.ServerInfo.Name)
	}

	// SIGTERM must drain cleanly and exit 0.
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	waitCh := make(chan error, 1)
	go func() { waitCh <- cmd.Wait() }()
	select {
	case err := <-waitCh:
		if err != nil {
			t.Errorf("SIGTERM should drain with exit 0, got %v (stderr=%q)", err, stderr.String())
		}
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("server did not shut down within 10s of SIGTERM")
	}
}

// TestHTTPHealthAndShutdown starts the HTTP transport on a loopback port,
// polls /health until the listener answers, then SIGTERM must produce a
// clean exit 0 (the serve/drain contract of ServeHTTPContext).
func TestHTTPHealthAndShutdown(t *testing.T) {
	port := freePort(t)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	cmd := exec.Command(binPath, "--http", addr)
	cmd.Env = append(os.Environ(),
		"KERN_PRELOAD=0",
		"KERN_MCP_WATCH=0",
		"XDG_CACHE_HOME="+t.TempDir(),
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()

	client := &http.Client{Timeout: 2 * time.Second}
	url := fmt.Sprintf("http://%s/health", addr)
	deadline := time.Now().Add(10 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK && len(bytes.TrimSpace(body)) > 0 {
				break
			}
			lastErr = fmt.Errorf("status %d body %q", resp.StatusCode, body)
		} else {
			lastErr = err
		}
		time.Sleep(10 * time.Millisecond)
	}
	if lastErr != nil && time.Now().After(deadline) {
		t.Fatalf("/health never healthy: %v (stderr=%q)", lastErr, stderr.String())
	}

	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	waitCh := make(chan error, 1)
	go func() { waitCh <- cmd.Wait() }()
	select {
	case err := <-waitCh:
		if err != nil {
			t.Errorf("SIGTERM should exit 0, got %v (stderr=%q)", err, stderr.String())
		}
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("HTTP server did not shut down within 10s of SIGTERM")
	}
}

// freePort reserves an ephemeral loopback port and releases it, so the child
// process can bind it. The window between close and bind is a benign race.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port
}

// withoutEnv returns the environment with the named variables removed, so a
// developer's exported KERN_MCP_TRANSPORT (or other overrides) cannot leak
// into a child process under test.
func withoutEnv(vars ...string) []string {
	drop := map[string]bool{}
	for _, v := range vars {
		drop[v] = true
	}
	var out []string
	for _, kv := range os.Environ() {
		if k, _, ok := strings.Cut(kv, "="); ok && drop[k] {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// TestHTTPAutoUnixSocketAndShutdown exercises the new default transport end to
// end through the real binary: `kern-mcp --http auto` serves on a 0600 unix
// socket in a 0700 temp dir, announced on stderr, answers /health through the
// socket, and a SIGTERM produces a clean exit 0 with the socket unlinked.
func TestHTTPAutoUnixSocketAndShutdown(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix sockets unavailable on windows; auto-select falls back to loopback TCP")
	}
	cmd := exec.Command(binPath, "--http", "auto")
	cmd.Env = append(withoutEnv("KERN_MCP_TRANSPORT"),
		"KERN_PRELOAD=0",
		"KERN_MCP_WATCH=0",
		"XDG_CACHE_HOME="+t.TempDir(),
	)
	var stderr safeBuffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()

	// The server announces the socket path on stderr; parse it.
	var sockPath string
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if line := stderr.String(); strings.Contains(line, "listening on unix:") {
			rest := strings.TrimSpace(strings.TrimPrefix(line, "kern-mcp: listening on unix:"))
			if rest != "" {
				sockPath = rest
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if sockPath == "" {
		t.Fatalf("no unix socket announcement on stderr within 10s: %q", stderr.String())
	}

	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return net.Dial("unix", sockPath)
			},
		},
		Timeout: 2 * time.Second,
	}
	url := "http://unix/health"
	deadline = time.Now().Add(10 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK && len(bytes.TrimSpace(body)) > 0 {
				break
			}
			lastErr = fmt.Errorf("status %d body %q", resp.StatusCode, body)
		} else {
			lastErr = err
		}
		time.Sleep(10 * time.Millisecond)
	}
	if lastErr != nil && time.Now().After(deadline) {
		t.Fatalf("/health never healthy over unix socket %s: %v (stderr=%q)", sockPath, lastErr, stderr.String())
	}

	// Security pin: the socket file is 0600 and its parent dir 0700.
	fi, err := os.Stat(sockPath)
	if err != nil {
		t.Fatalf("stat socket: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("socket mode = %04o, want 0600", perm)
	}
	if pi, err := os.Stat(filepath.Dir(sockPath)); err == nil {
		if perm := pi.Mode().Perm(); perm != 0o700 {
			t.Errorf("parent dir mode = %04o, want 0700", perm)
		}
	}

	// SIGTERM must drain cleanly, exit 0, and unlink the socket.
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	waitCh := make(chan error, 1)
	go func() { waitCh <- cmd.Wait() }()
	select {
	case err := <-waitCh:
		if err != nil {
			t.Errorf("SIGTERM should exit 0, got %v (stderr=%q)", err, stderr.String())
		}
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("server did not shut down within 10s of SIGTERM")
	}
	if _, err := os.Stat(sockPath); !os.IsNotExist(err) {
		t.Errorf("socket file %s still exists after clean shutdown (stat err: %v)", sockPath, err)
	}
}
