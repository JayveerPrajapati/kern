package mcp

// Envelope-strictness tests pinning the 2026-10-25 envelope-audit fixes:
// F-1 (jsonrpc "2.0" envelope enforced at both transport decode sites),
// F-3 (stdio line cap aligned to the HTTP 16 MiB body cap),
// F-4 (response frames dropped silently; missing method is -32600),
// F-6 (the spec's notifications/cancelled alias for $/cancelRequest).

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// F-1 (HTTP): a body that parses but does not carry "jsonrpc":"2.0" exactly
// answers -32600 BEFORE the protocol-version gate — the same precedence
// class as the parse error (the body has no valid envelope to negotiate
// with). Mirrors TestHandleHTTPParseErrorBeforeVersionGate's shape.
func TestHandleHTTPEnvelopeRejectedBeforeVersionGate(t *testing.T) {
	t.Parallel()
	s := newHTTPServer()
	for name, body := range map[string]string{
		"missing jsonrpc": `{"id":1,"method":"ping"}`,
		"wrong version":   `{"jsonrpc":"1.0","id":2,"method":"ping"}`,
	} {
		rr := doHTTP(t, s, http.MethodPost, "application/json", body, map[string]string{"MCP-Protocol-Version": ""})
		if rr.Code != http.StatusOK {
			t.Fatalf("%s: expected 200 (JSON-RPC error in body), got %d", name, rr.Code)
		}
		var resp map[string]any
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatalf("%s: unmarshal: %v", name, err)
		}
		e, ok := resp["error"].(map[string]any)
		if !ok || int(e["code"].(float64)) != -32600 {
			t.Fatalf("%s: expected -32600, got %+v", name, resp)
		}
	}
}

// F-1 (stdio): an envelope-less frame is answered -32600 and the session
// keeps serving afterwards.
func TestStdioEnvelopeRejectedAndSurvives(t *testing.T) {
	t.Setenv("KERN_PRELOAD", "0") // hermetic: no background index build
	in := io.MultiReader(
		strings.NewReader("{\"id\":1,\"method\":\"ping\"}\n"), // no jsonrpc field
		strings.NewReader("{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"ping\"}\n"),
	)
	out := &bytes.Buffer{}
	s := NewServer(in, out)
	s.initialized.Store(true)
	if err := ServeStdio(s); err != nil {
		t.Fatalf("ServeStdio: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 responses, got %d: %q", len(lines), out.String())
	}
	var r0, r1 map[string]any
	_ = json.Unmarshal([]byte(lines[0]), &r0)
	_ = json.Unmarshal([]byte(lines[1]), &r1)
	if e, ok := r0["error"].(map[string]any); !ok || int(e["code"].(float64)) != -32600 {
		t.Fatalf("expected -32600 for envelope-less frame, got %q", lines[0])
	}
	if _, ok := r1["result"]; !ok {
		t.Fatalf("session did not survive envelope rejection: %q", lines[1])
	}
}

// F-3: a line over the 16 MiB scanner cap is skipped without a response (no
// -32700 — the line is never parsed), and the session keeps serving — the
// stdio twin of the HTTP 413 body cap.
func TestStdioOversizeLineSkipped(t *testing.T) {
	t.Setenv("KERN_PRELOAD", "0")
	huge := strings.Repeat("a", 16<<20+4096)
	in := io.MultiReader(
		strings.NewReader(huge+"\n"),
		strings.NewReader("{\"jsonrpc\":\"2.0\",\"id\":7,\"method\":\"ping\"}\n"),
	)
	out := &bytes.Buffer{}
	s := NewServer(in, out)
	s.initialized.Store(true)
	if err := ServeStdio(s); err != nil {
		t.Fatalf("ServeStdio: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 1 { // only the ping: the oversize line is skipped
		t.Fatalf("expected exactly 1 response (ping only), got %d: %.80q", len(lines), out.String())
	}
	var r map[string]any
	_ = json.Unmarshal([]byte(lines[0]), &r)
	if id, _ := r["id"].(float64); id != 7 {
		t.Fatalf("expected ping response id 7, got %q", lines[0])
	}
}

// F-4: a response-shaped frame (no method, result/error present) is dropped
// silently even when no sampler is waiting, and a frame with no method at
// all is -32600 — never the old -32601 "method not found: " with an empty
// method name.
func TestDispatchResponseFrameDroppedMissingMethodInvalid(t *testing.T) {
	t.Parallel()
	s := newHTTPServer()
	if got := s.dispatch(rpcRequest{ID: json.RawMessage(`6`), Result: json.RawMessage(`{"x":1}`)}); got != nil {
		t.Fatalf("expected response frame to be dropped, got %+v", got)
	}
	if got := s.dispatch(rpcRequest{ID: json.RawMessage(`6`), Error: json.RawMessage(`{"code":-1}`)}); got != nil {
		t.Fatalf("expected error frame to be dropped, got %+v", got)
	}
	resp := s.dispatch(rpcRequest{ID: json.RawMessage(`8`)})
	b, _ := json.Marshal(resp)
	if !strings.Contains(string(b), "-32600") || !strings.Contains(string(b), "missing method") {
		t.Fatalf("expected -32600 missing method, got %s", b)
	}
}

// F-6: the MCP spec's own spelling — notifications/cancelled with the
// requestId param — cancels an in-flight tools/call exactly like
// $/cancelRequest (mirrors TestCancelRequestAbortsRealToolCall).
func TestNotificationsCancelledAliasCancelsRealToolCall(t *testing.T) {
	t.Setenv("KERN_PRELOAD", "0")
	dispatchTable["kern_test_cancel_spec"] = func(s *Server, ctx context.Context, id string, args map[string]any) (string, error) {
		<-ctx.Done()
		return "handler observed: " + ctx.Err().Error(), nil
	}
	defer delete(dispatchTable, "kern_test_cancel_spec")

	buf := &bytes.Buffer{}
	s := NewServer(strings.NewReader(""), buf)
	s.initialized.Store(true)

	done := make(chan any, 1)
	go func() {
		done <- s.dispatch(rpcRequest{ID: json.RawMessage(`99`), Method: "tools/call", Params: json.RawMessage(`{"name":"kern_test_cancel_spec","arguments":{}}`)})
	}()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && s.Inflight() != 1 {
		time.Sleep(2 * time.Millisecond)
	}
	if s.Inflight() != 1 {
		t.Fatalf("expected tools/call to register in-flight, got %d", s.Inflight())
	}

	// Spec spelling: a notification (no id) carrying requestId + reason.
	s.dispatch(rpcRequest{Method: "notifications/cancelled", Params: json.RawMessage(`{"requestId":99,"reason":"audit"}`)})

	select {
	case resp := <-done:
		b, _ := json.Marshal(resp)
		if !strings.Contains(string(b), "handler observed: context canceled") {
			t.Fatalf("handler not cancelled via notifications/cancelled: %s", b)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("notifications/cancelled did not unblock the in-flight handler")
	}
}
