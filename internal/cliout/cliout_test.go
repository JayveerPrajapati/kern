package cliout

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// TestClipStringsTruncatesAbsurdFields pins the 512 KiB safety guard: an
// oversized string field is truncated with the visible marker and the
// payload stays valid JSON (ported from graphcli's helpers_test.go, where it
// pinned the pre-extraction local copy of the same machinery).
func TestClipStringsTruncatesAbsurdFields(t *testing.T) {
	huge := strings.Repeat("x", maxJSONFieldLen+4096)
	v := struct {
		Note  string `json:"note"`
		Small string `json:"small"`
	}{Note: huge, Small: "ok"}

	var buf bytes.Buffer
	if err := Print(&buf, v); err != nil {
		t.Fatalf("Print: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "kern: field truncated at 512 KiB safety cap") {
		t.Fatalf("output missing truncation marker:\n%.200s…", out)
	}
	if len(out) >= maxJSONFieldLen+4096 {
		t.Fatalf("output not truncated: %d bytes", len(out))
	}
	if !strings.Contains(out, `"small": "ok"`) {
		t.Fatalf("small field lost:\n%s", out)
	}
}

// TestClipStringsPreservesTime pins the json.Marshaler guard: time.Time (all
// unexported fields) must round-trip with its RFC 3339 rendering intact, not
// be zeroed by reflection (ported from graphcli's helpers_test.go).
func TestClipStringsPreservesTime(t *testing.T) {
	ts := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	v := struct {
		At time.Time `json:"at"`
	}{At: ts}

	var buf bytes.Buffer
	if err := Print(&buf, v); err != nil {
		t.Fatalf("Print: %v", err)
	}
	if !strings.Contains(buf.String(), "2026-10-15T12:00:00Z") {
		t.Fatalf("time.Time not preserved:\n%s", buf.String())
	}
}

// TestPrintIndentsAndNewlines pins the output contract: two-space indented
// JSON plus exactly one trailing newline.
func TestPrintIndentsAndNewlines(t *testing.T) {
	var buf bytes.Buffer
	if err := Print(&buf, map[string]int{"a": 1}); err != nil {
		t.Fatalf("Print: %v", err)
	}
	want := "{\n  \"a\": 1\n}\n"
	if buf.String() != want {
		t.Fatalf("output = %q, want %q", buf.String(), want)
	}
}

// TestPrintSavingsFooterFormat pins the savings-banner format (consolidation
// tranche 8 — the shared home of the triplicated printSavingsFooter): the
// percentage's denominator is the labeled baseline, the token delta and the
// USD estimate are exact, and the whole line ends with one newline.
func TestPrintSavingsFooterFormat(t *testing.T) {
	var buf bytes.Buffer
	PrintSavingsFooter(&buf, 100, 50, 1e-5, "input verbatim")
	want := "[50% vs input verbatim ──> 50 tokens ──> $0.0005]\n"
	if buf.String() != want {
		t.Fatalf("output = %q, want %q", buf.String(), want)
	}
}

// TestPrintSavingsFooterEmptyBaseline: an empty baseline defaults to "input"
// so a bare "%" can never appear without its denominator (L2/honest
// reporting).
func TestPrintSavingsFooterEmptyBaseline(t *testing.T) {
	var buf bytes.Buffer
	PrintSavingsFooter(&buf, 100, 50, 1e-5, "")
	if !strings.Contains(buf.String(), "vs input ──>") {
		t.Fatalf("empty baseline did not default to \"input\": %q", buf.String())
	}
}

// TestPrintSavingsFooterSilenceOnNoSave: no reduction (equal or inflated
// token counts) prints nothing — a savings claim must never be emitted
// without a genuine reduction.
func TestPrintSavingsFooterSilenceOnNoSave(t *testing.T) {
	var buf bytes.Buffer
	PrintSavingsFooter(&buf, 100, 100, 1e-5, "input verbatim")
	if buf.Len() != 0 {
		t.Fatalf("equal tokens printed a footer: %q", buf.String())
	}
	buf.Reset()
	PrintSavingsFooter(&buf, 50, 100, 1e-5, "input verbatim")
	if buf.Len() != 0 {
		t.Fatalf("inflated tokens printed a footer: %q", buf.String())
	}
}

// TestPrintSavingsFooterFractionalPercent: the percentage (the savings
// share, (before-after)/before) is rounded to a whole number and the dollar
// amount keeps four decimals.
func TestPrintSavingsFooterFractionalPercent(t *testing.T) {
	var buf bytes.Buffer
	PrintSavingsFooter(&buf, 300, 100, 2e-5, "raw")
	want := "[67% vs raw ──> 200 tokens ──> $0.0040]\n"
	if buf.String() != want {
		t.Fatalf("output = %q, want %q", buf.String(), want)
	}
}
