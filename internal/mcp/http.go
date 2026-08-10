package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

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

// ServeHTTP runs the MCP server over HTTP using the Streamable HTTP transport:
// clients POST JSON-RPC messages to /mcp and receive the response body. SSE
// streaming is not supported (advertised via streamableHttpCapabilities) so the
// endpoint answers plain JSON. Every request is stateless and handled by the
// same dispatch as stdio.
//
// The listener binds to localhost (127.0.0.1) unless addr already names a
// host. Requests carrying an Origin header that is not a local origin are
// rejected so a browser page cannot call the local endpoint (CSRF guard).
func ServeHTTP(addr string) error {
	return ServeHTTPContext(context.Background(), addr)
}

// ServeHTTPContext is ServeHTTP with a shutdown context: when ctx is done the
// listener shuts down gracefully and in-flight tools are cancelled.
func ServeHTTPContext(ctx context.Context, addr string) error {
	srv := &Server{
		locks:     map[string]*lock.Lock{},
		inflight:  map[string]context.CancelFunc{},
		sessions:  map[string]*project.Session{},
		transport: "http",
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", srv.handleHTTP)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, "kern MCP server over HTTP\n\nPOST /mcp with a JSON-RPC body (e.g. initialize, tools/list, tools/call, prompts/list, prompts/get).\n")
	})
	hs := &http.Server{
		Addr:              localhostAddr(addr),
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	done := make(chan error, 1)
	go func() {
		done <- hs.ListenAndServe()
	}()
	select {
	case err := <-done:
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

// localhostAddr returns addr with an explicit loopback host when the caller
// did not specify one, so the MCP endpoint is never exposed to the network by
// default. A bare port (":8080") or empty address binds to 127.0.0.1.
func localhostAddr(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		if addr == "" {
			return "127.0.0.1:8080"
		}
		return "127.0.0.1:" + addr
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		return "127.0.0.1:" + port
	}
	return addr
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
	// MCP-Protocol-Version is mandatory in the Streamable HTTP transport.
	// The server implements one wire format but accepts every official
	// version of the MCP spec so standard clients (2024-11-05, 2025-03-26,
	// 2025-06-18) can connect; the negotiated version is echoed back in the
	// response header. Unknown or missing versions are rejected.
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
