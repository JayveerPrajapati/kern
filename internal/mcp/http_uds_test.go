package mcp

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestServeHTTPOverUnixDomainSocket(t *testing.T) {
	// Use a short temp dir: t.TempDir() derives its path from the test name
	// (33 chars here), which pushes the socket path past macOS's 104-byte
	// sun_path limit and makes bind/connect fail with EINVAL.
	sockDir, err := os.MkdirTemp("", "mcp-uds-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	sockPath := filepath.Join(sockDir, "mcp.sock")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- ServeHTTPContext(ctx, "unix:"+sockPath)
	}()

	// Wait for socket to become active
	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return net.Dial("unix", sockPath)
			},
		},
		Timeout: 2 * time.Second,
	}

	deadline := time.Now().Add(3 * time.Second)
	var resp *http.Response
	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(ctx, "POST", "http://unix/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{}}}`))
		if err == nil {
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("MCP-Protocol-Version", "2024-11-05")
			resp, err = client.Do(req)
			if err == nil {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}

	if resp == nil {
		t.Fatalf("failed to connect to unix socket at %s", sockPath)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "result") {
		t.Errorf("expected response to contain result, got: %s", string(body))
	}

	// Cancel context and ensure server stops cleanly
	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("expected nil error on shutdown, got: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("server did not shut down in time")
	}
}

// TestUnixSocketPathTooLongErrors: a socket path longer than the portable
// sun_path budget must fail fast with a clear error, never surface as the
// OS's silent "invalid argument" (macOS sun_path is 104 bytes; Linux 108).
func TestUnixSocketPathTooLongErrors(t *testing.T) {
	long := filepath.Join(t.TempDir(), strings.Repeat("a", 120), "mcp.sock")
	if len(long) <= 100 { // portable sun_path budget; see transport.maxUnixSocketPath
		t.Fatalf("fixture path %d bytes must exceed the 100-byte socket path budget", len(long))
	}
	err := ServeHTTPContext(context.Background(), "unix:"+long)
	if err == nil {
		t.Fatal("expected an error for an over-long unix socket path")
	}
	if !strings.Contains(err.Error(), "too long") {
		t.Errorf("expected a clear path-length error, got: %v", err)
	}
}
