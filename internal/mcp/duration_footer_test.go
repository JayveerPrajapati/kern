package mcp

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

// Duration-visibility L1: a tool call crossing durationFooterThreshold gains
// a "[kern] took Xs" last line, and the audit chain entry for the call
// carries DurationMs (L3). The threshold is a var, lowered here so the test
// does not sleep real seconds.
func TestRunToolDurationFooter(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	old := durationFooterThreshold
	durationFooterThreshold = 5 * time.Millisecond
	defer func() { durationFooterThreshold = old }()

	dispatchTable["kern_test_dur"] = func(s *Server, ctx context.Context, id string, args map[string]any) (string, error) {
		time.Sleep(20 * time.Millisecond)
		return "slow done", nil
	}
	defer delete(dispatchTable, "kern_test_dur")

	buf := &bytes.Buffer{}
	s := NewServer(strings.NewReader(""), buf)
	out, err := s.runTool(context.Background(), "91", "", "kern_test_dur", map[string]any{})
	if err != nil {
		t.Fatalf("runTool: %v", err)
	}
	if !strings.HasPrefix(out, "slow done\n[kern] took ") || !strings.HasSuffix(out, "s") {
		t.Fatalf("out = %q, want slow done + duration footer line", out)
	}

	// L3: the tool_call audit entry for this call carries the duration.
	entries := s.toolAudit().All()
	if len(entries) == 0 {
		t.Fatal("no audit entries recorded")
	}
	last := entries[len(entries)-1]
	if last.Action != "tool_call" || last.Resource != "kern_test_dur" {
		t.Fatalf("unexpected last entry: %+v", last)
	}
	if last.DurationMs <= 0 {
		t.Fatalf("audit entry DurationMs = %d, want > 0 for a call above the footer threshold", last.DurationMs)
	}
}

// Fast calls stay byte-identical: no footer below the threshold.
func TestRunToolFastCallNoFooter(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	old := durationFooterThreshold
	durationFooterThreshold = 5 * time.Second
	defer func() { durationFooterThreshold = old }()

	dispatchTable["kern_test_durfast"] = func(s *Server, ctx context.Context, id string, args map[string]any) (string, error) {
		return "fast done", nil
	}
	defer delete(dispatchTable, "kern_test_durfast")

	buf := &bytes.Buffer{}
	s := NewServer(strings.NewReader(""), buf)
	out, err := s.runTool(context.Background(), "92", "", "kern_test_durfast", map[string]any{})
	if err != nil {
		t.Fatalf("runTool: %v", err)
	}
	if out != "fast done" {
		t.Fatalf("out = %q, want fast done unchanged (no footer below threshold)", out)
	}
}

// Error results never gain the footer: the error text is a contract surface.
func TestRunToolErrorNoFooter(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	old := durationFooterThreshold
	durationFooterThreshold = 1 * time.Nanosecond
	defer func() { durationFooterThreshold = old }()

	dispatchTable["kern_test_durerr"] = func(s *Server, ctx context.Context, id string, args map[string]any) (string, error) {
		time.Sleep(2 * time.Millisecond)
		return "", context.DeadlineExceeded
	}
	defer delete(dispatchTable, "kern_test_durerr")

	buf := &bytes.Buffer{}
	s := NewServer(strings.NewReader(""), buf)
	out, err := s.runTool(context.Background(), "93", "", "kern_test_durerr", map[string]any{})
	if err == nil {
		t.Fatalf("expected error, got out=%q", out)
	}
	if strings.Contains(out, "[kern] took") {
		t.Fatalf("error result gained a footer: %q", out)
	}
}
