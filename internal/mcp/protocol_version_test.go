package mcp

import (
	"encoding/json"
	"net/http"
	"testing"
)

// TestInitializeNegotiatesCurrentProtocolVersion pins the opencode 1.19.4
// regression: that client advertises ONLY 2025-11-25 and treats a 2025-06-18
// answer as a failed session ("Failed to get tools") even though the tool
// surface is identical. The server must echo every supported revision.
func TestInitializeNegotiatesCurrentProtocolVersion(t *testing.T) {
	t.Parallel()
	for _, want := range []string{"2024-11-05", "2025-03-26", "2025-06-18", "2025-11-25"} {
		body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"` + want + `","capabilities":{},"clientInfo":{"name":"c","version":"1"}}}`
		rr := doHTTP(t, newHTTPServer(), http.MethodPost, "application/json", body, nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("%s: expected 200, got %d: %s", want, rr.Code, rr.Body.String())
		}
		var resp map[string]any
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatalf("%s: unmarshal: %v", want, err)
		}
		res, _ := resp["result"].(map[string]any)
		if got, _ := res["protocolVersion"].(string); got != want {
			t.Fatalf("negotiated %q, want %q (a supported client version must be echoed verbatim)", got, want)
		}
	}
}

// TestInitializeUnknownProtocolFallsBackToDefault keeps the boundary honest:
// an unknown version is not echoed; the server reports what it implements.
func TestInitializeUnknownProtocolFallsBackToDefault(t *testing.T) {
	t.Parallel()
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"1999-01-01","capabilities":{},"clientInfo":{"name":"c","version":"1"}}}`
	rr := doHTTP(t, newHTTPServer(), http.MethodPost, "application/json", body, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	res, _ := resp["result"].(map[string]any)
	if got, _ := res["protocolVersion"].(string); got != protocolVersion {
		t.Fatalf("unknown version negotiated %q, want the default %q", got, protocolVersion)
	}
}
