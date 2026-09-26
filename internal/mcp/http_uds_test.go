package mcp

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestServeHTTPOverUnixDomainSocket(t *testing.T) {
	t.Parallel()
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

	// Security pin: the socket file must be 0600 (owner-only) so no other
	// local user on a multi-user host can connect to it, and the listener
	// must not loosen a restricted parent dir (this test's 0700 MkdirTemp).
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

	// Clean-shutdown pin: the socket file must be unlinked.
	if _, err := os.Stat(sockPath); !os.IsNotExist(err) {
		t.Errorf("socket file %s still exists after clean shutdown (stat err: %v)", sockPath, err)
	}
}

// TestServeHTTPAutoUnixSocketDefault pins the new preferred default: an
// unspecified transport (the "auto" sentinel) serves on a 0600 unix socket in
// a fresh 0700 temp dir, reachable through the socket, unlinked on shutdown,
// with the temp dir removed by the resolver cleanup.
func TestServeHTTPAutoUnixSocketDefault(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix sockets unavailable on windows; auto-select falls back to loopback TCP")
	}
	listenAddr, cleanup := ResolveHTTPAddr("auto")
	if cleanup == nil {
		t.Fatal("auto-select must return a temp-dir cleanup")
	}
	// Safety net for early failures; the assertion path also calls cleanup()
	// explicitly after shutdown (RemoveAll is idempotent).
	defer cleanup()
	sockPath := strings.TrimPrefix(listenAddr, "unix:")
	if sockPath == listenAddr {
		t.Fatalf("expected unix: address, got %q", listenAddr)
	}
	if len(sockPath) > 100 { // portable sun_path budget; see transport.maxUnixSocketPath
		t.Fatalf("auto socket path %d bytes exceeds the 100-byte budget: %s", len(sockPath), sockPath)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	// Pass the RESOLVED address (as the CLIs do): re-resolving "auto" here
	// would create a second temp dir the test does not probe.
	go func() { errCh <- ServeHTTPContext(ctx, listenAddr) }()

	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return net.Dial("unix", sockPath)
			},
		},
		Timeout: 2 * time.Second,
	}
	deadline := time.Now().Add(5 * time.Second)
	var resp *http.Response
	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(ctx, "GET", "http://unix/health", nil)
		if err == nil {
			resp, err = client.Do(req)
			if err == nil {
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					break
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if resp == nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("failed to reach auto unix socket at %s", sockPath)
	}

	// Security pins: socket 0600, parent dir 0700.
	fi, err := os.Stat(sockPath)
	if err != nil {
		t.Fatalf("stat socket: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("auto socket mode = %04o, want 0600", perm)
	}
	parent := filepath.Dir(sockPath)
	if pi, err := os.Stat(parent); err != nil {
		t.Fatalf("stat parent dir: %v", err)
	} else if perm := pi.Mode().Perm(); perm != 0o700 {
		t.Errorf("auto parent dir mode = %04o, want 0700", perm)
	}

	// Clean shutdown: nil error, socket unlinked, temp dir removed.
	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("expected nil error on shutdown, got: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not shut down in time")
	}
	if _, err := os.Stat(sockPath); !os.IsNotExist(err) {
		t.Errorf("socket file still exists after clean shutdown (stat err: %v)", err)
	}
	// The resolver cleanup (which the CLIs defer at process scope) removes
	// the temp dir the auto-select created.
	cleanup()
	if _, err := os.Stat(parent); !os.IsNotExist(err) {
		t.Errorf("auto temp dir still exists after resolver cleanup (stat err: %v)", err)
	}
}

// TestResolveHTTPAddr pins the transport-selection contract: explicit unix
// paths and explicit TCP addresses pass through unchanged; an unspecified
// transport ("", "auto", "uds") auto-selects a 0600 unix socket in a 0700
// temp dir; KERN_MCP_TRANSPORT=tcp forces the legacy loopback TCP fallback.
func TestResolveHTTPAddr(t *testing.T) {
	t.Run("explicit unix passes through", func(t *testing.T) {
		for _, addr := range []string{"unix:/tmp/x.sock", "/abs/path.sock", "rel.sock", "unix:relative.sock"} {
			got, cleanup := ResolveHTTPAddr(addr)
			if got != addr || cleanup != nil {
				t.Errorf("ResolveHTTPAddr(%q) = (%q, cleanup=%v), want (%q, nil)", addr, got, cleanup != nil, addr)
			}
		}
	})
	t.Run("explicit TCP passes through", func(t *testing.T) {
		for _, addr := range []string{":8080", "127.0.0.1:1234", "localhost:9999", "0.0.0.0:80"} {
			got, cleanup := ResolveHTTPAddr(addr)
			if got != addr || cleanup != nil {
				t.Errorf("ResolveHTTPAddr(%q) = (%q, cleanup=%v), want (%q, nil)", addr, got, cleanup != nil, addr)
			}
		}
	})
	t.Run("auto selects 0700 unix socket", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("unix sockets unavailable on windows")
		}
		for _, addr := range []string{"", "auto", "uds"} {
			got, cleanup := ResolveHTTPAddr(addr)
			if cleanup == nil {
				t.Fatalf("ResolveHTTPAddr(%q): expected cleanup for auto UDS", addr)
			}
			sockPath := strings.TrimPrefix(got, "unix:")
			if sockPath == got {
				t.Fatalf("ResolveHTTPAddr(%q) = %q, want unix: path", addr, got)
			}
			parent := filepath.Dir(sockPath)
			if pi, err := os.Stat(parent); err != nil {
				t.Fatalf("ResolveHTTPAddr(%q): stat parent %s: %v", addr, parent, err)
			} else if perm := pi.Mode().Perm(); perm != 0o700 {
				t.Errorf("ResolveHTTPAddr(%q): parent dir mode = %04o, want 0700", addr, perm)
			}
			// The cleanup func removes the temp dir (socket is unlinked by
			// the listener on shutdown; the dir is ours to remove).
			cleanup()
			if _, err := os.Stat(parent); !os.IsNotExist(err) {
				t.Errorf("ResolveHTTPAddr(%q): cleanup did not remove temp dir %s", addr, parent)
			}
		}
	})
	t.Run("KERN_MCP_TRANSPORT=tcp forces loopback fallback", func(t *testing.T) {
		t.Setenv("KERN_MCP_TRANSPORT", "tcp")
		for _, addr := range []string{"", "auto", "uds"} {
			got, cleanup := ResolveHTTPAddr(addr)
			if got != "127.0.0.1:8080" || cleanup != nil {
				t.Errorf("ResolveHTTPAddr(%q) with KERN_MCP_TRANSPORT=tcp = (%q, cleanup=%v), want (127.0.0.1:8080, nil)", addr, got, cleanup != nil)
			}
		}
		// An explicit address still wins over the env var.
		if got, cleanup := ResolveHTTPAddr(":9090"); got != ":9090" || cleanup != nil {
			t.Errorf("explicit addr with KERN_MCP_TRANSPORT=tcp = (%q, cleanup=%v), want (:9090, nil)", got, cleanup != nil)
		}
	})
}

// TestUnixSocketPathTooLongErrors: a socket path longer than the portable
// sun_path budget must fail fast with a clear error, never surface as the
// OS's silent "invalid argument" (macOS sun_path is 104 bytes; Linux 108).
func TestUnixSocketPathTooLongErrors(t *testing.T) {
	t.Parallel()
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
