package mcp

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// servedLedgerFixture builds a temp project root with one main.go and
// returns (root, path). The fixture is file-backed (kern_compact_file), so
// these tests never pay an index build.
func servedLedgerFixture(t *testing.T) (root, f string) {
	t.Helper()
	root = testRoot(t)
	f = filepath.Join(root, "main.go")
	if err := os.WriteFile(f, []byte("package main\n\nfunc foo() string { return \"hi\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root, f
}

// servedLedgerCtx returns a per-call scope-ful ctx, so the fresh-dispatch
// path mints and stamps scope.etag exactly like a real tools/call.
func servedLedgerCtx() (context.Context, *indexScope) {
	scope := &indexScope{}
	return context.WithValue(context.Background(), indexScopeKey{}, scope), scope
}

// TestServedLedgerRepeatServesPointer (B1): the same read-tool call with
// the same view-defining args, repeated within one session (one Server), is
// answered with the one-line pointer carrying the original call's sequence
// number and etag — never the payload again.
func TestServedLedgerRepeatServesPointer(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root, f := servedLedgerFixture(t)
	s := NewServerForRoot(strings.NewReader(""), io.Discard, root)
	defer s.Close()
	ctx, scope := servedLedgerCtx()
	args := map[string]any{"root": root, "path": f}

	out1, err := s.runTool(ctx, "1", "", "kern_compact_file", args)
	if err != nil {
		t.Fatalf("first call failed: %v", err)
	}
	if !strings.Contains(out1, "foo") {
		t.Fatalf("expected the full summary on the first call, got %q", out1)
	}
	e1 := scope.etag
	if e1 == "" {
		t.Fatal("an eligible fresh dispatch must mint an etag on the scope")
	}

	out2, err := s.runTool(ctx, "2", "", "kern_compact_file", args)
	if err != nil {
		t.Fatalf("second call failed: %v", err)
	}
	want := "already served this session (call #1, etag " + e1 + ") — re-send with that etag to confirm unchanged, or vary the view (tier/lines/max_tokens), or no_cache=1 to re-emit"
	if out2 != want {
		t.Fatalf("second call must return the pointer line, got %q", out2)
	}
}

// TestServedLedgerChangedViewServesFullPayload (F-2 mirror): a different
// serve view (max_output) is different content — the ledger key keeps
// max_output (view-bound, like the D1 key), so a view change serves the
// full payload, and only a repeat of THAT view is answered with a pointer.
func TestServedLedgerChangedViewServesFullPayload(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root, f := servedLedgerFixture(t)
	s := NewServerForRoot(strings.NewReader(""), io.Discard, root)
	defer s.Close()
	ctx, scope := servedLedgerCtx()

	out1, err := s.runTool(ctx, "1", "", "kern_compact_file", map[string]any{"root": root, "path": f})
	if err != nil {
		t.Fatalf("first call failed: %v", err)
	}
	if !strings.Contains(out1, "foo") {
		t.Fatalf("expected the full summary, got %q", out1)
	}

	out2, err := s.runTool(ctx, "2", "", "kern_compact_file", map[string]any{"root": root, "path": f, "max_output": "400"})
	if err != nil {
		t.Fatalf("changed-view call failed: %v", err)
	}
	if strings.HasPrefix(out2, "already served this session") {
		t.Fatalf("a different serve view must NOT be answered with the pointer, got %q", out2)
	}
	if !strings.Contains(out2, "foo") {
		t.Fatalf("a different serve view must serve the full payload, got %q", out2)
	}
	e2 := scope.etag
	if e2 == "" {
		t.Fatal("the changed-view dispatch must mint its own etag")
	}

	out3, err := s.runTool(ctx, "3", "", "kern_compact_file", map[string]any{"root": root, "path": f, "max_output": "400"})
	if err != nil {
		t.Fatalf("third call failed: %v", err)
	}
	want := "already served this session (call #2, etag " + e2 + ") — re-send with that etag to confirm unchanged, or vary the view (tier/lines/max_tokens), or no_cache=1 to re-emit"
	if out3 != want {
		t.Fatalf("a repeat of the changed view must return the pointer, got %q", out3)
	}
}

// TestServedLedgerDisabledEnvServesFullPayload: KERN_SERVED_LEDGER=0 turns
// the ledger off — identical repeats keep serving the full payload.
func TestServedLedgerDisabledEnvServesFullPayload(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("KERN_SERVED_LEDGER", "0")
	root, f := servedLedgerFixture(t)
	s := NewServerForRoot(strings.NewReader(""), io.Discard, root)
	defer s.Close()
	ctx, _ := servedLedgerCtx()
	args := map[string]any{"root": root, "path": f}

	for i := 1; i <= 2; i++ {
		out, err := s.runTool(ctx, "1", "", "kern_compact_file", args)
		if err != nil {
			t.Fatalf("call %d failed: %v", i, err)
		}
		if strings.HasPrefix(out, "already served this session") {
			t.Fatalf("disabled ledger must serve the full payload, got %q", out)
		}
		if !strings.Contains(out, "foo") {
			t.Fatalf("expected the full summary, got %q", out)
		}
	}
}

// TestServedLedgerNoCacheBypass: no_cache=1 bypasses the ledger for both
// lookup (fresh serve) and record (the fresh serve is not re-keyed), exactly
// like the D1 cache — while an earlier plain serve is still remembered.
func TestServedLedgerNoCacheBypass(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root, f := servedLedgerFixture(t)
	s := NewServerForRoot(strings.NewReader(""), io.Discard, root)
	defer s.Close()
	ctx, _ := servedLedgerCtx()
	args := map[string]any{"root": root, "path": f}

	out1, err := s.runTool(ctx, "1", "", "kern_compact_file", args)
	if err != nil {
		t.Fatalf("first call failed: %v", err)
	}
	if !strings.Contains(out1, "foo") {
		t.Fatalf("expected the full summary, got %q", out1)
	}

	out2, err := s.runTool(ctx, "2", "", "kern_compact_file", map[string]any{"root": root, "path": f, "no_cache": "1"})
	if err != nil {
		t.Fatalf("no_cache call failed: %v", err)
	}
	if strings.HasPrefix(out2, "already served this session") {
		t.Fatalf("no_cache=1 must bypass the ledger and serve fresh, got %q", out2)
	}
	if !strings.Contains(out2, "foo") {
		t.Fatalf("no_cache=1 must serve the full payload, got %q", out2)
	}

	// The plain serve from call 1 is still recorded: a normal repeat is
	// answered with the pointer.
	out3, err := s.runTool(ctx, "3", "", "kern_compact_file", args)
	if err != nil {
		t.Fatalf("third call failed: %v", err)
	}
	if !strings.HasPrefix(out3, "already served this session (call #1") {
		t.Fatalf("a plain repeat after a no_cache serve must still hit the ledger, got %q", out3)
	}
}

// TestServedLedgerEtagCallUsesConditionalFetchPath: the pointer teaches the
// caller to "re-send with etag E to confirm unchanged" — so a call that
// actually carries the etag must be answered by the conditional-fetch path
// (the tiny "unchanged" reply), never intercepted by the ledger.
func TestServedLedgerEtagCallUsesConditionalFetchPath(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root, f := servedLedgerFixture(t)
	s := NewServerForRoot(strings.NewReader(""), io.Discard, root)
	defer s.Close()

	r1, err := s.CallToolGovernedResult(context.Background(), "kern_compact_file", map[string]any{"root": root, "path": f})
	if err != nil {
		t.Fatalf("first governed result call failed: %v", err)
	}
	if r1.Etag == "" || !strings.Contains(r1.Output, "foo") {
		t.Fatalf("fresh serve: want full text + etag, got %+v", r1)
	}

	r2, err := s.CallToolGovernedResult(context.Background(), "kern_compact_file", map[string]any{"root": root, "path": f, "etag": r1.Etag})
	if err != nil {
		t.Fatalf("etag confirm call failed: %v", err)
	}
	if !r2.Unchanged || !strings.HasPrefix(r2.Output, "unchanged (etag ") {
		t.Fatalf("an etag-carrying repeat must use the conditional-fetch path, got %+v (output %.80q)", r2, r2.Output)
	}
	if strings.HasPrefix(r2.Output, "already served this session") {
		t.Fatalf("the ledger must never intercept an etag-carrying call, got %q", r2.Output)
	}
}

// TestServedLedgerFileEditServesFullPayload: the ledger key is content-aware
// (file fingerprint for file-backed tools) — after the served file changes,
// an identical repeat must serve the FRESH full payload, never a stale
// "already served" pointer.
func TestServedLedgerFileEditServesFullPayload(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root, f := servedLedgerFixture(t)
	s := NewServerForRoot(strings.NewReader(""), io.Discard, root)
	defer s.Close()
	ctx, _ := servedLedgerCtx()
	args := map[string]any{"root": root, "path": f}

	out1, err := s.runTool(ctx, "1", "", "kern_compact_file", args)
	if err != nil {
		t.Fatalf("first call failed: %v", err)
	}
	if !strings.Contains(out1, "foo") {
		t.Fatalf("expected the full summary, got %q", out1)
	}

	// Edit the file with a DIFFERENT size so the fingerprint rotates even on
	// coarse-mtime filesystems.
	if err := os.WriteFile(f, []byte("package main\n\nfunc bar() string { return \"bye\" }\nvar extra = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out2, err := s.runTool(ctx, "2", "", "kern_compact_file", args)
	if err != nil {
		t.Fatalf("post-edit call failed: %v", err)
	}
	if strings.HasPrefix(out2, "already served this session") {
		t.Fatalf("a changed file must NOT be answered with the stale pointer, got %q", out2)
	}
	if !strings.Contains(out2, "bar") {
		t.Fatalf("a changed file must serve the fresh payload, got %q", out2)
	}
}

// TestServedLedgerSessionsAreIsolated: the ledger is per-Server (per MCP
// session) — a fresh Server does not see an earlier Server's serves, so the
// same args across sessions serve the full payload again.
func TestServedLedgerSessionsAreIsolated(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root, f := servedLedgerFixture(t)
	args := map[string]any{"root": root, "path": f}

	s1 := NewServerForRoot(strings.NewReader(""), io.Discard, root)
	ctx1, _ := servedLedgerCtx()
	out1, err := s1.runTool(ctx1, "1", "", "kern_compact_file", args)
	if err != nil {
		t.Fatalf("first session call failed: %v", err)
	}
	if !strings.Contains(out1, "foo") {
		t.Fatalf("expected the full summary, got %q", out1)
	}
	s1.Close()

	// A NEW Server is a NEW session: the same args serve the full payload,
	// never the previous session's pointer.
	s2 := NewServerForRoot(strings.NewReader(""), io.Discard, root)
	defer s2.Close()
	ctx2, _ := servedLedgerCtx()
	out2, err := s2.runTool(ctx2, "2", "", "kern_compact_file", args)
	if err != nil {
		t.Fatalf("second session call failed: %v", err)
	}
	if strings.HasPrefix(out2, "already served this session") {
		t.Fatalf("a new session must not see the previous session's serves, got %q", out2)
	}
	if !strings.Contains(out2, "foo") {
		t.Fatalf("a new session must serve the full payload, got %q", out2)
	}
}
