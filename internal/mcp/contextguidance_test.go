package mcp

import (
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/servedledger"
)

func TestAppendContextGuidanceReadyAndFirstSlice(t *testing.T) {
	s := &Server{ledger: servedledger.New()}
	s.ledger.Record("default", "kern_explore", "{}", "", []string{"internal/a/a.go"})
	got := s.appendContextGuidance(nil, "internal/a/a.go:1\ninternal/b/b.go:2\n")
	for _, want := range []string{
		"## Context sufficiency",
		"ready-to-execute: true",
		"covered: 2/2 referenced files (1 previously served)",
		"missing: none",
		"## First slice",
		"start with: internal/a/a.go",
		"verify with: go test ./internal/a",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("guidance missing %q:\n%s", want, got)
		}
	}
}

func TestAppendContextGuidanceUnknownWithoutFiles(t *testing.T) {
	s := &Server{ledger: servedledger.New()}
	got := s.appendContextGuidance(nil, "task: example\nnext: plan\n")
	for _, want := range []string{
		"ready-to-execute: unknown",
		"missing: no affected-file references in this output",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("guidance missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "## First slice") {
		t.Errorf("fileless output must not invent a first slice:\n%s", got)
	}
}

func TestAppendContextGuidanceSkipsJSON(t *testing.T) {
	s := &Server{ledger: servedledger.New()}
	in := `{"content":"internal/a/a.go:1"}`
	got := s.appendContextGuidance(nil, in)
	if !strings.Contains(got, "## Context sufficiency") || !strings.Contains(got, "start with: internal/a/a.go") {
		t.Fatalf("JSON envelope must carry guidance in content:\n%s", got)
	}
}
