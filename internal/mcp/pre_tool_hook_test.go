package mcp

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPreToolHook exercises the optional pre-tool-use hook. It must be
// purely additive: a nil hook (the default) leaves tools/call untouched,
// and a set hook can deny a call before any side effect runs.
func TestPreToolHook(t *testing.T) {
	callReq := writeReq("tools/call", 1, `{"name":"kern_compact_file","arguments":{"path":"server.go"}}`)

	t.Run("nil_hook_is_noop", func(t *testing.T) {
		in := strings.NewReader(callReq + "\n")
		buf := &bytes.Buffer{}
		s := NewServer(in, buf)
		s.roots = []string{"/"}
		if err := s.Serve(); err != nil {
			t.Fatalf("Serve: %v", err)
		}
		resp := decodeOne(t, buf.String())
		_, isErr := toolResultText(t, resp)
		if isErr {
			t.Fatal("default (nil hook) run reported an error")
		}
	})

	t.Run("deny_blocks_before_execution", func(t *testing.T) {
		in := strings.NewReader(callReq + "\n")
		buf := &bytes.Buffer{}
		var sawName string
		var sawArgs map[string]any
		s := NewServer(in, buf).WithPreToolHook(func(name string, args map[string]any) error {
			sawName = name
			sawArgs = args
			return &hookError{msg: "tool not allowed by policy"}
		})
		s.roots = []string{"/"}
		if err := s.Serve(); err != nil {
			t.Fatalf("Serve: %v", err)
		}
		resp := decodeOne(t, buf.String())
		text, isErr := toolResultText(t, resp)
		if !isErr {
			t.Fatal("denied call did not report isError=true")
		}
		if !strings.Contains(text, "pre-tool-use denied") || !strings.Contains(text, "not allowed by policy") {
			t.Fatalf("denial message missing: %q", text)
		}
		if sawName != "kern_compact_file" {
			t.Fatalf("hook saw name %q", sawName)
		}
		if sawArgs["path"] != "server.go" {
			t.Fatalf("hook saw args %v", sawArgs)
		}
	})

	t.Run("allow_runs_normally", func(t *testing.T) {
		in := strings.NewReader(callReq + "\n")
		buf := &bytes.Buffer{}
		s := NewServer(in, buf).WithPreToolHook(func(name string, args map[string]any) error {
			return nil
		})
		s.roots = []string{"/"}
		if err := s.Serve(); err != nil {
			t.Fatalf("Serve: %v", err)
		}
		resp := decodeOne(t, buf.String())
		text, isErr := toolResultText(t, resp)
		if isErr {
			t.Fatalf("allowed call errored: %q", text)
		}
	})
}

type hookError struct{ msg string }

func (e *hookError) Error() string { return e.msg }

// TestPreToolHook_GateAttachedByDefault verifies the production construction
// path (NewServer) wires the KERN_MCP_ROOTS confinement gate as the default
// pre-tool-use hook: a path-typed argument outside the roots is denied
// (isError=true) before its handler runs, KERN_MCP_NO_CONFINE=1 opts out, and
// zero-config fails closed to the process cwd.
func TestPreToolHook_GateAttachedByDefault(t *testing.T) {
	rootDir := t.TempDir()
	outside := t.TempDir()
	// A real file outside the roots: the handler (kern_compact_file) would
	// succeed on it without the gate, so a denial proves the call never
	// reached the handler.
	if err := os.WriteFile(filepath.Join(outside, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	outsidePath := filepath.Join(outside, "a.go")

	t.Run("gate_attached_by_default", func(t *testing.T) {
		s := NewServer(strings.NewReader(""), &bytes.Buffer{})
		if s.preTool == nil {
			t.Fatal("NewServer must attach the confinement gate as the default pre-tool-use hook")
		}
	})

	t.Run("outside_path_denied_before_handler", func(t *testing.T) {
		t.Setenv("KERN_MCP_ROOTS", rootDir)
		args, err := json.Marshal(map[string]any{"path": outsidePath})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		req := writeReq("tools/call", 1, `{"name":"kern_compact_file","arguments":`+string(args)+`}`)
		in := strings.NewReader(req + "\n")
		buf := &bytes.Buffer{}
		s := NewServer(in, buf)
		if s.preTool == nil {
			t.Fatal("gate must be attached as the default pre-tool-use hook")
		}
		if err := s.Serve(); err != nil {
			t.Fatalf("Serve: %v", err)
		}
		resp := decodeOne(t, buf.String())
		text, isErr := toolResultText(t, resp)
		if !isErr {
			t.Fatalf("outside path was not denied (handler ran): %s", text)
		}
		if !strings.Contains(text, "pre-tool-use denied") || !strings.Contains(text, "outside allowed roots") {
			t.Fatalf("denial message missing: %q", text)
		}
	})

	t.Run("no_confine_opts_out", func(t *testing.T) {
		t.Setenv("KERN_MCP_ROOTS", rootDir)
		t.Setenv("KERN_MCP_NO_CONFINE", "1")
		s := NewServer(strings.NewReader(""), &bytes.Buffer{})
		if s.preTool != nil {
			t.Fatal("KERN_MCP_NO_CONFINE=1 must not attach the gate as the pre-tool-use hook")
		}
		if s.gate != nil {
			t.Fatal("KERN_MCP_NO_CONFINE=1 must disable the gate entirely (opt-out)")
		}
	})

	t.Run("zero_config_fails_closed_to_cwd", func(t *testing.T) {
		t.Setenv("KERN_MCP_ROOTS", "")
		t.Setenv("KERN_MCP_NO_CONFINE", "")
		// A relative path escaping the cwd: with no KERN_MCP_ROOTS the gate
		// fails closed to the process cwd, so the escaping path is denied
		// before the handler runs.
		cwd, err := os.Getwd()
		if err != nil {
			t.Fatalf("getwd: %v", err)
		}
		esc, err := filepath.Rel(cwd, outsidePath)
		if err != nil {
			t.Fatalf("rel: %v", err)
		}
		args, err := json.Marshal(map[string]any{"path": esc})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		req := writeReq("tools/call", 2, `{"name":"kern_compact_file","arguments":`+string(args)+`}`)
		in := strings.NewReader(req + "\n")
		buf := &bytes.Buffer{}
		s := NewServer(in, buf)
		if s.preTool == nil {
			t.Fatal("gate must still be attached (cwd-confined) as the default hook in zero-config")
		}
		if s.gate == nil || !s.gate.enabled {
			t.Fatal("zero-config gate must be enabled and confined to the cwd")
		}
		if err := s.Serve(); err != nil {
			t.Fatalf("Serve: %v", err)
		}
		resp := decodeOne(t, buf.String())
		text, isErr := toolResultText(t, resp)
		if !isErr {
			t.Fatalf("zero-config run must deny paths escaping the cwd: %s", text)
		}
		if !strings.Contains(text, "outside allowed roots") {
			t.Fatalf("denial message missing: %q", text)
		}
		// A path inside the cwd still runs normally.
		args, err = json.Marshal(map[string]any{"path": "server.go"})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		req = writeReq("tools/call", 3, `{"name":"kern_compact_file","arguments":`+string(args)+`}`)
		in = strings.NewReader(req + "\n")
		buf = &bytes.Buffer{}
		s = NewServer(in, buf)
		if err := s.Serve(); err != nil {
			t.Fatalf("Serve: %v", err)
		}
		resp = decodeOne(t, buf.String())
		text, isErr = toolResultText(t, resp)
		if isErr {
			t.Fatalf("cwd-relative path must run in zero-config: %s", text)
		}
		if text == "" {
			t.Fatal("cwd-relative path produced an empty result")
		}
	})
}

func decodeOne(t *testing.T, out string) map[string]any {
	t.Helper()
	var resp map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &resp); err != nil {
		t.Fatalf("unmarshal response %q: %v", out, err)
	}
	return resp
}
