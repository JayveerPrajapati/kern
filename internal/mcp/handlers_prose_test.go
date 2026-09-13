package mcp

import (
	"context"
	"io"
	"strings"
	"testing"
)

// TestHandleProse drives kern_prose against the provenanceProject fixture
// (symbols Greet, PublicA, SecretB): prose words resolve to candidate symbols
// with the "<symbol> (<N> words matched)" line format; misses render "no
// prose matches: <query>"; a missing query is rejected. Mirrors the
// handler-test pattern in handlers_graph_test.go.
func TestHandleProse(t *testing.T) {
	root := provenanceProject(t)
	s := NewServer(strings.NewReader(""), io.Discard)
	defer s.Close()

	// A prose word from a symbol name.
	out, err := s.handleProse(context.Background(), map[string]any{"root": root, "query": "greet"})
	if err != nil {
		t.Fatalf("handleProse(greet): %v", err)
	}
	if out != "Greet (1 words matched)" {
		t.Errorf("handleProse(greet) = %q, want %q", out, "Greet (1 words matched)")
	}

	// A prose word derived from a package directory.
	out, err = s.handleProse(context.Background(), map[string]any{"root": root, "query": "secret"})
	if err != nil {
		t.Fatalf("handleProse(secret): %v", err)
	}
	if out != "SecretB (1 words matched)" {
		t.Errorf("handleProse(secret) = %q, want %q", out, "SecretB (1 words matched)")
	}

	// Multi-word queries rank hits by matched-word count.
	out, err = s.handleProse(context.Background(), map[string]any{"root": root, "query": "secret length"})
	if err != nil {
		t.Fatalf("handleProse(secret length): %v", err)
	}
	if !strings.HasPrefix(out, "SecretB (1 words matched)") {
		t.Errorf("handleProse(secret length) = %q, want SecretB first", out)
	}

	// A miss renders the miss line.
	out, err = s.handleProse(context.Background(), map[string]any{"root": root, "query": "bogusxyz"})
	if err != nil {
		t.Fatalf("handleProse(bogusxyz): %v", err)
	}
	if out != "no prose matches: bogusxyz" {
		t.Errorf("handleProse(bogusxyz) = %q, want miss line", out)
	}

	// Missing query is rejected.
	if _, err := s.handleProse(context.Background(), map[string]any{"root": root}); err == nil || !strings.Contains(err.Error(), "query is required") {
		t.Errorf("handleProse without query: err = %v, want 'query is required'", err)
	}
}
