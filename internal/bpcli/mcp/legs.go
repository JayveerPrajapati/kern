package mcp

import (
	"sort"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/blueprint/domain"
)

// validateResponse is the enriched MCP validate response: the canonical
// ValidationResult (status, findings, checks, ...) plus a per-leg verdict
// section so the caller sees each check's verdict individually with an honest
// blocking/advisory classification (P2.3). The embedded ValidationResult
// marshals its fields at the top level, so the response shape is strictly
// additive — existing consumers keep working.
type validateResponse struct {
	domain.ValidationResult
	// LegVerdicts lists every leg (check family) with its individual verdict
	// and kind. This is the detail view behind the aggregate Status.
	LegVerdicts []LegVerdict `json:"leg_verdicts"`
	// VerdictBasis is the one-line summary of which legs contributed to the
	// aggregate verdict (blocking vs advisory). The per-leg array is the
	// detail; this is the at-a-glance explanation.
	VerdictBasis string `json:"verdict_basis"`
}

// LegVerdict is one leg's verdict in the per-leg response section.
type LegVerdict struct {
	Leg     string `json:"leg"`
	Verdict string `json:"verdict"`
	// Kind is the honesty signal: "blocking" legs can produce BLOCK;
	// "advisory" legs only produce WARN/INFO (or NOT_RUN when skipped).
	Kind string `json:"kind"`
}

// buildValidateResponse enriches a ValidationResult with the per-leg verdict
// section. It derives everything from the existing Checks + ChecksSkipped —
// nothing is recomputed.
func buildValidateResponse(v domain.ValidationResult) validateResponse {
	return validateResponse{
		ValidationResult: v,
		LegVerdicts:      buildLegVerdicts(v),
		VerdictBasis:     buildVerdictBasis(v),
	}
}

// legNameFromCheck maps a check name to its short leg identifier: the family
// prefix before the first colon ("architecture:guard" -> "architecture",
// "secret:gitleaks" -> "secret"). A name without a colon is its own leg.
func legNameFromCheck(name string) string {
	if i := strings.Index(name, ":"); i > 0 {
		return name[:i]
	}
	return name
}

// verdictRank orders verdicts by severity for merging when two checks map to
// the same leg (e.g. secret:scan and secret:gitleaks both becoming "secret"):
// the most severe verdict wins. NOT_RUN only ever comes from ChecksSkipped
// and never overrides a real verdict.
func verdictRank(verdict string) int {
	switch verdict {
	case "BLOCK":
		return 5
	case "ERROR":
		return 4
	case "WARN":
		return 3
	case "PASS":
		return 2
	case "SKIP":
		return 1
	default: // "NOT_RUN"
		return 0
	}
}

// buildLegVerdicts derives one verdict per leg from the ValidationResult's
// Checks (status maps 1:1 to the verdict) plus ChecksSkipped (a skipped
// opt-in leg is NOT_RUN, P2.2). The kind comes from domain.CheckLegKind —
// the single source of truth. Returns nil when there are no checks and no
// skipped legs.
func buildLegVerdicts(v domain.ValidationResult) []LegVerdict {
	byLeg := make(map[string]LegVerdict)
	add := func(leg, verdict string, kind domain.LegKind) {
		existing, ok := byLeg[leg]
		if !ok || verdictRank(verdict) > verdictRank(existing.Verdict) {
			byLeg[leg] = LegVerdict{Leg: leg, Verdict: verdict, Kind: string(kind)}
		}
	}
	for _, cr := range v.Checks {
		add(legNameFromCheck(cr.Name), string(cr.Status), domain.CheckLegKind(cr.Name))
	}
	for _, skipped := range v.ChecksSkipped {
		// ChecksSkipped values are the user-facing short names (e.g.
		// "resilience"), already leg-shaped.
		add(skipped, "NOT_RUN", domain.CheckLegKind(skipped))
	}
	if len(byLeg) == 0 {
		return nil
	}
	// Deterministic order so repeated runs marshal identical JSON.
	names := make([]string, 0, len(byLeg))
	for name := range byLeg {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]LegVerdict, 0, len(names))
	for _, name := range names {
		out = append(out, byLeg[name])
	}
	return out
}

// buildVerdictBasis summarizes which legs contributed to the aggregate verdict
// (blocking vs advisory) in one line: "BLOCK due to: architecture",
// "PASS (advisory warnings: duplication)", "WARN (blocking: architecture;
// advisory: duplication)". The per-leg array carries the detail.
func buildVerdictBasis(v domain.ValidationResult) string {
	var blocking, advisory []string
	for _, lv := range buildLegVerdicts(v) {
		switch lv.Verdict {
		case "BLOCK", "ERROR", "WARN":
			if lv.Kind == string(domain.LegKindBlocking) {
				blocking = append(blocking, lv.Leg)
			} else {
				advisory = append(advisory, lv.Leg)
			}
		}
	}
	switch v.Status {
	case domain.StatusBlock, domain.StatusError:
		basis := string(v.Status) + " due to: " + strings.Join(blocking, ", ")
		if len(advisory) > 0 {
			basis += " (advisory: " + strings.Join(advisory, ", ") + ")"
		}
		return basis
	case domain.StatusWarn:
		var parts []string
		if len(blocking) > 0 {
			parts = append(parts, "blocking: "+strings.Join(blocking, ", "))
		}
		if len(advisory) > 0 {
			parts = append(parts, "advisory: "+strings.Join(advisory, ", "))
		}
		if len(parts) == 0 {
			return "WARN"
		}
		return "WARN (" + strings.Join(parts, "; ") + ")"
	case domain.StatusPass:
		if len(advisory) > 0 {
			return "PASS (advisory warnings: " + strings.Join(advisory, ", ") + ")"
		}
		if len(v.ChecksSkipped) > 0 {
			return "PASS (not run: " + strings.Join(v.ChecksSkipped, ", ") + ")"
		}
		return "PASS"
	default:
		return string(v.Status)
	}
}
