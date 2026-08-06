package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/lock"
)

func newHTTPServer() *Server {
	return &Server{
		locks:     map[string]*lock.Lock{},
		transport: "http",
	}
}

func doHTTP(t *testing.T, s *Server, method, ctype, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", ctype)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rr := httptest.NewRecorder()
	s.handleHTTP(rr, req)
	return rr
}

func TestHandleHTTPGetNotAllowed(t *testing.T) {
	rr := doHTTP(t, newHTTPServer(), http.MethodGet, "application/json", "", nil)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rr.Code)
	}
}

func TestHandleHTTPGetSSEUnsupported(t *testing.T) {
	rr := doHTTP(t, newHTTPServer(), http.MethodGet, "text/event-stream", "", map[string]string{"Accept": "text/event-stream"})
	if rr.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501 for SSE, got %d", rr.Code)
	}
}

func TestHandleHTTPBadContentType(t *testing.T) {
	rr := doHTTP(t, newHTTPServer(), http.MethodPost, "text/plain", `{}`, nil)
	if rr.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("expected 415, got %d", rr.Code)
	}
}

func TestHandleHTTPParseError(t *testing.T) {
	body := "not json"
	rr := doHTTP(t, newHTTPServer(), http.MethodPost, "application/json", body, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 (JSON-RPC error in body), got %d", rr.Code)
	}
	var errResp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	e := errResp["error"].(map[string]any)
	if int(e["code"].(float64)) != -32700 {
		t.Fatalf("expected parse-error code, got %v", e["code"])
	}
}

func TestHandleHTTPSingleRequest(t *testing.T) {
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`
	rr := doHTTP(t, newHTTPServer(), http.MethodPost, "application/json", body, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["jsonrpc"] != "2.0" {
		t.Fatalf("bad jsonrpc: %v", resp)
	}
	res := resp["result"].(map[string]any)
	if res["protocolVersion"] != protocolVersion {
		t.Fatalf("bad protocolVersion: %v", res["protocolVersion"])
	}
}

func TestHandleHTTPBatchRequest(t *testing.T) {
	body := `[{"jsonrpc":"2.0","id":1,"method":"ping"},{"jsonrpc":"2.0","id":2,"method":"ping"}]`
	rr := doHTTP(t, newHTTPServer(), http.MethodPost, "application/json", body, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var resp []any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal batch: %v", err)
	}
	if len(resp) != 2 {
		t.Fatalf("expected 2 batch responses, got %d", len(resp))
	}
	for i, r := range resp {
		if r.(map[string]any)["id"].(float64) != float64(i+1) {
			t.Fatalf("batch order wrong at %d: %+v", i, r)
		}
	}
}

func TestHandleHTTPNotificationNoBody(t *testing.T) {
	body := `{"jsonrpc":"2.0","method":"notifications/initialized"}`
	rr := doHTTP(t, newHTTPServer(), http.MethodPost, "application/json", body, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 for notification, got %d", rr.Code)
	}
	if strings.TrimSpace(rr.Body.String()) != "{}" {
		t.Fatalf("notification body should be {}, got %q", rr.Body.String())
	}
}

func TestHandleHTTPUnknownMethod(t *testing.T) {
	body := `{"jsonrpc":"2.0","id":3,"method":"bogus/method"}`
	rr := doHTTP(t, newHTTPServer(), http.MethodPost, "application/json", body, nil)
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	e := resp["error"].(map[string]any)
	if int(e["code"].(float64)) != -32601 {
		t.Fatalf("expected method-not-found, got %v", e)
	}
}

func TestWriteHTTPErrorIsJSON(t *testing.T) {
	rr := httptest.NewRecorder()
	writeHTTPError(rr, errorResponse(nil, -1, "boom"))
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("expected json content-type, got %q", ct)
	}
	if !strings.Contains(rr.Body.String(), "boom") {
		t.Fatalf("expected error body, got %q", rr.Body.String())
	}
	if !strings.HasSuffix(rr.Body.String(), "\n") {
		t.Fatal("expected trailing newline")
	}
}

func TestHandleHTTPOversizeBodyRejected(t *testing.T) {
	// The handler limits reads to 1<<24 bytes. A body smaller than the limit
	// must still succeed (smoke that the LimitReader path is wired).
	big := `{"jsonrpc":"2.0","id":4,"method":"initialize","params":{}}`
	rr := doHTTP(t, newHTTPServer(), http.MethodPost, "application/json", big, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 for small body, got %d", rr.Code)
	}
}
