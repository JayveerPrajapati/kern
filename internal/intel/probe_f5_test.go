package intel

import (
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

const srcExecApproval = `package gov

func CheckExecCommand(cmd string) error { return nil }

func execRejectedFor() error { return nil }

func execRejectionTTL() int { return 300 }

func execRejectionTTLText() string { return "" }

type approval struct{ id string }

func (a approval) Decide() bool { return true }
`

// TestProbeProseKeywordRecovery pins F5 (QA 2026-10-19 campaign): the F4
// recovery only reaches camelCase-shaped tokens, so an all-prose task like
// "exec approval TTL bug" anchored just the `approval` struct while the
// exec/TTL machinery (CheckExecCommand, execRejectionTTL, ...) stayed
// unfound. The stopword-filtered keyword pass must recover them: a symbol
// whose name covers multiple task keywords anchors (precise identification),
// and one single-keyword hub anchors too. Prose-matched anchors are labeled
// Fuzzy (low confidence) even when they are real symbols.
func TestProbeProseKeywordRecovery(t *testing.T) {
	dir := writeTree(t, map[string]string{"gov/gov.go": srcExecApproval})
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	r := Probe(ix, "exec approval TTL bug", 4000)
	got := map[string]bool{}
	fuzzy := map[string]bool{}
	for _, a := range r.Anchors {
		got[a.Resolved] = true
		fuzzy[a.Resolved] = a.Fuzzy
	}
	// `approval` resolves exactly (not fuzzy); the multi-keyword exec/TTL
	// symbols anchor as fuzzy identifications.
	if !got["approval"] {
		t.Fatalf("exact token `approval` must still anchor; anchors: %v", got)
	}
	if fuzzy["approval"] {
		t.Fatalf("exact resolution must not be labeled fuzzy; anchors: %v", fuzzy)
	}
	for _, want := range []string{"CheckExecCommand", "execRejectionTTL", "execRejectionTTLText"} {
		if !got[want] {
			t.Fatalf("prose keyword pass did not recover %q; anchors: %v", want, got)
		}
		if !fuzzy[want] {
			t.Fatalf("recovered symbol %q must be labeled fuzzy (low confidence)", want)
		}
	}
}

// TestProbeProseKeywordRankedNotScanOrder pins the ranking contract: a
// multi-keyword match (execRejectionTTLText covers "exec" and "ttl") anchors
// even when a scan-order-first single-keyword symbol would otherwise fill
// the cut, and among single-keyword matches the most-connected symbol wins
// (CheckExecCommand is called by execRejectedFor and execRejectionTTLText;
// an incidental substring match with no callers loses).
func TestProbeProseKeywordRankedNotScanOrder(t *testing.T) {
	dir := writeTree(t, map[string]string{"gov/gov.go": srcExecApproval})
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	r := Probe(ix, "exec TTL bug", 4000)
	got := map[string]bool{}
	for _, a := range r.Anchors {
		got[a.Resolved] = true
	}
	// Multi-keyword coverage must anchor all three exec/TTL symbols.
	for _, want := range []string{"execRejectionTTL", "execRejectionTTLText"} {
		if !got[want] {
			t.Fatalf("multi-keyword symbol %q must anchor; anchors: %v", want, got)
		}
	}
	// The single-keyword "exec" match is the hub CheckExecCommand (2
	// callers), not the caller-less lone-substring alternative.
	if !got["CheckExecCommand"] {
		t.Fatalf("single-keyword hub CheckExecCommand must anchor; anchors: %v", got)
	}
	// No task keyword matches unrelated prose-only anchors: the exact match
	// count stays small (the 12-cap is not gamed by noise).
	if len(r.Anchors) > 6 {
		t.Fatalf("prose keyword pass must stay tight, got %d anchors: %v", len(r.Anchors), got)
	}
}
