package intel

import (
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

const srcProbePrecision = `package precise

func RenderReport() {}

func ValidateInput() {}

func AuditTrail() {}
`

// TestProbeSkipsProseKeywordNoiseAfterTwoExactAnchors pins the Phase 1
// precision rule: once a task already names two exact symbols, an ordinary
// prose word ("audit") must not add an unrelated fuzzy anchor. The tighter
// camelCase partial fallback remains available for case/plural misses.
func TestProbeSkipsProseKeywordNoiseAfterTwoExactAnchors(t *testing.T) {
	dir := writeTree(t, map[string]string{"precise/precise.go": srcProbePrecision})
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}

	report := Probe(ix, "RenderReport ValidateInput audit", 4000)
	got := map[string]bool{}
	for _, anchor := range report.Anchors {
		got[anchor.Resolved] = true
	}
	for _, want := range []string{"RenderReport", "ValidateInput"} {
		if !got[want] {
			t.Fatalf("exact symbol %q must anchor; got %v", want, got)
		}
	}
	if got["AuditTrail"] {
		t.Fatalf("ordinary prose keyword must not add unrelated fuzzy anchor AuditTrail; got %v", got)
	}
}
