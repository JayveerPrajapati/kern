// Package transport holds the Server-independent HTTP transport plumbing for
// kern-mcp: TLS option resolution, loopback bind policy, and origin checking.
// It is deliberately dependency-free of internal/mcp — the Server kernel
// constructs and calls into this package, never the reverse, so the
// single-constructor invariant (newServerCore) stays intact.
package transport

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// TLSConfig holds the certificate and key file paths used to serve the HTTP
// MCP transport over TLS. Both fields must be set for TLS to be enabled.
//
// TLS is optional and opt-in: a nil *TLSConfig keeps the server on plain HTTP
// (the historical behavior), so existing deployments are unchanged. When TLS
// is enabled the listener still binds to loopback only (LocalhostAddr) and the
// loopback Origin check still applies — TLS here protects the transport
// against loopback sniffing and is the building block for exposing it through
// a local TLS-terminating proxy.
type TLSConfig struct {
	CertFile string // path to the PEM-encoded TLS certificate (chain)
	KeyFile  string // path to the PEM-encoded TLS private key
}

// Valid reports whether the config is complete enough to serve TLS. A config
// with exactly one of CertFile/KeyFile set is invalid: silently serving plain
// HTTP in that state would be a security downgrade, so callers fail fast
// instead.
func (c *TLSConfig) Valid() bool {
	return c != nil && c.CertFile != "" && c.KeyFile != ""
}

// TLSOptionsFromEnv returns the TLS config derived from the KERN_MCP_TLS_CERT
// and KERN_MCP_TLS_KEY environment variables. It returns nil when neither
// variable is set (plain HTTP). When exactly one is set the returned config is
// incomplete and Valid() reports false, so callers can fail fast instead of
// serving a half-configured listener.
//
// Environment variables:
//   - KERN_MCP_TLS_CERT — path to the PEM-encoded TLS certificate file
//   - KERN_MCP_TLS_KEY  — path to the PEM-encoded TLS private key file
func TLSOptionsFromEnv() *TLSConfig {
	cert, key := os.Getenv("KERN_MCP_TLS_CERT"), os.Getenv("KERN_MCP_TLS_KEY")
	if cert == "" && key == "" {
		return nil
	}
	return &TLSConfig{CertFile: cert, KeyFile: key}
}

// TLSOptions returns the effective TLS config for the HTTP transport. Explicit
// command-line flag values win; the KERN_MCP_TLS_CERT / KERN_MCP_TLS_KEY
// environment variables fill in whichever field the flags leave empty. A nil
// result means plain HTTP.
func TLSOptions(certFlag, keyFlag string) *TLSConfig {
	env := TLSOptionsFromEnv()
	cert, key := certFlag, keyFlag
	if env != nil {
		if cert == "" {
			cert = env.CertFile
		}
		if key == "" {
			key = env.KeyFile
		}
	}
	if cert == "" && key == "" {
		return nil
	}
	return &TLSConfig{CertFile: cert, KeyFile: key}
}

// LocalhostAddr returns addr bound to an explicit loopback host. A bare port
// (":8080") or empty address binds to 127.0.0.1. Any explicitly supplied
// non-loopback host is refused: kern-mcp exposes RCE-capable tools, so
// exposing it beyond the loopback interface (with only trivially-bypassable
// Origin-header auth) is an unauthenticated RCE. Use kern-server for network
// access.
func LocalhostAddr(addr string) (string, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		if addr == "" {
			return "127.0.0.1:8080", nil
		}
		return "127.0.0.1:" + addr, nil
	}
	host = strings.ToLower(host)
	if host == "" || host == "0.0.0.0" || host == "::" || host == "127.0.0.1" || host == "localhost" || host == "::1" {
		return "127.0.0.1:" + port, nil
	}
	return "", fmt.Errorf("kern-mcp --http only supports loopback binds for security; use kern-server for network access")
}

// IsLocalhostOrigin reports whether an Origin header comes from the local
// machine. Empty origins (non-browser clients) are allowed.
func IsLocalhostOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	host := u.Hostname()
	if host == "" {
		host = u.Host
	}
	switch strings.ToLower(host) {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	return false
}

// maxUnixSocketPath is the longest unix socket path we accept. The OS limit is
// baked into the sockaddr's sun_path array: 104 bytes on macOS (BSD) and 108
// bytes on Linux. Going over it makes bind/connect fail with a silent EINVAL,
// so we reject over-long paths with a clear error before the OS does.
const maxUnixSocketPath = 100

// ServeListener owns the listener lifecycle for the HTTP MCP transport:
// unix-socket or loopback TCP setup (see LocalhostAddr), the http.Server with
// its socket-phase timeouts, the TLS wrap when tlsCfg is set and Valid(), and
// graceful drain. It blocks until the listener fails or ctx is done.
//
// Shutdown contract: on ctx cancellation, onShutdown runs FIRST (cancel
// in-flight tools, release server-side resources), then the HTTP server drains
// gracefully for up to 5 seconds; the return is nil in that case. On a
// listener or serve error, onShutdown is NOT called — the caller learns the
// server never came up (or died) from the returned error and owns cleanup.
// http.ErrServerClosed maps to nil. A non-nil, incomplete tlsCfg returns an
// error before any listener starts: silently falling back to plaintext when
// the operator asked for TLS would be a security downgrade.
func ServeListener(ctx context.Context, addr string, tlsCfg *TLSConfig, h http.Handler, onShutdown func()) error {
	if tlsCfg != nil && !tlsCfg.Valid() {
		return fmt.Errorf("kern-mcp TLS config incomplete: both certificate and key files are required (cert=%q key=%q)", tlsCfg.CertFile, tlsCfg.KeyFile)
	}
	var ln net.Listener
	isUnix := strings.HasPrefix(addr, "unix:") || strings.HasPrefix(addr, "/") || strings.HasSuffix(addr, ".sock")
	if isUnix {
		sockPath := strings.TrimPrefix(addr, "unix:")
		if len(sockPath) > maxUnixSocketPath {
			return fmt.Errorf("unix socket path too long (%d bytes, limit ~100): %s — use a shorter --addr path", len(sockPath), sockPath)
		}
		if err := os.MkdirAll(filepath.Dir(sockPath), 0o755); err != nil {
			return err
		}
		_ = os.Remove(sockPath)
		uln, err := net.Listen("unix", sockPath)
		if err != nil {
			return fmt.Errorf("listen unix socket %s: %w", sockPath, err)
		}
		_ = os.Chmod(sockPath, 0o600)
		defer func() { _ = os.Remove(sockPath) }()
		defer func() { _ = uln.Close() }()
		ln = uln
	} else {
		bindAddr, err := LocalhostAddr(addr)
		if err != nil {
			return err
		}
		tln, err := net.Listen("tcp", bindAddr)
		if err != nil {
			return err
		}
		defer func() { _ = tln.Close() }()
		ln = tln
	}

	hs := &http.Server{
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		// Timeouts guard the socket phase, not handler duration; a long tool
		// finishes in the handler and only then writes the small response.
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	if tlsCfg != nil && tlsCfg.Valid() {
		cert, err := tls.LoadX509KeyPair(tlsCfg.CertFile, tlsCfg.KeyFile)
		if err != nil {
			return fmt.Errorf("load tls cert: %w", err)
		}
		hs.TLSConfig = &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
		}
		ln = tls.NewListener(ln, hs.TLSConfig)
	}

	done := make(chan error, 1)
	go func() {
		done <- hs.Serve(ln)
	}()
	select {
	case err := <-done:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	case <-ctx.Done():
		if onShutdown != nil {
			onShutdown()
		}
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = hs.Shutdown(shutCtx)
		return nil
	}
}
