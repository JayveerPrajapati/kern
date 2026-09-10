package mcp

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/lock"
	"github.com/JayveerPrajapati/kern/internal/project"
)

// supportedProtocolVersions lists every official MCP protocol version the
// Streamable HTTP transport speaks. The wire format is shared, so a client
// negotiating any of these versions can talk to this server.
var supportedProtocolVersions = map[string]bool{
	"2024-11-05": true,
	"2025-03-26": true,
	"2025-06-18": true,
}

// TLSConfig holds the certificate and key file paths used to serve the HTTP
// MCP transport over TLS. Both fields must be set for TLS to be enabled.
//
// TLS is optional and opt-in: a nil *TLSConfig keeps the server on plain HTTP
// (the historical behavior), so existing deployments are unchanged. When TLS
// is enabled the listener still binds to loopback only (localhostAddr) and the
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

// ServeHTTP runs the MCP server over HTTP using the Streamable HTTP transport:
// clients POST JSON-RPC messages to /mcp and receive a plain JSON response
// (SSE is not supported). The listener binds to localhost and rejects requests
// whose Origin is not a local origin.
func ServeHTTP(addr string) error {
	return ServeHTTPContext(context.Background(), addr)
}

// ServeHTTPWithTLS is ServeHTTP with TLS enabled: the listener serves HTTPS
// using the certificate and key referenced by tlsCfg. A nil tlsCfg serves
// plain HTTP (same as ServeHTTP); an incomplete config (only one of CertFile /
// KeyFile set) returns an error instead of silently downgrading to plaintext.
func ServeHTTPWithTLS(addr string, tlsCfg *TLSConfig) error {
	return ServeHTTPContextWithTLS(context.Background(), addr, tlsCfg)
}

// ServeHTTPContext is ServeHTTP with a shutdown context: when ctx is done the
// listener shuts down gracefully and in-flight tools are cancelled. TLS is
// configured through the KERN_MCP_TLS_CERT / KERN_MCP_TLS_KEY environment
// variables; see ServeHTTPContextWithTLS for the explicit-config variant.
func ServeHTTPContext(ctx context.Context, addr string) error {
	return ServeHTTPContextWithTLS(ctx, addr, nil)
}

// ServeHTTPContextWithTLS is ServeHTTPContext with an explicit TLS config. When
// tlsCfg is nil the listener serves plain HTTP (backward compatible). When
// tlsCfg is set and Valid(), the listener serves HTTPS with the given
// certificate and key files. A non-nil but incomplete tlsCfg (only one of
// CertFile/KeyFile set) returns an error before any listener starts: silently
// falling back to plaintext when the operator asked for TLS would be a
// security downgrade.
func ServeHTTPContextWithTLS(ctx context.Context, addr string, tlsCfg *TLSConfig) error {
	if tlsCfg != nil && !tlsCfg.Valid() {
		return fmt.Errorf("kern-mcp TLS config incomplete: both certificate and key files are required (cert=%q key=%q)", tlsCfg.CertFile, tlsCfg.KeyFile)
	}
	srv := &Server{
		sem:       make(chan struct{}, defaultConcurrency()),
		locks:     map[string]*lock.Lock{},
		inflight:  map[string]context.CancelFunc{},
		sessions:  map[string]*project.Session{},
		transport: "http",
		roots:     defaultWorkspaceRoots(),
		gate:      confinementGate(),
		commits:   map[string]string{},
		watchStop: make(chan struct{}),
		watchDone: make(chan struct{}),
	}
	// Same as NewServer: register the built-in default agent so calls without
	// an explicit agent_id are governed (workspace-scoped) instead of denied.
	governance.EnsureDefaultAgent()
	// Same default-on confinement as the stdio path: the KERN_MCP_ROOTS gate
	// runs as the pre-tool-use hook, and KERN_MCP_NO_CONFINE=1 opts out.
	if srv.gate != nil {
		srv.preTool = srv.gate.Check
	}
	// Implicit background index watch: rebuild stale workspace-root indexes
	// between tool calls so the first call after an edit finds a warm index.
	// Disabled by KERN_MCP_WATCH=0; KERN_MCP_WATCH_INTERVAL sets the poll.
	// The watcher stops on ctx cancellation or srv.Close() below.
	srv.StartBackgroundWatch(ctx, watchIntervalFromEnv())
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", srv.handleHTTP)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		res, err := srv.handleHealth(r.Context(), nil)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, res+"\n")
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, "kern MCP server over HTTP\n\nPOST /mcp with a JSON-RPC body (e.g. initialize, tools/list, tools/call, prompts/list, prompts/get).\n")
	})
	// Loopback-only: kern-mcp exposes RCE-capable tools (kern_sandbox,
	// kern_exec), so binding to anything but the loopback interface would be an
	// unauthenticated network attack surface. An explicitly supplied LAN IP is
	// refused outright rather than silently rebinding it to loopback.
	bindAddr, err := localhostAddr(addr)
	if err != nil {
		return err
	}
	hs := &http.Server{
		Addr:              bindAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		// Timeouts guard the socket phase, not handler duration; a long tool
		// finishes in the handler and only then writes the small response.
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	if tlsCfg != nil {
		// TLS is opt-in: when configured, enforce a floor of TLS 1.2 so an
		// operator cannot accidentally serve a transport with legacy ciphers.
		hs.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	done := make(chan error, 1)
	go func() {
		if tlsCfg != nil {
			done <- hs.ListenAndServeTLS(tlsCfg.CertFile, tlsCfg.KeyFile)
			return
		}
		done <- hs.ListenAndServe()
	}()
	select {
	case err := <-done:
		// Stop the background watch so its goroutine never leaks on the
		// listener-error path (the ctx.Done path already calls Close).
		srv.Close()
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	case <-ctx.Done():
		srv.cancelAll()
		srv.Close()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = hs.Shutdown(shutCtx)
		return nil
	}
}

// localhostAddr returns addr bound to an explicit loopback host. A bare port
// (":8080") or empty address binds to 127.0.0.1. Any explicitly supplied
// non-loopback host is refused: kern-mcp exposes RCE-capable tools, so
// exposing it beyond the loopback interface (with only trivially-bypassable
// Origin-header auth) is an unauthenticated RCE. Use kern-server for network
// access.
func localhostAddr(addr string) (string, error) {
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

// isLocalhostOrigin reports whether an Origin header comes from the local
// machine. Empty origins (non-browser clients) are allowed.
func isLocalhostOrigin(r *http.Request) bool {
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

func (s *Server) handleHTTP(w http.ResponseWriter, r *http.Request) {
	if !isLocalhostOrigin(r) {
		http.Error(w, "origin not allowed", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodPost {
		if strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
			http.Error(w, "SSE streaming not supported", http.StatusNotImplemented)
			return
		}
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		http.Error(w, "expected Content-Type: application/json", http.StatusUnsupportedMediaType)
		return
	}
	// MCP-Protocol-Version is mandatory; any official spec version is accepted
	// and echoed back. Missing or unknown versions are rejected.
	if v := r.Header.Get("MCP-Protocol-Version"); !supportedProtocolVersions[v] {
		w.Header().Set("MCP-Protocol-Version", protocolVersion)
		http.Error(w, "unsupported MCP protocol version", http.StatusPreconditionFailed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<24))
	if err != nil {
		http.Error(w, "read error", http.StatusBadRequest)
		return
	}
	// Batch requests were removed from the spec (2025-06-18); reject arrays.
	if strings.HasPrefix(strings.TrimLeft(string(body), " \t\r\n"), "[") {
		writeHTTPError(w, errorResponse(nil, -32700, "batch requests are not supported"))
		return
	}
	var req rpcRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeHTTPError(w, errorResponse(nil, -32700, "parse error"))
		return
	}
	if r := s.dispatch(req); r != nil {
		data, err := json.Marshal(r)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if _, err := w.Write(append(data, '\n')); err != nil {
			fmt.Fprintf(w, "write: %v", err)
		}
		return
	}
	// Notification: no response body, 202 Accepted.
	w.WriteHeader(http.StatusAccepted)
}

func writeHTTPError(w http.ResponseWriter, resp any) {
	w.Header().Set("Content-Type", "application/json")
	data, _ := json.Marshal(resp)
	_, _ = w.Write(append(data, '\n'))
}
