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

// W1+W7 (2026-10-26 black-box audit, stdio): a frame of valid JSON whose top
// level is not a request object (an array, a bare scalar) or whose field
// types are wrong (method as a number) is an invalid REQUEST (-32600) with
// a sanitized message — never a -32700 parse error leaking Go type names
// like mcp.rpcRequest. The session keeps serving afterwards.
func TestStdioWrongShapeIsInvalidRequestNotParseError(t *testing.T) {
	t.Setenv("KERN_PRELOAD", "0") // hermetic: no background index build
	in := io.MultiReader(
		strings.NewReader("[1,2]\n"), // array envelope
		strings.NewReader("42\n"),    // bare scalar
		strings.NewReader("{\"jsonrpc\":\"2.0\",\"id\":7,\"method\":123}\n"), // wrong field type
		strings.NewReader("{\"jsonrpc\":\"2.0\",\"id\":8,\"method\":\"ping\"}\n"),
	)
	out := &bytes.Buffer{}
	s := NewServer(in, out)
	s.initialized.Store(true)
	if err := ServeStdio(s); err != nil {
		t.Fatalf("ServeStdio: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 4 {
		t.Fatalf("expected 4 responses, got %d: %q", len(lines), out.String())
	}
	for i, line := range lines[:3] {
		var resp map[string]any
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			t.Fatalf("line %d: unmarshal: %v", i, err)
		}
		e, ok := resp["error"].(map[string]any)
		if !ok || int(e["code"].(float64)) != -32600 {
			t.Fatalf("line %d: expected -32600 for wrong-shape frame, got %q", i, line)
		}
		if strings.Contains(line, "rpcRequest") || strings.Contains(line, "Go value") {
			t.Fatalf("line %d: error message leaks Go type names: %s", i, line)
		}
	}
	var last map[string]any
	_ = json.Unmarshal([]byte(lines[3]), &last)
	if _, ok := last["result"]; !ok {
		t.Fatalf("session did not survive wrong-shape rejections: %q", lines[3])
	}
}

// W9 (2026-10-26 black-box audit): an explicit "id":null on a request is
// answered -32600 (JSON-RPC 2.0: a request id, when present, MUST NOT be
// null) instead of being silently treated as a notification — a buggy client
// waiting on the response used to hang forever. Absent-id notifications
// stay silent and normal ids keep being answered.
func TestStdioExplicitNullIDAnsweredNotDropped(t *testing.T) {
	t.Setenv("KERN_PRELOAD", "0") // hermetic: no background index build
	in := io.MultiReader(
		strings.NewReader(`{"jsonrpc":"2.0","id":null,"method":"ping"}`+"\n"),
		strings.NewReader(`{"jsonrpc":"2.0","id":null,"method":"notifications/initialized"}`+"\n"), // tolerated interop shape: silent
		strings.NewReader(`{"jsonrpc":"2.0","method":"notifications/initialized"}`+"\n"),           // absent id: silent
		strings.NewReader(`{"jsonrpc":"2.0","id":9,"method":"ping"}`+"\n"),
	)
	out := &bytes.Buffer{}
	s := NewServer(in, out)
	s.initialized.Store(true)
	if err := ServeStdio(s); err != nil {
		t.Fatalf("ServeStdio: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 responses (null-id error + id 9), got %d: %q", len(lines), out.String())
	}
	var r0 map[string]any
	_ = json.Unmarshal([]byte(lines[0]), &r0)
	e, ok := r0["error"].(map[string]any)
	if !ok || int(e["code"].(float64)) != -32600 {
		t.Fatalf("expected -32600 for explicit null id, got %q", lines[0])
	}
	if id, present := r0["id"]; present && id != nil {
		t.Fatalf("error response id for an invalid null id must be null, got %v", id)
	}
	var r1 map[string]any
	_ = json.Unmarshal([]byte(lines[1]), &r1)
	if r1["id"].(float64) != 9 {
		t.Fatalf("normal id must still be answered, got %q", lines[1])
	}
}

// F-3 + W13 (2026-10-26 black-box audit): a line over the 16 MiB cap is
// drained and answered -32600 "message too large" (the stdio twin of the HTTP
// 413 body cap — a naive client waiting on a response is told instead of
// hanging), and the session keeps serving.
func TestStdioOversizeLineSkipped(t *testing.T) {
	t.Setenv("KERN_PRELOAD", "0")
	huge := strings.Repeat("a", 16<<20+4096)
	in := io.MultiReader(
		strings.NewReader(huge+"\n"),
		strings.NewReader(huge+"\n"), // second oversized line: answered again
		strings.NewReader("{\"jsonrpc\":\"2.0\",\"id\":7,\"method\":\"ping\"}\n"),
	)
	out := &bytes.Buffer{}
	s := NewServer(in, out)
	s.initialized.Store(true)
	if err := ServeStdio(s); err != nil {
		t.Fatalf("ServeStdio: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 3 { // two too-large errors + the ping
		t.Fatalf("expected exactly 3 responses, got %d: %.80q", len(lines), out.String())
	}
	for i, line := range lines[:2] {
		var r map[string]any
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("line %d: unmarshal: %v", i, err)
		}
		e, ok := r["error"].(map[string]any)
		if !ok || int(e["code"].(float64)) != -32600 {
			t.Fatalf("line %d: expected -32600 message-too-large, got %q", i, line)
		}
	}
	var r map[string]any
	_ = json.Unmarshal([]byte(lines[2]), &r)
	if id, _ := r["id"].(float64); id != 7 {
		t.Fatalf("expected ping response id 7, got %q", lines[2])
	}
}

// W2 (2026-10-26 black-box audit): tools/list never paginates (the catalog
// fits one page), so a client-supplied cursor is always unknown — it is
// rejected -32602 instead of being silently ignored. nextCursor must be
// OMITTED when there is no next page: the MCP schema types it as an optional
// string, and a strict client (opencode 1.19.4) rejects an explicit null with
// "Failed to get tools" — the 2026-10-27 regression that made every kern tool
// disappear from the host.
func TestToolsListCursorRejectedAndNextCursorOmitted(t *testing.T) {
	t.Parallel()
	s := newHTTPServer()
	resp := s.dispatch(rpcRequest{ID: json.RawMessage(`1`), Method: "tools/list"})
	m, ok := resp.(map[string]any)
	if !ok {
		t.Fatalf("expected a result response, got %+v", resp)
	}
	res, _ := m["result"].(map[string]any)
	if res == nil {
		t.Fatalf("expected a result object, got %+v", resp)
	}
	if v, present := res["nextCursor"]; present {
		t.Fatalf("tools/list must omit nextCursor when there is no next page (strict clients reject null), got %v", v)
	}

	bad := s.dispatch(rpcRequest{ID: json.RawMessage(`2`), Method: "tools/list", Params: json.RawMessage(`{"cursor":"bogus"}`)})
	bm, ok := bad.(map[string]any)
	if !ok {
		t.Fatalf("expected an error response for bogus cursor, got %+v", bad)
	}
	e, _ := bm["error"].(map[string]any)
	var code int
	switch v := e["code"].(type) {
	case float64:
		code = int(v)
	case int:
		code = v
	}
	if e == nil || code != -32602 {
		t.Fatalf("expected -32602 for unknown cursor, got %+v", bad)
	}
}

// W5 (2026-10-26 black-box audit): every advertised tool carries the standard
// MCP annotations derived from its catalog risk metadata — readOnlyHint for
// low-risk (read-only) tools, destructiveHint for high, destructiveHint +
// openWorldHint for critical (command execution/deployment).
func TestToolsListAnnotationsDerivedFromRisk(t *testing.T) {
	t.Parallel()
	s := newHTTPServer()
	resp := s.dispatch(rpcRequest{ID: json.RawMessage(`1`), Method: "tools/list"})
	m, ok := resp.(map[string]any)
	if !ok {
		t.Fatalf("expected a result response, got %+v", resp)
	}
	res, _ := m["result"].(map[string]any)
	tools, _ := res["tools"].([]Tool)
	if len(tools) == 0 {
		t.Fatal("no tools advertised")
	}
	for _, tl := range tools {
		switch tl.RiskLevel {
		case "low":
			if !tl.Annotations["readOnlyHint"] {
				t.Errorf("%s (risk low): missing readOnlyHint annotation", tl.Name)
			}
		case "high":
			if !tl.Annotations["destructiveHint"] {
				t.Errorf("%s (risk high): missing destructiveHint annotation", tl.Name)
			}
		case "critical":
			if !tl.Annotations["destructiveHint"] || !tl.Annotations["openWorldHint"] {
				t.Errorf("%s (risk critical): missing destructiveHint/openWorldHint annotations", tl.Name)
			}
		}
	}
	// Spot-check the three classes are present in the default surface.
	byName := map[string]Tool{}
	for _, tl := range tools {
		byName[tl.Name] = tl
	}
	if tl := byName["kern_search"]; !tl.Annotations["readOnlyHint"] {
		t.Errorf("kern_search must carry readOnlyHint, got %+v", tl.Annotations)
	}
	if tl := byName["kern_apply"]; !tl.Annotations["destructiveHint"] {
		t.Errorf("kern_apply must carry destructiveHint, got %+v", tl.Annotations)
	}
	if tl := byName["kern_exec"]; !tl.Annotations["openWorldHint"] {
		t.Errorf("kern_exec must carry openWorldHint, got %+v", tl.Annotations)
	}
}

// W14 (2026-10-26 black-box audit): notifications/cancelled (and the legacy
// $/cancelRequest spelling) cancels the registered in-flight call for both
// the spec's requestId param shape and the JSON-RPC id shape. The audit's
// live probe could not observe this (its "slow" call finished in 0.3s before
// the cancel arrived) — this pins the actual wiring.
func TestNotificationsCancelledCancelsInflight(t *testing.T) {
	t.Parallel()
	for name, frame := range map[string]struct {
		method string
		params string
		key    string
	}{
		"spec requestId": {"notifications/cancelled", `{"requestId": 42}`, "42"},
		"legacy id":      {"$/cancelRequest", `{"id": 43}`, "43"},
		"spec id member": {"notifications/cancelled", `{"id": 44}`, "44"},
	} {
		s := newHTTPServer()
		fired := make(chan struct{})
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() {
			<-ctx.Done()
			close(fired)
		}()
		s.registerInflight(frame.key, cancel)
		resp := s.dispatch(rpcRequest{Method: frame.method, Params: json.RawMessage(frame.params)})
		if resp != nil {
			t.Fatalf("%s: notification must not be answered, got %+v", name, resp)
		}
		select {
		case <-fired:
		case <-time.After(2 * time.Second):
			t.Fatalf("%s: cancel was not propagated to the in-flight call", name)
		}
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
