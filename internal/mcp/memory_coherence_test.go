package mcp

import (
	"context"
	"strings"
	"testing"
)

// TestMemoryReadYourWritesSameSession pins LOW-9's verify cell: `add` →
// `recall` in ONE session returns the entry — including under KERN_READONLY=1
// (the deliberate memory write still persists; recall sees it).
//
// Root cause note (2026-10-25): the original finding's repro piped the two
// requests without waiting for the add's response; the server dispatches
// tools/call concurrently, so the racing recall could execute before the
// add's disk write landed. That is dispatch-order causality, not
// readonly-specific staleness — a protocol-compliant client (waits for the
// add's response, the 100 ms live probe suffices) deterministically sees the
// write, in both readonly and normal modes. This test pins that guarantee.
func TestMemoryReadYourWritesSameSession(t *testing.T) {
	t.Setenv("KERN_READONLY", "1")
	root := t.TempDir()
	s := newServerCore("stdio")
	if _, err := s.handleMemory(context.Background(), map[string]any{
		"action": "add", "lesson": "low9 pin: always run make test-touched before committing",
		"root": root,
	}); err != nil {
		t.Fatalf("add: %v", err)
	}
	res, err := s.handleMemory(context.Background(), map[string]any{
		"action": "recall", "prompt": "make test-touched", "root": root,
	})
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if !strings.Contains(res, "low9 pin") {
		t.Fatalf("same-session recall missed the just-added lesson; recall = %q", res)
	}
}
