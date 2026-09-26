package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// toolCallEnv pins the governance environment the route's in-process tool
// server reads at construction: no bearer gate, no allowlist, no
// permissive/no-confine escapes, and no rate-limit surprises. Must run before
// newTestApp (the App is built inside New).
func toolCallEnv(t *testing.T) {
	t.Helper()
	for _, v := range []string{
		"KERN_AUTH_TOKEN", "KERN_TOOLS", "KERN_MCP_ROOTS", "KERN_MCP_PERMISSIVE",
		"KERN_MCP_NO_CONFINE", "KERN_RBAC_DEFAULT_DENY", "KERN_MCP_FULL",
		"KERN_MCP_HIGH_LEVEL_ONLY", "KERN_MCP_SINGLE_TOOL", "KERN_MCP_PHASE",
		"KERN_MCP_CATEGORY",
	} {
		t.Setenv(v, "")
	}
}

// fakeToolServer is a test double for ToolServer. internal/web cannot import
// internal/mcp (the mcp → org → enterprise → web import cycle), and the real
// governed dispatch is exercised by the internal/sdk catalog tests over the
// same CallToolGoverned path; this fake lets the route test assert the
// contract (200 shape, error → status mapping) with the typed sentinel
// errors the real server returns (finding 6: the route maps with errors.Is,
// never by matching raw error text).
type fakeToolServer struct {
	out   string
	err   error
	calls []string // tool names received, in order
}

func (f *fakeToolServer) CallToolGoverned(_ context.Context, name string, args map[string]any) (string, error) {
	f.calls = append(f.calls, name)
	if f.err != nil {
		return "", f.err
	}
	return f.out, nil
}

func (f *fakeToolServer) Close() {}

// TestV1ToolCallPassthrough covers the single REST passthrough route
// (POST /v1/tools/{name}): a successful read-only call returns 200 with the
// raw tool output, a governed denial (domain.ErrToolDenied) is 403, an
// unknown tool (domain.ErrToolUnknown) is 404, and an unexpected error is a
// GENERIC 500 whose detail is logged, never returned (finding 6).
func TestV1ToolCallPassthrough(t *testing.T) {
	toolCallEnv(t)
	a := newTestApp(t)
	defer func() { _ = a.Close() }()

	fake := &fakeToolServer{out: "masked 1 secrets: [kern] masked 1 secrets\n"}
	a.tools = fake

	rec := postJSON(t, a, "/v1/tools/kern_mask_pii", `{"text":"token=sk-abc123def"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /v1/tools/kern_mask_pii = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	var ok struct {
		Output string `json:"output"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &ok); err != nil {
		t.Fatalf("decode 200 body: %v", err)
	}
	if ok.Output == "" || !strings.Contains(ok.Output, "masked") {
		t.Errorf("output = %q, want the raw tool output", ok.Output)
	}
	if len(fake.calls) != 1 || fake.calls[0] != "kern_mask_pii" {
		t.Errorf("delegated tool = %v, want [kern_mask_pii]", fake.calls)
	}

	// Gated/refused: the governed path classifies every pre-execution denial
	// as domain.ErrToolDenied; the route surfaces it as 403, never a 200.
	fake.out = ""
	fake.err = domain.ErrToolDenied
	rec = postJSON(t, a, "/v1/tools/kern_mask_pii", `{"text":"x","root":"/etc"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("out-of-confinement root = %d, want 403 (body %s)", rec.Code, rec.Body.String())
	}

	// Unknown tool → 404, named safely without raw error detail.
	fake.err = domain.ErrToolUnknown
	rec = postJSON(t, a, "/v1/tools/kern_no_such_tool", `{}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown tool = %d, want 404 (body %s)", rec.Code, rec.Body.String())
	}

	// Unexpected error → generic 500: the detail (which may disclose internal
	// paths/policy) is logged server-side, never returned to the client.
	fake.err = errors.New("boom: /etc/secret-config leaked")
	rec = postJSON(t, a, "/v1/tools/kern_mask_pii", `{}`)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("unexpected error = %d, want 500 (body %s)", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "/etc/secret-config") || strings.Contains(rec.Body.String(), "boom") {
		t.Errorf("500 body must not leak the raw error detail, got: %s", rec.Body.String())
	}

	// Method gate: GET is refused like every other POST-only /v1 route.
	req := httptest.NewRequest(http.MethodGet, "/v1/tools/kern_mask_pii", nil)
	rrec := httptest.NewRecorder()
	a.ServeHTTP(rrec, req)
	if rrec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET = %d, want 405", rrec.Code)
	}
}
