package intel

import (
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

const srcKafka = `package kafka

func RetryPolicy() int { return 3 }

func DeliveryReport() {}

func HandleDeliveryReports() {}

func BrokerRestart() {}
`

// TestProbeAnchorsHyphenCompounds pins F4 (eval 2026-10-03): a task naming
// symbols through hyphenated prose ("retry-policy", "broker-restart") must
// anchor them via the compound-join resolution, not lose them because the
// bare tokens never match.
func TestProbeAnchorsHyphenCompounds(t *testing.T) {
	dir := writeTree(t, map[string]string{"kafka/kafka.go": srcKafka})
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	r := Probe(ix, "Check the retry-policy and broker-restart handling after a restart", 4000)
	got := map[string]bool{}
	for _, a := range r.Anchors {
		got[a.Resolved] = true
	}
	for _, want := range []string{"RetryPolicy", "BrokerRestart"} {
		if !got[want] {
			t.Fatalf("hyphen compound did not anchor %q; anchors: %v", want, got)
		}
	}
}

// TestProbePartialFallbackRecoversMissedSymbols pins the other half of F4:
// the fuzzy fallback used to fire only when NOTHING resolved, so a task that
// anchored some symbols never recovered camelCase tokens with a case/plural
// mismatch ("retryPolicy" vs RetryPolicy, "deliveryReport" vs
// HandleDeliveryReports).
func TestProbePartialFallbackRecoversMissedSymbols(t *testing.T) {
	dir := writeTree(t, map[string]string{"kafka/kafka.go": srcKafka})
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	// BrokerRestart resolves exactly; retryPolicy does not (case), and
	// deliveryReports neither — both must still anchor via the partial
	// camelCase fuzzy pass.
	r := Probe(ix, "BrokerRestart plus the retryPolicy and deliveryReports paths", 4000)
	got := map[string]bool{}
	for _, a := range r.Anchors {
		got[a.Resolved] = true
	}
	for _, want := range []string{"BrokerRestart", "RetryPolicy", "HandleDeliveryReports"} {
		if !got[want] {
			t.Fatalf("partial fallback did not recover %q; anchors: %v", want, got)
		}
	}
}
