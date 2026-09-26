package blueprint

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	bpmcp "github.com/JayveerPrajapati/kern/internal/bpcli/mcp"
)

// recordingBlueprintHandler is a stub ToolHandler that records the decoded
// arguments it receives, so tests can prove what runBlueprintHandler passes
// through (and that a confined payload never reaches the handler).
type recordingBlueprintHandler struct {
	got   map[string]any
	calls int
}

func (h *recordingBlueprintHandler) Name() string        { return "test_blueprint_stub" }
func (h *recordingBlueprintHandler) Description() string { return "recording stub" }
func (h *recordingBlueprintHandler) InputSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (h *recordingBlueprintHandler) Handle(_ context.Context, args json.RawMessage) bpmcp.ToolResult {
	h.calls++
	h.got = map[string]any{}
	if err := json.Unmarshal(args, &h.got); err != nil {
		return bpmcp.NewErrorResult("stub: bad args: " + err.Error())
	}
	return bpmcp.NewTextResult("ok")
}

// TestRunBlueprintHandlerConfinesDecodedFiles proves that a JSON-encoded
// `files` payload containing an absolute path outside the root is rejected
// before the handler runs, with the gate's error style and without
// disclosing the allowed roots.
func TestRunBlueprintHandlerConfinesDecodedFiles(t *testing.T) {
	root := t.TempDir()
	h := &recordingBlueprintHandler{}
	args := map[string]any{
		"root":  root,
		"files": `[{"path":"/etc/passwd","content":"root:x:0:0","op":"write"}]`,
	}
	_, err := runBlueprintHandler(h, args)
	if err == nil {
		t.Fatal("expected JSON-encoded files payload with an absolute path outside root to be rejected")
	}
	if h.calls != 0 {
		t.Fatalf("confined payload must not reach the handler; got %d calls", h.calls)
	}
	msg := err.Error()
	if !strings.Contains(msg, "path outside allowed roots") {
		t.Errorf("error should use the gate's wording, got: %s", msg)
	}
	if !strings.Contains(msg, `files[0].path="/etc/passwd"`) {
		t.Errorf("error should name the confined key and value, got: %s", msg)
	}
	// (c) the rejection must not disclose the allowed roots.
	if strings.Contains(msg, root) {
		t.Errorf("rejection error must not disclose allowed roots; contains %q: %s", root, msg)
	}
	if strings.Contains(msg, "(allowed") {
		t.Errorf("rejection error must not disclose allowed roots; got: %s", msg)
	}
}

// TestRunBlueprintHandlerPassesThroughInRootFiles proves that a valid
// in-root `files` payload is decoded, confined (passing), and delivered to
// the handler unchanged.
func TestRunBlueprintHandlerPassesThroughInRootFiles(t *testing.T) {
	root := t.TempDir()
	h := &recordingBlueprintHandler{}
	args := map[string]any{
		"root":  root,
		"files": `[{"path":"src/main.go","content":"package main","op":"write"}]`,
	}
	out, err := runBlueprintHandler(h, args)
	if err != nil {
		t.Fatalf("valid in-root files payload should pass through: %v", err)
	}
	if out != "ok" {
		t.Fatalf("unexpected handler output: %q", out)
	}
	if h.calls != 1 {
		t.Fatalf("handler should run exactly once for a valid payload; got %d calls", h.calls)
	}
	files, ok := h.got["files"].([]any)
	if !ok || len(files) != 1 {
		t.Fatalf("handler should receive the decoded files array, got %#v", h.got["files"])
	}
	m, ok := files[0].(map[string]any)
	if !ok || m["path"] != "src/main.go" || m["content"] != "package main" || m["op"] != "write" {
		t.Fatalf("decoded file entry does not match the payload: %#v", files[0])
	}
	if h.got["repo"] != root {
		t.Fatalf("handler should receive repo (renamed from root), got %#v", h.got["repo"])
	}
}

// TestRunBlueprintHandlerConfinesDecodedFinding proves the same confinement
// for the `finding` payload's path-bearing "file" field.
func TestRunBlueprintHandlerConfinesDecodedFinding(t *testing.T) {
	root := t.TempDir()
	h := &recordingBlueprintHandler{}
	args := map[string]any{
		"root":    root,
		"finding": `{"rule_id":"secrets:gitleaks","severity":"high","category":"secrets","file":"/etc/shadow","line":3,"message":"password"}`,
	}
	_, err := runBlueprintHandler(h, args)
	if err == nil {
		t.Fatal("expected JSON-encoded finding with a file outside root to be rejected")
	}
	if h.calls != 0 {
		t.Fatalf("confined finding must not reach the handler; got %d calls", h.calls)
	}
	msg := err.Error()
	if !strings.Contains(msg, "path outside allowed roots") || !strings.Contains(msg, `finding.file="/etc/shadow"`) {
		t.Errorf("error should name the confined finding.file field, got: %s", msg)
	}
	if strings.Contains(msg, root) || strings.Contains(msg, "(allowed") {
		t.Errorf("rejection error must not disclose allowed roots; got: %s", msg)
	}
}

// TestRunBlueprintHandlerPassesThroughInRootFinding proves an in-root
// finding payload reaches the handler unchanged.
func TestRunBlueprintHandlerPassesThroughInRootFinding(t *testing.T) {
	root := t.TempDir()
	h := &recordingBlueprintHandler{}
	args := map[string]any{
		"root":    root,
		"finding": `{"rule_id":"secrets:gitleaks","severity":"high","category":"secrets","file":"src/creds.go","line":3,"message":"token"}`,
	}
	out, err := runBlueprintHandler(h, args)
	if err != nil {
		t.Fatalf("valid in-root finding should pass through: %v", err)
	}
	if out != "ok" {
		t.Fatalf("unexpected handler output: %q", out)
	}
	f, ok := h.got["finding"].(map[string]any)
	if !ok || f["file"] != "src/creds.go" {
		t.Fatalf("decoded finding does not match the payload: %#v", h.got["finding"])
	}
}
