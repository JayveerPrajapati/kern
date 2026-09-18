package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/blueprint/domain"
)

// p23Result builds a representative ValidationResult exercising all four legs:
// two blocking (architecture, secret), one advisory with a WARN finding
// (duplication), and one advisory skipped (resilience). Projected-import
// violations are part of the architecture check, not a standalone leg.
func p23Result() domain.ValidationResult {
	return domain.ValidationResult{
		Status:   domain.StatusWarn,
		ExitCode: 0,
		Summary:  domain.Summary{Total: 1, Warnings: 1},
		Checks: []domain.CheckResult{
			{Name: "architecture:guard", Status: domain.StatusPass},
			{Name: "secret:gitleaks", Status: domain.StatusPass},
			{
				Name:   "duplication:jscpd",
				Status: domain.StatusWarn,
				Findings: []domain.Finding{{
					RuleID:   "duplication:advisory",
					Severity: domain.SeverityWarn,
					Category: domain.CategoryDuplication,
					Message:  "similar function detected",
					File:     "a.go",
				}},
			},
		},
		ChecksSkipped: []string{"resilience"},
	}
}

func p23FindLeg(t *testing.T, legs []LegVerdict, leg string) LegVerdict {
	t.Helper()
	for _, lv := range legs {
		if lv.Leg == leg {
			return lv
		}
	}
	t.Fatalf("leg %q not found in leg_verdicts: %+v", leg, legs)
	return LegVerdict{}
}

// TestP23_LegVerdictsHonestKinds: the per-leg verdicts must classify each leg
// as blocking (architecture/secrets) or advisory
// (duplication/resilience), with resilience NOT_RUN because it was skipped
// (P2.2), and an advisory duplication finding marked advisory (P1.1).
func TestP23_LegVerdictsHonestKinds(t *testing.T) {
	resp := buildValidateResponse(p23Result())

	arch := p23FindLeg(t, resp.LegVerdicts, "architecture")
	if arch.Verdict != "PASS" || arch.Kind != "blocking" {
		t.Errorf("architecture leg = %+v, want PASS/blocking", arch)
	}
	secret := p23FindLeg(t, resp.LegVerdicts, "secret")
	if secret.Verdict != "PASS" || secret.Kind != "blocking" {
		t.Errorf("secret leg = %+v, want PASS/blocking", secret)
	}
	dup := p23FindLeg(t, resp.LegVerdicts, "duplication")
	if dup.Verdict != "WARN" || dup.Kind != "advisory" {
		t.Errorf("duplication leg = %+v, want WARN/advisory (advisory finding must not be presented as blocking)", dup)
	}
	res := p23FindLeg(t, resp.LegVerdicts, "resilience")
	if res.Verdict != "NOT_RUN" || res.Kind != "advisory" {
		t.Errorf("resilience leg = %+v, want NOT_RUN/advisory (opt-in, skipped)", res)
	}
}

// TestP23_ResponseJSONIncludesLegVerdicts: the serialized MCP validate
// response must carry the leg_verdicts array and verdict_basis alongside the
// canonical ValidationResult fields (additive shape — status stays top-level).
func TestP23_ResponseJSONIncludesLegVerdicts(t *testing.T) {
	resp := buildValidateResponse(p23Result())
	raw, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("response is not valid JSON: %v\nraw: %s", err, raw)
	}
	if m["status"] != "WARN" {
		t.Errorf("top-level status = %v, want WARN (embedded ValidationResult must stay flat)", m["status"])
	}
	legs, ok := m["leg_verdicts"].([]interface{})
	if !ok || len(legs) != 4 {
		t.Fatalf("leg_verdicts = %v, want a 4-entry array", m["leg_verdicts"])
	}
	basis, ok := m["verdict_basis"].(string)
	if !ok {
		t.Fatalf("verdict_basis missing from response JSON: %s", raw)
	}
	if !strings.HasPrefix(basis, "WARN") {
		t.Errorf("verdict_basis = %q, want WARN-prefixed summary", basis)
	}
}

// TestP23_VerdictBasisDocumentsContributors: the one-line basis must name
// which legs contributed and whether they are blocking or advisory.
func TestP23_VerdictBasisDocumentsContributors(t *testing.T) {
	cases := []struct {
		name string
		vr   domain.ValidationResult
		want string
	}{
		{
			name: "block from blocking leg",
			vr: domain.ValidationResult{
				Status: domain.StatusBlock,
				Checks: []domain.CheckResult{
					{Name: "architecture:guard", Status: domain.StatusBlock},
					{Name: "duplication:jscpd", Status: domain.StatusPass},
				},
			},
			want: "BLOCK due to: architecture",
		},
		{
			name: "pass with advisory warning",
			vr: domain.ValidationResult{
				Status: domain.StatusWarn,
				Checks: []domain.CheckResult{
					{Name: "architecture:guard", Status: domain.StatusPass},
					{Name: "duplication:jscpd", Status: domain.StatusWarn},
				},
			},
			want: "WARN (advisory: duplication)",
		},
		{
			name: "warn from blocking and advisory legs",
			vr: domain.ValidationResult{
				Status: domain.StatusWarn,
				Checks: []domain.CheckResult{
					{Name: "architecture:guard", Status: domain.StatusWarn},
					{Name: "duplication:jscpd", Status: domain.StatusWarn},
				},
			},
			want: "WARN (blocking: architecture; advisory: duplication)",
		},
		{
			name: "clean pass with resilience skipped",
			vr: domain.ValidationResult{
				Status:        domain.StatusPass,
				Checks:        []domain.CheckResult{{Name: "architecture:guard", Status: domain.StatusPass}},
				ChecksSkipped: []string{"resilience"},
			},
			want: "PASS (not run: resilience)",
		},
		{
			name: "clean pass",
			vr: domain.ValidationResult{
				Status: domain.StatusPass,
				Checks: []domain.CheckResult{{Name: "architecture:guard", Status: domain.StatusPass}},
			},
			want: "PASS",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := buildVerdictBasis(tc.vr); got != tc.want {
				t.Errorf("verdict_basis = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestP23_ExplainFindingAdvisoryNote: explaining a duplication advisory
// finding must state that the leg is advisory-only and cannot block, with a
// reference to the duplication benchmark. Non-duplication findings must not
// carry the advisory note.
func TestP23_ExplainFindingAdvisoryNote(t *testing.T) {
	h := ExplainFindingHandler{}
	for _, ruleID := range []string{"duplication:advisory", "duplication:jscpd:clone"} {
		args, _ := json.Marshal(explainFindingArgs{Finding: domain.Finding{
			RuleID:   ruleID,
			Severity: domain.SeverityWarn,
			Message:  "similar code detected",
		}})
		tr := h.Handle(context.Background(), args)
		if tr.IsError {
			t.Fatalf("%s: expected result, got error: %s", ruleID, tr.Content[0].Text)
		}
		text := tr.Content[0].Text
		if !strings.Contains(text, "advisory") || !strings.Contains(text, "cannot block") {
			t.Errorf("%s: explanation must note advisory-only, non-blocking semantics:\n%s", ruleID, text)
		}
		if !strings.Contains(text, "docs/duplication-benchmark.md") {
			t.Errorf("%s: explanation must reference the duplication benchmark:\n%s", ruleID, text)
		}
	}

	// A blocking-leg finding must not be tagged advisory.
	args, _ := json.Marshal(explainFindingArgs{Finding: domain.Finding{
		RuleID:   "architecture:guard-violation",
		Severity: domain.SeverityBlock,
		Message:  "forbidden import",
	}})
	tr := h.Handle(context.Background(), args)
	if strings.Contains(tr.Content[0].Text, "cannot block") {
		t.Errorf("architecture finding must not carry the duplication advisory note:\n%s", tr.Content[0].Text)
	}
}
