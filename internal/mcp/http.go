package mcp

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/lock"
)

// ServeHTTP runs the MCP server over HTTP using the Streamable HTTP transport:
// clients POST JSON-RPC messages to /mcp and receive the response body. SSE
// streaming is not supported (advertised via streamableHttpCapabilities) so the
// endpoint answers plain JSON. Every request is stateless and handled by the
// same dispatch as stdio.
func ServeHTTP(addr string) error {
	srv := &Server{
		locks:     map[string]*lock.Lock{},
		transport: "http",
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", srv.handleHTTP)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, "kern MCP server over HTTP\n\nPOST /mcp with a JSON-RPC body (e.g. initialize, tools/list, tools/call, prompts/list, prompts/get).\n")
	})
	hs := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	return hs.ListenAndServe()
}

func (s *Server) handleHTTP(w http.ResponseWriter, r *http.Request) {
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
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<24))
	if err != nil {
		http.Error(w, "read error", http.StatusBadRequest)
		return
	}
	var resp any
	first := strings.TrimLeft(string(body), " \t\r\n")
	if strings.HasPrefix(first, "[") {
		var batch []rpcRequest
		if err := json.Unmarshal(body, &batch); err != nil {
			writeHTTPError(w, errorResponse(nil, -32700, "parse error"))
			return
		}
		var out []any
		for _, req := range batch {
			if resp := s.dispatch(req); resp != nil {
				out = append(out, resp)
			}
		}
		if out == nil {
			out = []any{}
		}
		resp = out
	} else {
		var req rpcRequest
		if err := json.Unmarshal(body, &req); err != nil {
			writeHTTPError(w, errorResponse(nil, -32700, "parse error"))
			return
		}
		if r := s.dispatch(req); r != nil {
			resp = r
		} else {
			resp = map[string]any{}
		}
	}
	data, err := json.Marshal(resp)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if _, err := w.Write(append(data, '\n')); err != nil {
		fmt.Fprintf(w, "write: %v", err)
	}
}

func writeHTTPError(w http.ResponseWriter, resp any) {
	w.Header().Set("Content-Type", "application/json")
	data, _ := json.Marshal(resp)
	_, _ = w.Write(append(data, '\n'))
}
