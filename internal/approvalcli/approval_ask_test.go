package approvalcli

import (
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// The ask flow must reject an invalid [2] prefix before deciding the record.
// Before the preflight, a failed widening left the record "approved once";
// retrying the same ask could only report "already decided".
func TestValidateAskPrefixRejectsInvalidBeforeDecision(t *testing.T) {
	rec := domain.Approval{
		Status:    "pending",
		Command:   "make install EXTRA=1",
		RiskLevel: domain.RiskHigh,
	}

	err := validateAskPrefix(rec, "make deploy")
	if err == nil {
		t.Fatal("non-prefix accepted")
	}
	if !strings.Contains(err.Error(), "token prefix") {
		t.Fatalf("error does not teach the valid prefix contract: %v", err)
	}
	if err := validateAskPrefix(rec, ""); err == nil {
		t.Fatal("empty prefix accepted")
	}
	if err := validateAskPrefix(domain.Approval{Command: "legacy"}, ""); err == nil {
		t.Fatal("legacy command-less record accepted for prefix widening")
	}

	critical := rec
	critical.RiskLevel = domain.RiskCritical
	if err := validateAskPrefix(critical, "make install"); err == nil {
		t.Fatal("CRITICAL command accepted for prefix widening")
	}
	if err := validateAskPrefix(rec, "make install"); err != nil {
		t.Fatalf("valid two-token prefix rejected: %v", err)
	}
	if err := validateAskPrefix(rec, "make install EXTRA=1"); err != nil {
		t.Fatalf("full-command prefix rejected: %v", err)
	}
}
