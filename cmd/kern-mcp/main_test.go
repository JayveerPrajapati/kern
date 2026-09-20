package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
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
