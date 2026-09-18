package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/mcp/transport"
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
// clients POST JSON-RPC messages to /mcp and receive a plain JSON response
// (SSE is not supported). The listener binds to localhost and rejects requests
// whose Origin is not a local origin.
func ServeHTTP(addr string) error {
	return ServeHTTPContext(context.Background(), addr)
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
// security downgrade. The listener lifecycle itself (unix socket / loopback
// TCP bind, TLS wrap, graceful drain) is owned by transport.ServeListener.
func ServeHTTPContextWithTLS(ctx context.Context, addr string, tlsCfg *transport.TLSConfig) error {
	srv := newServerCore("http")
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
	err := transport.ServeListener(ctx, addr, tlsCfg, mux, func() {
		srv.cancelAll()
		srv.Close()
	})
	if err != nil {
		// Stop the background watch so its goroutine never leaks on the
		// listener-error path (the ctx.Done path already closed the server
		// via the onShutdown callback above).
		srv.Close()
		return err
	}
	return nil
}

func (s *Server) handleHTTP(w http.ResponseWriter, r *http.Request) {
	if !transport.IsLocalhostOrigin(r) {
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
			_, _ = fmt.Fprintf(w, "write: %v", err)
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
